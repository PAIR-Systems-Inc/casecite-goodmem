package app

import (
	"crypto/subtle"
	"database/sql"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

func (s *Server) configureGoogle() {
	if s.cfg.GoogleSSOClientID == "" || len(s.cfg.GoogleSSODomains) == 0 {
		return
	}
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(s.ctx, s.client), "https://www.googleapis.com/oauth2/v3/certs")
	s.googleVerifier = oidc.NewVerifier("https://accounts.google.com", keys, &oidc.Config{ClientID: s.cfg.GoogleSSOClientID, SupportedSigningAlgs: []string{"RS256"}})
}

func (s *Server) googleConfig(q request) (any, error) {
	if s.googleVerifier == nil {
		return nil, fail(503, "Google single sign-on is not configured")
	}
	state, nonce := token(), token()
	if err := s.kvSet(q.r.Context(), "google-sso:"+digest(state), M{"nonce": digest(nonce), "user_id": q.user.ID}, 10*time.Minute); err != nil {
		return nil, err
	}
	s.setCookie(q.w, "google_sso_state", state, true, time.Now().Add(10*time.Minute))
	domain := s.cfg.GoogleSSODomains[0]
	if len(s.cfg.GoogleSSODomains) > 1 {
		domain = "*"
	}
	linked := false
	if q.user.ID != "" {
		if err := s.db.QueryRowContext(q.r.Context(), `SELECT google_subject IS NOT NULL FROM cc_users WHERE id=$1`, q.user.ID).Scan(&linked); err != nil {
			return nil, err
		}
	}
	return M{"client_id": s.cfg.GoogleSSOClientID, "nonce": nonce, "hosted_domain": domain, "linked": linked}, nil
}

func (s *Server) googleLogin(q request) (any, error) {
	if s.googleVerifier == nil {
		return nil, fail(503, "Google single sign-on is not configured")
	}
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	verified, err := s.googleVerifier.Verify(q.r.Context(), str(m, "token"))
	if err != nil {
		return nil, fail(401, "Invalid or expired Google identity token")
	}
	var claims struct {
		Email           string `json:"email"`
		Verified        bool   `json:"email_verified"`
		Domain          string `json:"hd"`
		Name            string `json:"name"`
		AuthorizedParty string `json:"azp"`
	}
	if err = verified.Claims(&claims); err != nil || verified.Subject == "" || len(verified.Subject) > 255 || !claims.Verified || !slices.Contains(s.cfg.GoogleSSODomains, strings.ToLower(claims.Domain)) || claims.AuthorizedParty != "" && claims.AuthorizedParty != s.cfg.GoogleSSOClientID {
		return nil, fail(401, "Google identity must have a verified email and belong to an allowed Google Workspace domain")
	}
	cookie, err := q.r.Cookie("google_sso_state")
	if err != nil || len(cookie.Value) != 64 {
		return nil, fail(401, "Sign-in state expired. Start Google sign-in again.")
	}
	var state M
	if err = s.kvGet(q.r.Context(), "google-sso:"+digest(cookie.Value), &state); err != nil {
		if notFound(err) {
			return nil, fail(401, "Sign-in state expired. Start Google sign-in again.")
		}
		return nil, err
	}
	if str(state, "user_id") != q.user.ID {
		return nil, fail(401, "Google sign-in belongs to a different browser session")
	}
	if verified.Nonce == "" || subtle.ConstantTimeCompare([]byte(str(state, "nonce")), []byte(digest(verified.Nonce))) != 1 {
		return nil, fail(401, "Google sign-in nonce does not match")
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
	res, err := tx.ExecContext(q.r.Context(), `DELETE FROM cc_kv WHERE key=$1 AND expires_at>now()`, "google-sso:"+digest(cookie.Value))
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, fail(401, "Google sign-in has already been used")
	}
	if q.user.ID != "" {
		var email string
		var existing sql.NullString
		if err = tx.QueryRowContext(q.r.Context(), `SELECT email,google_subject FROM cc_users WHERE id=$1 AND active FOR UPDATE`, q.user.ID).Scan(&email, &existing); err != nil {
			return nil, err
		}
		if !strings.EqualFold(strings.TrimSpace(claims.Email), email) || existing.Valid && existing.String != verified.Subject {
			return nil, fail(403, "Link the Google account with the same verified email as your CaseCite account")
		}
		var used bool
		if err = tx.QueryRowContext(q.r.Context(), `SELECT EXISTS(SELECT 1 FROM cc_users WHERE google_subject=$1 AND id<>$2)`, verified.Subject, q.user.ID).Scan(&used); err != nil {
			return nil, err
		}
		if used {
			return nil, fail(409, "This Google identity is already linked to another account")
		}
		if _, err = tx.ExecContext(q.r.Context(), `UPDATE cc_users SET google_subject=$1 WHERE id=$2`, verified.Subject, q.user.ID); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		s.setCookie(q.w, "google_sso_state", "", true, time.Unix(1, 0))
		s.audit(q.r.Context(), q.user.ID, "user.google.link", "user", q.user.ID, M{})
		return M{"status": "linked"}, nil
	}
	var id string
	var active bool
	err = tx.QueryRowContext(q.r.Context(), `SELECT id,active FROM cc_users WHERE google_subject=$1 FOR UPDATE`, verified.Subject).Scan(&id, &active)
	if notFound(err) {
		email := strings.ToLower(strings.TrimSpace(claims.Email))
		if !strings.Contains(email, "@") || len(email) > 254 {
			return nil, bad("Google did not supply a usable email address")
		}
		var role, oid string
		var mustChange bool
		var last sql.NullTime
		err = tx.QueryRowContext(q.r.Context(), `SELECT id,active,role,must_change_password,last_login,COALESCE(google_subject,azure_oid,'') FROM cc_users WHERE email=$1 FOR UPDATE`, email).Scan(&id, &active, &role, &mustChange, &last, &oid)
		if err == nil {
			// Email is only an invitation lookup, never an identity key. An existing
			// account in use or an admin cannot be taken over through email matching.
			if !active || role == "admin" || !mustChange || last.Valid || oid != "" {
				return nil, fail(403, "This email belongs to an existing account that cannot be linked automatically. Ask an administrator.")
			}
			_, err = tx.ExecContext(q.r.Context(), `UPDATE cc_users SET google_subject=$1,password_hash='',must_change_password=false WHERE id=$2`, verified.Subject, id)
		} else if notFound(err) {
			if !s.cfg.GoogleAutoProvision {
				return nil, fail(403, "Ask an administrator to invite you before signing in")
			}
			role = s.cfg.GoogleDefaultRole
			if !slices.Contains([]string{"attorney", "paralegal", "viewer"}, role) {
				role = "viewer"
			}
			id, active = uuid(), true
			_, err = tx.ExecContext(q.r.Context(), `INSERT INTO cc_users(id,email,name,password_hash,role,google_subject) VALUES($1,$2,$3,'',$4,$5)`, id, email, claims.Name, role, verified.Subject)
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
	s.setCookie(q.w, "google_sso_state", "", true, time.Unix(1, 0))
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
