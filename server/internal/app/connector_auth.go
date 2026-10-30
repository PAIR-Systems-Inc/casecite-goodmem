package app

import (
	"context"
	"crypto/md5" // Filevine's published API handshake requires MD5.
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

type connectorAccount struct {
	Generation           string
	Credentials, Account M
	LastSync             *time.Time
}
type connectorOAuth struct {
	Authorization, Token, API, Scope string
	Basic                            bool
}

func connectorOAuthEndpoints(d connectorDefinition, v map[string]string) connectorOAuth {
	switch d.ID {
	case "google_drive":
		return connectorOAuth{Authorization: "https://accounts.google.com/o/oauth2/v2/auth", Token: "https://oauth2.googleapis.com/token", API: "https://www.googleapis.com", Scope: "https://www.googleapis.com/auth/drive.readonly https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile"}
	case "onedrive":
		base := "https://login.microsoftonline.com/" + url.PathEscape(fallback(v["microsoft_tenant_id"], "common")) + "/oauth2/v2.0"
		return connectorOAuth{Authorization: base + "/authorize", Token: base + "/token", API: "https://graph.microsoft.com/v1.0", Scope: "Files.Read.All Sites.Read.All User.Read offline_access"}
	case "box":
		return connectorOAuth{Authorization: "https://account.box.com/api/oauth2/authorize", Token: "https://api.box.com/oauth2/token", API: "https://api.box.com/2.0"}
	case "dropbox":
		return connectorOAuth{Authorization: "https://www.dropbox.com/oauth2/authorize", Token: "https://api.dropboxapi.com/oauth2/token", API: "https://api.dropboxapi.com/2"}
	case "clio":
		base := strings.TrimRight(v["clio_base_url"], "/")
		return connectorOAuth{Authorization: base + "/oauth/authorize", Token: base + "/oauth/token", API: base + "/api/v4"}
	case "netdocuments":
		base := strings.TrimRight(v["netdocuments_api_host"], "/")
		return connectorOAuth{Authorization: strings.TrimRight(v["netdocuments_vault_host"], "/") + "/neWeb2/OAuth.aspx", Token: base + "/v1/OAuth", API: base + "/v2", Scope: "read", Basic: true}
	case "imanage":
		base := strings.TrimRight(v["imanage_base_url"], "/")
		return connectorOAuth{Authorization: base + "/auth/oauth2/authorize", Token: base + "/auth/oauth2/token", API: base + "/work/api/v2", Scope: "user"}
	case "filevine":
		return connectorOAuth{Token: strings.TrimRight(v["filevine_base_url"], "/") + "/session", API: strings.TrimRight(v["filevine_base_url"], "/")}
	}
	return connectorOAuth{}
}
func connectorConfigHash(d connectorDefinition, v map[string]string) string {
	return digest(string(jsonBytes(M{"client_id": v[d.IDField], "secret": v[d.SecretField], "redirect": v[d.RedirectField], "endpoints": connectorOAuthEndpoints(d, v)})))
}
func connectorAuthURL(d connectorDefinition, v map[string]string, state string) string {
	o := connectorOAuthEndpoints(d, v)
	params := url.Values{"client_id": {v[d.IDField]}, "redirect_uri": {v[d.RedirectField]}, "response_type": {"code"}, "state": {state}}
	if o.Scope != "" {
		params.Set("scope", o.Scope)
	}
	switch d.ID {
	case "google_drive":
		params.Set("access_type", "offline")
		params.Set("prompt", "consent")
	case "onedrive":
		params.Set("response_mode", "query")
	case "dropbox":
		params.Set("token_access_type", "offline")
	}
	return o.Authorization + "?" + params.Encode()
}
func (s *Server) loadConnectorAccount(ctx context.Context, user, provider string) (connectorAccount, error) {
	account := connectorAccount{}
	var encrypted string
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT generation,credentials,account,last_sync FROM cc_connector_accounts WHERE user_id=$1 AND provider=$2`, user, provider).Scan(&account.Generation, &encrypted, &raw, &account.LastSync)
	if err != nil {
		return account, err
	}
	account.Credentials = M{}
	if encrypted != "" {
		plain, e := s.crypt(encrypted, true)
		if e != nil {
			return account, e
		}
		if e = json.Unmarshal([]byte(plain), &account.Credentials); e != nil {
			return account, e
		}
	}
	err = json.Unmarshal(raw, &account.Account)
	return account, err
}
func connectorCookie(state string) string { return "cc_connector_" + digest(state)[:16] }

func (s *Server) beginConnectorAuth(q request) (any, error) {
	d, err := s.connectorDefinition(q.r.PathValue("provider"))
	if err != nil {
		return nil, err
	}
	if err = connectorPermit(q.user, d); err != nil {
		return nil, err
	}
	v, err := s.connectorConfig(q.r.Context(), d.Settings)
	if err != nil {
		return nil, err
	}
	if !s.connectorConfigured(d, v) {
		return nil, bad(d.Label + " is not configured. An administrator must configure its credentials.")
	}
	ctx := q.r.Context()
	generation := uuid()
	if err = s.db.QueryRowContext(ctx, `INSERT INTO cc_connector_accounts(user_id,provider,generation,credentials) VALUES($1,$2,$3,'') ON CONFLICT(user_id,provider) DO UPDATE SET updated_at=cc_connector_accounts.updated_at RETURNING generation`, q.user.ID, d.ID, generation).Scan(&generation); err != nil {
		return nil, err
	}
	state, binding := token(), token()
	if err = s.kvSet(ctx, "connector-oauth:"+digest(state), M{"user_id": q.user.ID, "provider": d.ID, "generation": generation, "binding": digest(binding), "config": connectorConfigHash(d, v)}, 10*time.Minute); err != nil {
		return nil, err
	}
	http.SetCookie(q.w, &http.Cookie{Name: connectorCookie(state), Value: binding, Path: "/api/v1/connectors/" + d.ID + "/", HttpOnly: true, Secure: !s.cfg.Debug, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	// A same-origin launch URL supports administrator-configured regional and
	// on-premise providers without allowing arbitrary browser redirect targets.
	authURL := s.cfg.PublicURL + "/api/v1/connectors/" + d.ID + "/authorize?state=" + url.QueryEscape(state)
	return M{"auth_url": authURL, "state": state}, nil
}
func (s *Server) launchConnectorAuth(q request) (any, error) {
	d, err := s.connectorDefinition(q.r.PathValue("provider"))
	if err != nil {
		return nil, err
	}
	state := q.r.URL.Query().Get("state")
	flow := M{}
	if state == "" || len(state) > 256 || s.kvGet(q.r.Context(), "connector-oauth:"+digest(state), &flow) != nil {
		return nil, bad("Invalid or expired OAuth state. Please connect again.")
	}
	cookie, err := q.r.Cookie(connectorCookie(state))
	if err != nil || str(flow, "provider") != d.ID || subtle.ConstantTimeCompare([]byte(digest(cookie.Value)), []byte(str(flow, "binding"))) != 1 {
		return nil, fail(403, "This connection was started from a different browser session. Please connect again.")
	}
	u, err := s.loadUser(q.r.Context(), str(flow, "user_id"))
	if err != nil {
		return nil, fail(403, "The account is no longer available")
	}
	if err = connectorPermit(u, d); err != nil {
		return nil, err
	}
	v, err := s.connectorConfig(q.r.Context(), d.Settings)
	if err != nil {
		return nil, err
	}
	if !s.connectorConfigured(d, v) || str(flow, "config") != connectorConfigHash(d, v) {
		return nil, bad("The connector configuration changed. Please connect again.")
	}
	target := connectorAuthURL(d, v, state)
	if d.ID == "filevine" {
		target = s.cfg.PublicURL + "/api/v1/connectors/filevine/callback?state=" + url.QueryEscape(state) + "&code=session"
	}
	q.w.Header().Set("Cache-Control", "no-store")
	http.Redirect(q.w, q.r, target, http.StatusFound)
	return nil, nil
}
func connectorPermit(user User, d connectorDefinition) error {
	if !slices.Contains(user.Permissions, "connectors.manage") {
		return fail(403, "Your role does not include connectors.manage")
	}
	if d.AdminOnly && !slices.Contains(user.Permissions, "admin.settings") {
		return fail(403, "This connector uses a firm-wide credential and can only be used by an administrator.")
	}
	if user.MustChangePassword || user.MFAEnrollmentRequired {
		return fail(403, "Complete the account sign-in requirements before connecting a provider")
	}
	return nil
}
func (s *Server) connectorCallback(q request) (any, error) {
	ctx := q.r.Context()
	d, err := s.connectorDefinition(q.r.PathValue("provider"))
	if err != nil {
		return nil, err
	}
	state, code := q.r.URL.Query().Get("state"), q.r.URL.Query().Get("code")
	if len(state) > 256 || state == "" || len(code) > 8192 {
		return nil, bad("Invalid OAuth response")
	}
	var raw []byte
	err = s.db.QueryRowContext(ctx, `DELETE FROM cc_kv WHERE key=$1 AND expires_at>now() RETURNING value`, "connector-oauth:"+digest(state)).Scan(&raw)
	if notFound(err) {
		return nil, bad("Invalid or expired OAuth state. Please connect again.")
	}
	if err != nil {
		return nil, err
	}
	flow := M{}
	if json.Unmarshal(raw, &flow) != nil {
		return nil, bad("Invalid OAuth state")
	}
	cookie, e := q.r.Cookie(connectorCookie(state))
	http.SetCookie(q.w, &http.Cookie{Name: connectorCookie(state), Value: "", Path: "/api/v1/connectors/" + d.ID + "/", HttpOnly: true, Secure: !s.cfg.Debug, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	if e != nil || str(flow, "provider") != d.ID || subtle.ConstantTimeCompare([]byte(digest(cookie.Value)), []byte(str(flow, "binding"))) != 1 {
		return nil, fail(403, "This connection was started from a different browser session. Please connect again.")
	}
	user, err := s.loadUser(ctx, str(flow, "user_id"))
	if err != nil {
		return nil, fail(403, "The account is no longer available")
	}
	if err = connectorPermit(user, d); err != nil {
		return nil, err
	}
	v, err := s.connectorConfig(ctx, d.Settings)
	if err != nil {
		return nil, err
	}
	if !s.connectorConfigured(d, v) || str(flow, "config") != connectorConfigHash(d, v) {
		return nil, bad("The connector configuration changed. Please connect again.")
	}
	if code == "" || q.r.URL.Query().Get("error") != "" {
		return nil, bad("The provider did not authorize this connection. Please connect again.")
	}
	creds, err := s.exchangeConnectorToken(ctx, d, v, "authorization_code", code, M{})
	if err != nil {
		return nil, bad("Failed to connect. Please try again.")
	}
	account, err := s.connectorAccountInfo(ctx, d, v, creds)
	if err != nil {
		s.revokeConnector(ctx, d, v, creds)
		return nil, bad("Could not read the connected account. Please try again.")
	}
	// Provider consent can take time. Recheck role/enrollment requirements
	// after the external response, before persisting the grant.
	user, err = s.loadUser(ctx, user.ID)
	if err == nil {
		err = connectorPermit(user, d)
	}
	if err != nil {
		s.revokeConnector(ctx, d, v, creds)
		return nil, fail(403, "The account can no longer connect this provider")
	}
	encrypted, err := s.crypt(string(jsonBytes(creds)), false)
	if err != nil {
		return nil, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE cc_connector_accounts SET generation=$4,credentials=$5,account=$6,last_sync=NULL,updated_at=now() WHERE user_id=$1 AND provider=$2 AND generation=$3 AND EXISTS(SELECT 1 FROM cc_users WHERE id=$1 AND active)`, user.ID, d.ID, str(flow, "generation"), uuid(), encrypted, jsonBytes(account))
	if err != nil {
		return nil, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		s.revokeConnector(ctx, d, v, creds)
		return nil, fail(409, "This connection was cancelled or changed. Please connect again.")
	}
	s.audit(ctx, user.ID, "connector.connect", "connector", d.ID, M{})
	if strings.Contains(q.r.Header.Get("Accept"), "text/html") {
		nonce := token()
		q.w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; base-uri 'none'; frame-ancestors 'none'")
		q.w.Header().Set("Content-Type", "text/html; charset=utf-8")
		q.w.Header().Set("Cache-Control", "no-store")
		_, err = q.w.Write([]byte(`<!doctype html><html lang="en"><meta charset="utf-8"><title>Account connected</title><p>Account connected. You can close this window and return to CaseCite.</p><script nonce="` + nonce + `">window.close()</script></html>`))
		return nil, err
	}
	return M{"status": "connected", "account": account}, nil
}

func (s *Server) revokeUserConnectors(ctx context.Context, user string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var pending sync.WaitGroup
	for _, definition := range connectorDefinitions {
		pending.Go(func() {
			account, err := s.loadConnectorAccount(ctx, user, definition.ID)
			if err != nil || str(account.Credentials, "access_token") == "" {
				return
			}
			values, err := s.connectorConfig(ctx, definition.Settings)
			if err == nil {
				s.revokeConnector(ctx, definition, values, account.Credentials)
			}
		})
	}
	pending.Wait()
}

func (s *Server) exchangeConnectorToken(ctx context.Context, d connectorDefinition, v map[string]string, grant, value string, previous M) (M, error) {
	o := connectorOAuthEndpoints(d, v)
	headers := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
	params := url.Values{"grant_type": {grant}, "client_id": {v[d.IDField]}, "client_secret": {v[d.SecretField]}}
	if grant == "authorization_code" {
		params.Set("code", value)
		params.Set("redirect_uri", v[d.RedirectField])
	} else {
		params.Set("refresh_token", value)
	}
	if d.ID == "onedrive" {
		params.Set("scope", o.Scope)
	}
	if o.Basic {
		params.Del("client_id")
		params.Del("client_secret")
		headers.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(v[d.IDField]+":"+v[d.SecretField])))
	}
	payload := []byte(params.Encode())
	if d.ID == "filevine" {
		stamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
		sum := md5.Sum([]byte(v[d.IDField] + "/" + stamp + "/" + v[d.SecretField]))
		payload = jsonBytes(M{"mode": "key", "apiKey": v[d.IDField], "apiHash": hex.EncodeToString(sum[:]), "apiTimestamp": stamp})
		headers.Set("Content-Type", "application/json")
	}
	// Authorization codes and rotating refresh tokens are never automatically
	// replayed after a timeout: the first request might already have succeeded.
	tokens, err := s.connectorJSON(ctx, "POST", o.Token, headers, payload, false)
	if err != nil {
		return nil, err
	}
	creds := M{}
	for k, v := range previous {
		creds[k] = v
	}
	if d.ID == "filevine" {
		creds["access_token"] = fallback(str(tokens, "accessToken"), str(tokens, "authToken"))
		creds["user_id"], creds["org_id"] = connectorNative(tokens["userId"]), connectorNative(tokens["orgId"])
		creds["expires_at"] = time.Now().Add(10 * time.Minute).Unix()
	} else {
		creds["access_token"] = tokens["access_token"]
		if str(tokens, "refresh_token") != "" {
			creds["refresh_token"] = tokens["refresh_token"]
		}
		for _, key := range []string{"customer_id", "account_id"} {
			if tokens[key] != nil {
				creds[key] = tokens[key]
			}
		}
		ttl := number(tokens, "expires_in", 3600)
		if d.ID == "dropbox" {
			ttl = number(tokens, "expires_in", 14400)
		}
		creds["expires_at"] = time.Now().Add(time.Duration(max(1, min(ttl, 365*86400))) * time.Second).Unix()
	}
	if str(creds, "access_token") == "" {
		return nil, fail(502, "The provider response omitted its access token")
	}
	creds["client_id"] = v[d.IDField]
	return creds, nil
}
func connectorNative(value any) string {
	if m, ok := value.(map[string]any); ok {
		return chatSourceID(m["native"])
	}
	return chatSourceID(value)
}
func connectorHeaders(d connectorDefinition, creds M) http.Header {
	h := connectorBearer(str(creds, "access_token"))
	if d.ID == "filevine" {
		for k, v := range map[string]string{"x-fv-userid": str(creds, "user_id"), "x-fv-orgid": str(creds, "org_id")} {
			if v != "" {
				h.Set(k, v)
			}
		}
	}
	return h
}

func (s *Server) connectorCredentials(ctx context.Context, user string, d connectorDefinition, v map[string]string, expected string) (connectorAccount, error) {
	account, err := s.loadConnectorAccount(ctx, user, d.ID)
	if err != nil {
		return account, fail(409, "Connector not connected")
	}
	if expected != "" && account.Generation != expected {
		return account, fail(409, "The connector was disconnected or reconnected")
	}
	if str(account.Credentials, "access_token") == "" || str(account.Credentials, "client_id") != v[d.IDField] {
		return account, fail(409, "Reconnect this connector before importing files")
	}
	if judgeNumber(account.Credentials, "expires_at") > float64(time.Now().Unix()+60) && (d.ID != "imanage" || chatSourceID(account.Credentials["customer_id"]) != "") {
		return account, nil
	}
	// The row lock serializes rotating-token refresh across Go replicas and
	// disconnect. Only this existing generation may be updated.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return account, err
	}
	defer tx.Rollback()
	var encrypted, generation string
	err = tx.QueryRowContext(ctx, `SELECT generation,credentials FROM cc_connector_accounts WHERE user_id=$1 AND provider=$2 FOR UPDATE`, user, d.ID).Scan(&generation, &encrypted)
	if err != nil || generation != account.Generation || encrypted == "" {
		return account, fail(409, "The connector was disconnected or changed")
	}
	plain, err := s.crypt(encrypted, true)
	if err != nil {
		return account, err
	}
	creds := M{}
	if err = json.Unmarshal([]byte(plain), &creds); err != nil {
		return account, err
	}
	if judgeNumber(creds, "expires_at") <= float64(time.Now().Unix()+60) {
		if str(creds, "refresh_token") == "" && d.ID != "filevine" {
			return account, fail(409, "The connector grant expired. Reconnect the account.")
		}
		creds, err = s.exchangeConnectorToken(ctx, d, v, "refresh_token", str(creds, "refresh_token"), creds)
		if err != nil {
			return account, fail(502, "Could not refresh the connector grant. Reconnect if the provider has revoked access.")
		}
	}
	if d.ID == "imanage" && chatSourceID(creds["customer_id"]) == "" {
		if err = s.connectorDiscoverCustomer(ctx, d, v, creds); err != nil {
			return account, err
		}
	}
	encrypted, err = s.crypt(string(jsonBytes(creds)), false)
	if err != nil {
		return account, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE cc_connector_accounts SET credentials=$3,updated_at=now() WHERE user_id=$1 AND provider=$2`, user, d.ID, encrypted); err != nil {
		return account, err
	}
	if err = tx.Commit(); err != nil {
		return account, err
	}
	account.Credentials = creds
	return account, nil
}
func (s *Server) connectorDiscoverCustomer(ctx context.Context, d connectorDefinition, v map[string]string, creds M) error {
	o := connectorOAuthEndpoints(d, v)
	data, err := s.connectorJSON(ctx, "GET", o.API+"/customers", connectorHeaders(d, creds), nil, true)
	if err != nil {
		return err
	}
	rows := maps(data["data"])
	if len(rows) == 0 || chatSourceID(rows[0]["id"]) == "" {
		return fail(502, "iManage returned no customer for this account")
	}
	creds["customer_id"] = chatSourceID(rows[0]["id"])
	return nil
}
func (s *Server) connectorAccountInfo(ctx context.Context, d connectorDefinition, v map[string]string, creds M) (M, error) {
	o := connectorOAuthEndpoints(d, v)
	method, path := "GET", ""
	var params url.Values
	switch d.ID {
	case "google_drive":
		path = "/oauth2/v2/userinfo"
	case "onedrive":
		path = "/me"
	case "box":
		path = "/users/me"
	case "dropbox":
		method, path = "POST", "/users/get_current_account"
	case "clio":
		path = "/users/who_am_i.json"
		params = url.Values{"fields": {"id,name,email"}}
	case "netdocuments":
		path = "/User/info"
	case "filevine":
		path = "/core/users/me"
	case "imanage":
		if chatSourceID(creds["customer_id"]) == "" {
			if err := s.connectorDiscoverCustomer(ctx, d, v, creds); err != nil {
				return nil, err
			}
		}
		path = "/customers/" + url.PathEscape(chatSourceID(creds["customer_id"])) + "/users/me"
	}
	data, err := s.connectorJSON(ctx, method, connectorURL(o.API, path, params), connectorHeaders(d, creds), nil, true)
	if err != nil {
		return nil, err
	}
	if d.ID == "clio" || d.ID == "imanage" {
		data = object(data["data"])
	}
	name, email := data["name"], data["email"]
	switch d.ID {
	case "onedrive":
		name, email = data["displayName"], fallback(str(data, "mail"), str(data, "userPrincipalName"))
	case "box":
		email = data["login"]
	case "dropbox":
		name = object(data["name"])["display_name"]
	case "netdocuments":
		name = data["displayName"]
	case "filevine":
		name = strings.TrimSpace(str(data, "firstName") + " " + str(data, "lastName"))
	}
	out := M{"name": name, "email": email}
	switch d.ID {
	case "google_drive":
		out["picture"] = data["picture"]
	case "netdocuments":
		out["repository"] = object(data["repository"])["name"]
	case "clio", "imanage":
		out["user_id"] = data["id"]
	case "filevine":
		out["org_id"] = creds["org_id"]
	}
	return out, nil
}

func (s *Server) revokeConnector(ctx context.Context, d connectorDefinition, v map[string]string, creds M) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var target string
	h := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
	params := url.Values{}
	switch d.ID {
	case "google_drive":
		target = "https://oauth2.googleapis.com/revoke"
		params.Set("token", fallback(str(creds, "refresh_token"), str(creds, "access_token")))
	case "box":
		target = "https://api.box.com/oauth2/revoke"
		params.Set("client_id", v[d.IDField])
		params.Set("client_secret", v[d.SecretField])
		params.Set("token", fallback(str(creds, "refresh_token"), str(creds, "access_token")))
	case "dropbox":
		if str(creds, "refresh_token") != "" {
			if fresh, err := s.exchangeConnectorToken(ctx, d, v, "refresh_token", str(creds, "refresh_token"), creds); err == nil {
				creds = fresh
			}
		}
		target = "https://api.dropboxapi.com/2/auth/token/revoke"
		h = connectorHeaders(d, creds)
	default:
		return
	}
	if str(creds, "access_token") == "" && str(creds, "refresh_token") == "" {
		return
	}
	if _, err := s.connectorJSON(ctx, "POST", target, h, []byte(params.Encode()), false); err != nil {
		s.log.Warn("connector revocation failed", "provider", d.ID, "reason", connectorSafeError(err))
	}
}
