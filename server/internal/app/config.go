package app

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address, DatabaseURL, Secret, BootstrapToken, PublicURL    string
	GoodMemURL, GoodMemKey, GoodMemSpace, RerankerID           string
	LLMURL, LLMKey, LLMModel, CourtListenerToken               string
	LLMUtilityModel                                            string
	LLMContextWindow                                           int
	Debug                                                      bool
	SessionTTL, IdleTTL                                        time.Duration
	JobTimeout                                                 time.Duration
	MaxUpload                                                  int64
	RequireMFA                                                 bool
	AuditFailClosed                                            bool
	MicrosoftClientID, MicrosoftTenantID, MicrosoftRedirectURI string
	AzureAutoProvision                                         bool
	AzureDefaultRole                                           string
	SMTPHost, SMTPPort, SMTPUser, SMTPPassword, SMTPFrom       string
	SMTPTLS                                                    bool
	AddinOrigins                                               []string
}

func LoadConfig() (Config, error) {
	c := Config{
		Address: env("LISTEN_ADDR", ":8000"), DatabaseURL: os.Getenv("DATABASE_URL"),
		Secret: os.Getenv("SECRET_KEY"), BootstrapToken: os.Getenv("REGISTRATION_BOOTSTRAP_TOKEN"),
		PublicURL:  strings.TrimRight(env("PUBLIC_URL", "http://localhost:8000"), "/"),
		GoodMemURL: strings.TrimRight(env("GOODMEM_BASE_URL", "http://goodmem:8080"), "/"),
		GoodMemKey: os.Getenv("GOODMEM_API_KEY"), GoodMemSpace: os.Getenv("GOODMEM_SPACE_ID"),
		RerankerID: os.Getenv("GOODMEM_RERANKER_ID"),
		LLMURL:     strings.TrimRight(env("OPENAI_BASE_URL", "https://api.openai.com/v1"), "/"),
		LLMKey:     os.Getenv("OPENAI_API_KEY"), LLMModel: env("LLM_MODEL", "gpt-5.5"),
		LLMUtilityModel:    env("OPENAI_UTILITY_MODEL", "gpt-5.4-mini"),
		CourtListenerToken: os.Getenv("COURTLISTENER_API_TOKEN"), Debug: env("DEBUG", "false") == "true",
		SessionTTL: 8 * time.Hour, IdleTTL: 2 * time.Hour, MaxUpload: 50 << 20,
		JobTimeout:        time.Hour,
		RequireMFA:        env("REQUIRE_MFA", "false") == "true",
		AuditFailClosed:   env("AUDIT_FAIL_CLOSED", "false") == "true",
		MicrosoftClientID: os.Getenv("MICROSOFT_CLIENT_ID"), MicrosoftTenantID: os.Getenv("MICROSOFT_TENANT_ID"), MicrosoftRedirectURI: os.Getenv("MICROSOFT_REDIRECT_URI"),
		AzureAutoProvision: env("AZURE_SSO_AUTO_PROVISION", "true") == "true", AzureDefaultRole: env("AZURE_SSO_DEFAULT_ROLE", "viewer"),
		SMTPHost: os.Getenv("SMTP_HOST"), SMTPPort: env("SMTP_PORT", "587"), SMTPUser: os.Getenv("SMTP_USER"), SMTPPassword: os.Getenv("SMTP_PASSWORD"), SMTPFrom: os.Getenv("SMTP_FROM"), SMTPTLS: env("SMTP_TLS", "false") == "true",
	}
	if file := os.Getenv("GOODMEM_CONFIG_FILE"); file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return c, err
		}
		var v struct {
			APIKey  string `json:"api_key"`
			SpaceID string `json:"space_id"`
			BaseURL string `json:"base_url"`
		}
		if err := json.Unmarshal(b, &v); err != nil {
			return c, err
		}
		if c.GoodMemKey == "" {
			c.GoodMemKey = v.APIKey
		}
		if c.GoodMemSpace == "" {
			c.GoodMemSpace = v.SpaceID
		}
		if os.Getenv("GOODMEM_BASE_URL") == "" && v.BaseURL != "" {
			c.GoodMemURL = v.BaseURL
		}
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	c.DatabaseURL = strings.Replace(c.DatabaseURL, "postgresql+asyncpg://", "postgresql://", 1)
	if len(c.Secret) < 32 {
		return c, errors.New("SECRET_KEY must contain at least 32 characters")
	}
	if c.GoodMemKey == "" || c.GoodMemSpace == "" {
		return c, errors.New("GOODMEM_API_KEY and GOODMEM_SPACE_ID (or GOODMEM_CONFIG_FILE) are required")
	}
	for _, raw := range []string{c.PublicURL, c.GoodMemURL, c.LLMURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return c, errors.New("service URLs must be absolute HTTP(S) URLs")
		}
	}
	if !c.Debug && !strings.HasPrefix(c.PublicURL, "https://") {
		return c, errors.New("PUBLIC_URL must use HTTPS in production; set DEBUG=true for a local evaluation")
	}
	if c.MicrosoftClientID != "" && (c.MicrosoftTenantID == "" || c.MicrosoftTenantID == "common" || c.MicrosoftTenantID == "organizations" || strings.ContainsAny(c.MicrosoftTenantID, "/?#")) {
		return c, errors.New("Microsoft SSO requires the organization's explicit MICROSOFT_TENANT_ID")
	}
	for _, raw := range strings.Split(os.Getenv("ADDIN_ALLOWED_ORIGINS"), ",") {
		origin := strings.TrimRight(strings.TrimSpace(raw), "/")
		if origin == "" {
			continue
		}
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(c.Debug && u.Scheme == "http")) {
			return c, errors.New("ADDIN_ALLOWED_ORIGINS must contain comma-separated HTTPS origins (HTTP is allowed only in DEBUG mode)")
		}
		c.AddinOrigins = append(c.AddinOrigins, origin)
	}
	if n, err := strconv.ParseInt(os.Getenv("MAX_UPLOAD_MB"), 10, 64); err == nil && n > 0 && n <= 250 {
		c.MaxUpload = n << 20
	}
	if n, err := strconv.Atoi(os.Getenv("LLM_CONTEXT_WINDOW")); err == nil && n > 0 {
		c.LLMContextWindow = n
	}
	if raw := os.Getenv("JOB_TIMEOUT_SECONDS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 86400 {
			return c, errors.New("JOB_TIMEOUT_SECONDS must be between 1 and 86400")
		}
		c.JobTimeout = time.Duration(n) * time.Second
	}
	return c, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
