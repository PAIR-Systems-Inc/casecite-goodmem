package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"slices"
	"strings"
)

type connectorDefinition struct {
	ID, Label, Settings, IDField, SecretField, RedirectField string
	Experimental, AdminOnly, Flat                            bool
}

var connectorDefinitions = []connectorDefinition{
	{ID: "google_drive", Label: "Google Drive", Settings: "google", IDField: "google_client_id", SecretField: "google_client_secret", RedirectField: "google_redirect_uri"},
	{ID: "onedrive", Label: "OneDrive & SharePoint", Settings: "microsoft", IDField: "microsoft_client_id", SecretField: "microsoft_client_secret", RedirectField: "microsoft_connector_redirect_uri"},
	{ID: "box", Label: "Box", Settings: "box", IDField: "box_client_id", SecretField: "box_client_secret", RedirectField: "box_redirect_uri"},
	{ID: "dropbox", Label: "Dropbox", Settings: "dropbox", IDField: "dropbox_app_key", SecretField: "dropbox_app_secret", RedirectField: "dropbox_redirect_uri"},
	{ID: "filevine", Label: "Filevine", Settings: "filevine", IDField: "filevine_api_key", SecretField: "filevine_api_secret", AdminOnly: true},
	{ID: "clio", Label: "Clio", Settings: "clio", IDField: "clio_client_id", SecretField: "clio_client_secret", RedirectField: "clio_redirect_uri", Flat: true},
	{ID: "netdocuments", Label: "NetDocuments", Settings: "netdocuments", IDField: "netdocuments_client_id", SecretField: "netdocuments_client_secret", RedirectField: "netdocuments_redirect_uri", Experimental: true},
	{ID: "imanage", Label: "iManage", Settings: "imanage", IDField: "imanage_client_id", SecretField: "imanage_client_secret", RedirectField: "imanage_redirect_uri", Experimental: true},
}
var connectorAdminFields = map[string][]string{
	"google":    {"google_client_id", "google_client_secret", "google_api_key", "google_app_id"},
	"microsoft": {"microsoft_client_id", "microsoft_client_secret", "microsoft_tenant_id"},
	"box":       {"box_client_id", "box_client_secret"},
	"dropbox":   {"dropbox_app_key", "dropbox_app_secret"},
}

func connectorEnvironment(publicURL string) map[string]string {
	values := map[string]string{}
	for _, d := range connectorDefinitions {
		values[d.IDField], values[d.SecretField] = strings.TrimSpace(os.Getenv(strings.ToUpper(d.IDField))), strings.TrimSpace(os.Getenv(strings.ToUpper(d.SecretField)))
		if d.RedirectField != "" {
			// SSO and Graph use distinct callbacks, even when sharing an app ID.
			values[d.RedirectField] = env(strings.ToUpper(d.RedirectField), publicURL+"/api/v1/connectors/"+d.ID+"/callback")
		}
	}
	for _, fields := range connectorAdminFields {
		for _, field := range fields {
			values[field] = strings.TrimSpace(os.Getenv(strings.ToUpper(field)))
		}
	}
	// Gemini's server key must never be returned by /pickers/config. The
	// Picker developer key is deliberately browser-visible and origin-limited.
	values["google_api_key"] = strings.TrimSpace(os.Getenv("GOOGLE_PICKER_API_KEY"))
	for key, fallback := range map[string]string{"microsoft_tenant_id": "common", "clio_base_url": "https://app.clio.com", "filevine_base_url": "https://api.filevine.io", "netdocuments_vault_host": "https://vault.netvoyage.com", "netdocuments_api_host": "https://api.vault.netvoyage.com", "imanage_base_url": "https://cloudimanage.com"} {
		values[key] = strings.TrimRight(env(strings.ToUpper(key), fallback), "/")
	}
	return values
}

func (s *Server) connectorDefinition(id string) (connectorDefinition, error) {
	for _, d := range connectorDefinitions {
		if d.ID != id {
			continue
		}
		if d.Experimental && !s.cfg.ExperimentalConnectors {
			return d, bad("This connector is experimental. Set EXPERIMENTAL_CONNECTORS=true to enable it.")
		}
		return d, nil
	}
	return connectorDefinition{}, bad("Unknown connector")
}
func (s *Server) connectorStoredConfig(ctx context.Context, provider string) (map[string]string, error) {
	return s.connectorStoredConfigOn(ctx, s.db, provider)
}
func (s *Server) connectorStoredConfigOn(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, provider string) (map[string]string, error) {
	var value []byte
	var encrypted string
	err := db.QueryRowContext(ctx, `SELECT value FROM cc_kv WHERE key=$1 AND (expires_at IS NULL OR expires_at>now())`, "instance:connector:"+provider).Scan(&value)
	if notFound(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(value, &encrypted); err != nil {
		return nil, err
	}
	raw, err := s.crypt(encrypted, true)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	err = json.Unmarshal([]byte(raw), &values)
	return values, err
}
func (s *Server) connectorConfig(ctx context.Context, provider string) (map[string]string, error) {
	values := map[string]string{}
	for k, v := range s.cfg.ConnectorSettings {
		values[k] = v
	}
	if connectorAdminFields[provider] != nil {
		stored, err := s.connectorStoredConfig(ctx, provider)
		if err != nil {
			return nil, err
		}
		for k, v := range stored {
			if v != "" {
				values[k] = v
			}
		}
	}
	return values, nil
}
func (s *Server) connectorConfigured(d connectorDefinition, values map[string]string) bool {
	return values[d.IDField] != "" && values[d.SecretField] != "" && (!d.AdminOnly || s.cfg.FilevineEnabled)
}
func connectorAdminStatus(provider string, effective, stored, defaults map[string]string) M {
	fields := connectorAdminFields[provider]
	source := "none"
	if defaults[fields[0]] != "" {
		source = "env"
	}
	if len(stored) > 0 {
		source = "instance"
	}
	masked := M{}
	for _, field := range fields {
		value := effective[field]
		if value == "" {
			masked[field] = nil
		} else if strings.Contains(field, "secret") || field == "google_api_key" {
			masked[field] = strings.Repeat("•", 8)
		} else {
			masked[field] = maskedIntegrationKey(value)
		}
	}
	label := ""
	for _, d := range connectorDefinitions {
		if d.Settings == provider {
			label = d.Label
		}
	}
	return M{"provider": provider, "label": label, "configured": effective[fields[0]] != "", "source": source, "fields": fields, "masked": masked}
}
func (s *Server) connectorIntegrations(q request) (any, error) {
	if err := permit(q, "admin.settings"); err != nil {
		return nil, err
	}
	ctx, provider := q.r.Context(), q.r.PathValue("provider")
	if provider == "" {
		out := []M{}
		for _, p := range []string{"google", "microsoft", "box", "dropbox"} {
			values, err := s.connectorConfig(ctx, p)
			if err != nil {
				return nil, err
			}
			stored, err := s.connectorStoredConfig(ctx, p)
			if err != nil {
				return nil, err
			}
			out = append(out, connectorAdminStatus(p, values, stored, s.cfg.ConnectorSettings))
		}
		return M{"providers": out}, nil
	}
	fields, ok := connectorAdminFields[provider]
	if !ok {
		return nil, missing()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	key := "instance:connector:" + provider
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,4))`, key); err != nil {
		return nil, err
	}
	if q.r.Method == "DELETE" {
		_, err = tx.ExecContext(ctx, `DELETE FROM cc_kv WHERE key=$1`, key)
	} else {
		in, e := body(q)
		if e != nil {
			return nil, e
		}
		values, ok := in["values"].(map[string]any)
		if !ok || len(values) > 20 {
			return nil, fail(422, "values must contain at most 20 credential fields")
		}
		stored, e := s.connectorStoredConfigOn(ctx, tx, provider)
		if e != nil {
			return nil, e
		}
		provided := 0
		for field, raw := range values {
			value, ok := raw.(string)
			if !ok || len([]rune(field)) > 100 || len([]rune(value)) > 2000 {
				return nil, fail(422, "Invalid credential value")
			}
			if !slices.Contains(fields, field) {
				continue
			}
			provided++
			value = strings.TrimSpace(value)
			if value == "" {
				delete(stored, field)
			} else {
				stored[field] = value
			}
		}
		if provided == 0 {
			return nil, bad("No credential values provided")
		}
		if len(stored) == 0 {
			_, err = tx.ExecContext(ctx, `DELETE FROM cc_kv WHERE key=$1`, key)
		} else {
			encrypted, e := s.crypt(string(jsonBytes(stored)), false)
			if e != nil {
				return nil, e
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO cc_kv(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, key, jsonBytes(encrypted))
		}
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(ctx, q.user.ID, "settings.change", "integration", "connector_"+provider, M{"action": q.r.Method})
	effective, err := s.connectorConfig(ctx, provider)
	if err != nil {
		return nil, err
	}
	stored, err := s.connectorStoredConfig(ctx, provider)
	if err != nil {
		return nil, err
	}
	return connectorAdminStatus(provider, effective, stored, s.cfg.ConnectorSettings), nil
}

func connectorSameOrigin(a, b string) bool {
	u, e := url.Parse(a)
	v, f := url.Parse(b)
	return e == nil && f == nil && u.User == nil && v.User == nil && u.Scheme == v.Scheme && strings.EqualFold(u.Host, v.Host)
}
