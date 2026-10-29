package app

import (
	"crypto/subtle"
	"database/sql"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

func (s *Server) configureAzure() {
	if s.cfg.MicrosoftClientID == "" || s.cfg.MicrosoftTenantID == "" {
		return
	}
	tenant := url.PathEscape(s.cfg.MicrosoftTenantID)
	issuer := "https://login.microsoftonline.com/" + tenant + "/v2.0"
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(s.ctx, s.client), "https://login.microsoftonline.com/"+tenant+"/discovery/v2.0/keys")
	s.azureVerifier = oidc.NewVerifier(issuer, keys, &oidc.Config{ClientID: s.cfg.MicrosoftClientID, SupportedSigningAlgs: []string{"RS256"}})
}

func (s *Server) azureConfig(q request) (any, error) {
	if s.azureVerifier == nil {
		return nil, fail(503, "Microsoft single sign-on is not configured")
	}
	state, nonce := token(), token()
	if err := s.kvSet(q.r.Context(), "sso:"+digest(state), M{"nonce": digest(nonce)}, 10*time.Minute); err != nil {
		return nil, err
	}
	s.setCookie(q.w, "sso_state", state, true, time.Now().Add(10*time.Minute))
	redirect := s.cfg.MicrosoftRedirectURI
	if redirect == "" {
		redirect = s.cfg.PublicURL + "/auth/microsoft-callback"
	}
	return M{"authority": "https://login.microsoftonline.com/" + url.PathEscape(s.cfg.MicrosoftTenantID), "client_id": s.cfg.MicrosoftClientID, "redirect_uri": redirect, "scopes": []string{"openid", "profile", "email"}, "nonce": nonce}, nil
}

func (s *Server) azureLogin(q request) (any, error) {
	if s.azureVerifier == nil {
		return nil, fail(503, "Microsoft single sign-on is not configured")
	}
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	verified, err := s.azureVerifier.Verify(q.r.Context(), str(m, "token"))
	if err != nil {
		return nil, fail(401, "Invalid or expired Microsoft identity token")
	}
	var claims struct {
		OID      string `json:"oid"`
		Tenant   string `json:"tid"`
		Email    string `json:"email"`
		Username string `json:"preferred_username"`
		Name     string `json:"name"`
	}
	if err = verified.Claims(&claims); err != nil || claims.OID == "" || claims.Tenant != s.cfg.MicrosoftTenantID || verified.Subject == "" {
		return nil, fail(401, "Microsoft identity does not belong to this organization")
	}
	cookie, err := q.r.Cookie("sso_state")
	if err != nil || len(cookie.Value) != 64 {
		return nil, fail(401, "Sign-in state expired. Start Microsoft sign-in again.")
	}
	var state M
	if err = s.kvGet(q.r.Context(), "sso:"+digest(cookie.Value), &state); err != nil {
		if notFound(err) {
			return nil, fail(401, "Sign-in state expired. Start Microsoft sign-in again.")
		}
		return nil, err
	}
	if verified.Nonce == "" || subtle.ConstantTimeCompare([]byte(str(state, "nonce")), []byte(digest(verified.Nonce))) != 1 {
		return nil, fail(401, "Microsoft sign-in nonce does not match")
	}
	tx, err := s.db.BeginTx(q.r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Consume the browser-bound challenge once, and serialize first-login linking.
	if _, err = tx.ExecContext(q.r.Context(), `SELECT pg_advisory_xact_lock(734280002)`); err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(q.r.Context(), `DELETE FROM cc_kv WHERE key=$1 AND expires_at>now()`, "sso:"+digest(cookie.Value))
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, fail(401, "Microsoft sign-in has already been used")
	}
	var id string
	var active bool
	err = tx.QueryRowContext(q.r.Context(), `SELECT id,active FROM cc_users WHERE azure_tenant=$1 AND azure_oid=$2 FOR UPDATE`, claims.Tenant, claims.OID).Scan(&id, &active)
	if notFound(err) {
		email := strings.ToLower(strings.TrimSpace(claims.Email))
		if email == "" {
			email = strings.ToLower(strings.TrimSpace(claims.Username))
		}
		if !strings.Contains(email, "@") || len(email) > 254 {
			return nil, bad("Microsoft did not supply a usable email address")
		}
		var role, oid string
		var mustChange bool
		var last sql.NullTime
		err = tx.QueryRowContext(q.r.Context(), `SELECT id,active,role,must_change_password,last_login,COALESCE(azure_oid,'') FROM cc_users WHERE email=$1 FOR UPDATE`, email).Scan(&id, &active, &role, &mustChange, &last, &oid)
		if err == nil {
			// Email is only an invitation lookup, never an identity key. An existing
			// account in use or an admin cannot be taken over through email matching.
			if !active || role == "admin" || !mustChange || last.Valid || oid != "" {
				return nil, fail(403, "This email belongs to an existing account that cannot be linked automatically. Ask an administrator.")
			}
			_, err = tx.ExecContext(q.r.Context(), `UPDATE cc_users SET azure_oid=$1,azure_tenant=$2,password_hash='',must_change_password=false WHERE id=$3`, claims.OID, claims.Tenant, id)
		} else if notFound(err) {
			if !s.cfg.AzureAutoProvision {
				return nil, fail(403, "Ask an administrator to invite you before signing in")
			}
			role = s.cfg.AzureDefaultRole
			if !slices.Contains([]string{"attorney", "paralegal", "viewer"}, role) {
				role = "viewer"
			}
			id, active = uuid(), true
			_, err = tx.ExecContext(q.r.Context(), `INSERT INTO cc_users(id,email,name,password_hash,role,azure_oid,azure_tenant) VALUES($1,$2,$3,'',$4,$5,$6)`, id, email, claims.Name, role, claims.OID, claims.Tenant)
		}
	}
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, fail(403, "Account is disabled")
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.setCookie(q.w, "sso_state", "", true, time.Unix(1, 0))
	u, err := s.loadUser(q.r.Context(), id)
	if err != nil {
		return nil, err
	}
	if u.MFAEnabled {
		challenge := token()
		if err = s.kvSet(q.r.Context(), "mfa-challenge:"+digest(challenge), M{"user_id": u.ID}, 5*time.Minute); err != nil {
			return nil, err
		}
		return M{"mfa_required": true, "mfa_token": challenge}, nil
	}
	return s.signIn(q, u)
}
