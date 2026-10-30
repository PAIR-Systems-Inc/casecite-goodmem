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
	GoogleSSOClientID, GoogleDefaultRole                               string
	GoogleSSODomains                                                   []string
	GoogleAutoProvision                                                bool
	GoodMemSecurity                                                    *GoodMemSecurity
	Address, DatabaseURL, Secret, BootstrapToken, PublicURL            string
	GoodMemURL, GoodMemKey, GoodMemSpace, RerankerID                   string
	LLMURL, LLMKey, LLMModel, CourtListenerToken                       string
	CourtListenerURL                                                   string
	WikipediaURL                                                       string
	LLMUtilityModel                                                    string
	AnthropicKey, GoogleKey                                            string
	EmbeddingURL, EmbeddingKey, EmbeddingModel, VoyageKey, CohereKey   string
	ProviderPolicyEnabled                                              bool
	ApprovedAIProviders                                                []string
	LLMContextWindow                                                   int
	Debug                                                              bool
	SessionTTL, IdleTTL                                                time.Duration
	JobTimeout                                                         time.Duration
	ReviewConcurrency                                                  int
	WorkflowMaxSteps, WorkflowMaxDocuments, WorkflowMaxCitations       int
	WorkflowMaxOutputBytes                                             int
	WorkflowNotifications                                              bool
	ResearchMaxSubquestions, ResearchMaxToolCalls, ResearchMaxPassages int
	ResearchConcurrency, ResearchFollowupDepth                         int
	ResearchOpinionWindows                                             int
	MaxUpload                                                          int64
	RequireMFA                                                         bool
	AuditFailClosed                                                    bool
	MicrosoftClientID, MicrosoftTenantID, MicrosoftRedirectURI         string
	AzureAutoProvision                                                 bool
	AzureDefaultRole                                                   string
	SMTPHost, SMTPPort, SMTPUser, SMTPPassword, SMTPFrom               string
	SMTPTLS                                                            bool
	AddinOrigins                                                       []string
	ConnectorSettings                                                  map[string]string
	ExperimentalConnectors, FilevineEnabled                            bool
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
		LLMUtilityModel: env("OPENAI_UTILITY_MODEL", "gpt-5.4-mini"),
		AnthropicKey:    os.Getenv("ANTHROPIC_API_KEY"), GoogleKey: env("GOOGLE_API_KEY", os.Getenv("GEMINI_API_KEY")),
		EmbeddingURL: strings.TrimSpace(os.Getenv("EMBEDDING_BASE_URL")), EmbeddingKey: os.Getenv("EMBEDDING_API_KEY"), EmbeddingModel: env("EMBEDDING_MODEL", "text-embedding-3-small"),
		VoyageKey: os.Getenv("VOYAGE_API_KEY"), CohereKey: os.Getenv("COHERE_API_KEY"),
		ProviderPolicyEnabled: env("HIPAA_ENFORCEMENT_ENABLED", "false") == "true",
		CourtListenerToken:    os.Getenv("COURTLISTENER_API_TOKEN"), Debug: env("DEBUG", "false") == "true",
		CourtListenerURL: strings.TrimRight(env("COURTLISTENER_BASE_URL", "https://www.courtlistener.com/api/rest/v4"), "/"),
		WikipediaURL:     env("WIKIPEDIA_API_URL", "https://en.wikipedia.org/w/api.php"),
		SessionTTL:       8 * time.Hour, IdleTTL: 2 * time.Hour, MaxUpload: 50 << 20,
		JobTimeout:        time.Hour,
		ReviewConcurrency: 4,
		WorkflowMaxSteps:  12, WorkflowMaxDocuments: 25, WorkflowMaxCitations: 10, WorkflowMaxOutputBytes: 524288,
		WorkflowNotifications:   env("WORKFLOW_NOTIFICATIONS", "false") == "true",
		ResearchMaxSubquestions: 8, ResearchMaxToolCalls: 24, ResearchMaxPassages: 60, ResearchConcurrency: 3, ResearchFollowupDepth: 1,
		ResearchOpinionWindows: 6,
		RequireMFA:             env("REQUIRE_MFA", "false") == "true",
		AuditFailClosed:        env("AUDIT_FAIL_CLOSED", "false") == "true",
		MicrosoftClientID:      os.Getenv("MICROSOFT_CLIENT_ID"), MicrosoftTenantID: os.Getenv("MICROSOFT_TENANT_ID"), MicrosoftRedirectURI: os.Getenv("MICROSOFT_REDIRECT_URI"),
		AzureAutoProvision: env("AZURE_SSO_AUTO_PROVISION", "true") == "true", AzureDefaultRole: env("AZURE_SSO_DEFAULT_ROLE", "viewer"),
		SMTPHost: os.Getenv("SMTP_HOST"), SMTPPort: env("SMTP_PORT", "587"), SMTPUser: os.Getenv("SMTP_USER"), SMTPPassword: os.Getenv("SMTP_PASSWORD"), SMTPFrom: os.Getenv("SMTP_FROM"), SMTPTLS: env("SMTP_TLS", "false") == "true",
	}
	c.GoogleSSOClientID = strings.TrimSpace(os.Getenv("GOOGLE_SSO_CLIENT_ID"))
	c.GoogleAutoProvision = env("GOOGLE_SSO_AUTO_PROVISION", "false") == "true"
	c.GoogleDefaultRole = env("GOOGLE_SSO_DEFAULT_ROLE", "viewer")
	for _, domain := range strings.Split(os.Getenv("GOOGLE_SSO_ALLOWED_DOMAINS"), ",") {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" {
			continue
		}
		if !strings.Contains(domain, ".") || strings.ContainsAny(domain, " /:@?#*\\") {
			return c, errors.New("GOOGLE_SSO_ALLOWED_DOMAINS must contain exact Google Workspace domains")
		}
		c.GoogleSSODomains = append(c.GoogleSSODomains, domain)
	}
	if c.GoogleSSOClientID != "" && len(c.GoogleSSODomains) == 0 {
		return c, errors.New("Google SSO requires GOOGLE_SSO_ALLOWED_DOMAINS")
	}
	c.ConnectorSettings = connectorEnvironment(c.PublicURL)
	c.ExperimentalConnectors = env("EXPERIMENTAL_CONNECTORS", "false") == "true"
	c.FilevineEnabled = env("FILEVINE_ENABLED", "false") == "true"
	approved := strings.TrimSpace(os.Getenv("APPROVED_AI_PROVIDERS"))
	if strings.HasPrefix(approved, "[") {
		if err := json.Unmarshal([]byte(approved), &c.ApprovedAIProviders); err != nil {
			return c, errors.New("APPROVED_AI_PROVIDERS must be a JSON array of strings or a comma-separated list")
		}
	} else {
		c.ApprovedAIProviders = strings.Split(approved, ",")
	}
	if file := os.Getenv("GOODMEM_CONFIG_FILE"); file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return c, err
		}
		var v GoodMemSecurity
		if err := json.Unmarshal(b, &v); err != nil {
			return c, err
		}
		if v.Version != 1 || v.ControllerID == "" || len(v.Subjects) == 0 {
			return c, errors.New("GOODMEM_CONFIG_FILE must contain scoped controller and user identities from setup-goodmem; legacy root credentials are not accepted")
		}
		if c.GoodMemKey != "" && c.GoodMemKey != v.APIKey || c.GoodMemSpace != "" && c.GoodMemSpace != v.SpaceID {
			return c, errors.New("GoodMem environment credentials must not override the scoped configuration")
		}
		if c.RerankerID != "" && c.RerankerID != v.RerankerID {
			return c, errors.New("GOODMEM_RERANKER_ID must match the scoped setup configuration; rerun setup-goodmem before changing it")
		}
		c.RerankerID = v.RerankerID
		c.GoodMemKey, c.GoodMemSpace, c.GoodMemSecurity = v.APIKey, v.SpaceID, &v
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
	if c.GoodMemSecurity == nil || c.GoodMemKey == "" || c.GoodMemSpace == "" {
		return c, errors.New("GOODMEM_CONFIG_FILE from setup-goodmem is required (a shared API key is not a user security boundary)")
	}
	for _, raw := range []string{c.PublicURL, c.GoodMemURL, c.LLMURL, c.CourtListenerURL, c.WikipediaURL} {
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
	if raw := os.Getenv("REVIEW_TABLE_CONCURRENCY"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 32 {
			return c, errors.New("REVIEW_TABLE_CONCURRENCY must be between 1 and 32")
		}
		c.ReviewConcurrency = n
	}
	for _, field := range []struct {
		key       string
		target    *int
		low, high int
	}{
		{"WORKFLOW_MAX_STEPS", &c.WorkflowMaxSteps, 1, 50},
		{"WORKFLOW_MAX_DOCUMENTS_PER_STEP", &c.WorkflowMaxDocuments, 1, 1000},
		{"WORKFLOW_MAX_CITATIONS_PER_STEP", &c.WorkflowMaxCitations, 1, 1000},
		{"WORKFLOW_MAX_OUTPUT_BYTES", &c.WorkflowMaxOutputBytes, 4096, 16 << 20},
		{"DEEP_RESEARCH_MAX_SUBQUESTIONS", &c.ResearchMaxSubquestions, 1, 50},
		{"DEEP_RESEARCH_MAX_TOOL_CALLS", &c.ResearchMaxToolCalls, 1, 1000},
		{"DEEP_RESEARCH_MAX_PASSAGES", &c.ResearchMaxPassages, 1, 1000},
		{"DEEP_RESEARCH_CONCURRENCY", &c.ResearchConcurrency, 1, 32},
		{"DEEP_RESEARCH_FOLLOWUP_DEPTH", &c.ResearchFollowupDepth, 0, 1},
		{"RESEARCH_MAX_WINDOWS_PER_OPINION", &c.ResearchOpinionWindows, 1, 32},
	} {
		if raw := os.Getenv(field.key); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < field.low || n > field.high {
				return c, errors.New(field.key + " is outside its supported range")
			}
			*field.target = n
		}
	}
	return c, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
