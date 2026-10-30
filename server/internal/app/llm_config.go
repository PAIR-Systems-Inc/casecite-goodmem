package app

import (
	"context"
	"encoding/json"
	"net/netip"
	"net/url"
	"strings"
	"unicode/utf8"
)

const localLLMKey = "instance:local_llm_config"

type llmRuntime struct {
	BaseURL      string `json:"base_url"`
	ChatModel    string `json:"chat_model"`
	UtilityModel string `json:"utility_model"`
	Configured   bool   `json:"-"`
}

func validateLLMEndpoint(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", bad("The model endpoint must be an absolute HTTP(S) URL without credentials, query parameters or a fragment.")
	}
	return raw, nil
}

func providerForEndpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "unknown"
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || host == "api.openai.com" {
		return "openai"
	}
	if host == "localhost" || host == "host.docker.internal" {
		return "self_hosted"
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return "self_hosted"
		}
		return host
	}
	if !strings.Contains(host, ".") {
		return "self_hosted"
	}
	return host
}

func modelProvider(model string) string {
	model = strings.ToLower(model)
	if strings.Contains(model, "claude") {
		return "anthropic"
	}
	if strings.Contains(model, "gemini") {
		return "google"
	}
	return "openai"
}

func (s *Server) allowAIProvider(provider string) error {
	if !s.cfg.ProviderPolicyEnabled {
		return nil
	}
	for _, entry := range s.cfg.ApprovedAIProviders {
		name := strings.ToLower(strings.TrimSpace(entry))
		if name == provider || (provider == "self_hosted" && (name == "local" || name == "self-hosted" || name == "selfhosted")) {
			return nil
		}
	}
	return fail(403, "This instance restricts AI providers, and '"+provider+"' is not approved. Choose an approved provider or ask your administrator.")
}

func (s *Server) llmRuntime(ctx context.Context) (llmRuntime, error) {
	runtime := llmRuntime{BaseURL: fallback(s.cfg.LLMURL, "https://api.openai.com/v1"), ChatModel: s.cfg.LLMModel, UtilityModel: s.cfg.LLMUtilityModel}
	runtime.Configured = strings.TrimRight(runtime.BaseURL, "/") != "https://api.openai.com/v1"
	var encrypted string
	err := s.kvGet(ctx, localLLMKey, &encrypted)
	if notFound(err) {
		return runtime, nil
	}
	if err != nil {
		return runtime, err
	}
	raw, err := s.crypt(encrypted, true)
	if err != nil {
		return runtime, err
	}
	if err = json.Unmarshal([]byte(raw), &runtime); err != nil {
		return runtime, err
	}
	runtime.Configured = true
	return runtime, nil
}

func llmStatus(runtime llmRuntime) M {
	custom := runtime.Configured
	var endpoint any
	if custom {
		endpoint = runtime.BaseURL
	}
	return M{"configured": custom, "base_url": endpoint, "chat_model": runtime.ChatModel, "utility_model": runtime.UtilityModel}
}

func (s *Server) localLLM(q request) (any, error) {
	if err := permit(q, "admin.settings"); err != nil {
		return nil, err
	}
	status := ""
	if q.r.Method == "POST" {
		in, err := body(q)
		if err != nil {
			return nil, err
		}
		endpoint, err := validateLLMEndpoint(str(in, "base_url"))
		if err != nil {
			return nil, err
		}
		model := strings.TrimSpace(str(in, "chat_model"))
		utility := strings.TrimSpace(str(in, "utility_model"))
		if len(endpoint) > 400 || model == "" || utf8.RuneCountInString(model) > 120 || utf8.RuneCountInString(utility) > 120 {
			return nil, bad("Provide an endpoint up to 400 characters and model names up to 120 characters.")
		}
		if utility == "" {
			utility = model
		}
		if err = s.allowAIProvider(providerForEndpoint(endpoint)); err != nil {
			return nil, err
		}
		encrypted, err := s.crypt(string(jsonBytes(llmRuntime{BaseURL: endpoint, ChatModel: model, UtilityModel: utility})), false)
		if err != nil {
			return nil, err
		}
		if err = s.kvSet(q.r.Context(), localLLMKey, encrypted, 0); err != nil {
			return nil, err
		}
		s.audit(q.r.Context(), q.user.ID, "settings.update", "integration", "local_llm", M{"action": "local_llm_set", "provider": providerForEndpoint(endpoint)})
		status = "saved"
	} else if q.r.Method == "DELETE" {
		if _, err := s.db.ExecContext(q.r.Context(), `DELETE FROM cc_kv WHERE key=$1`, localLLMKey); err != nil {
			return nil, err
		}
		s.audit(q.r.Context(), q.user.ID, "settings.update", "integration", "local_llm", M{"action": "local_llm_cleared"})
		status = "cleared"
	}
	runtime, err := s.llmRuntime(q.r.Context())
	if err != nil {
		return nil, err
	}
	out := llmStatus(runtime)
	if status != "" {
		out["status"] = status
	}
	return out, nil
}

type llmSelection struct {
	Protocol, Provider, Endpoint, Model, Key string
	MaxTokens                                int
	Temperature                              float64
}

// Utility tasks retain the chosen native provider. Explicit model overrides,
// such as a drafting budget's model, still take precedence.
func (s *Server) selectLLM(ctx context.Context, user string) (llmSelection, error) {
	var selected llmSelection
	runtime, err := s.llmRuntime(ctx)
	if err != nil {
		return selected, err
	}
	settings, err := s.userSettingsWithRuntime(ctx, user, runtime)
	if err != nil {
		return selected, err
	}
	options, _ := ctx.Value(llmOptionsKey{}).(llmOptions)
	model := str(settings, "llm_model")
	if options.Utility && modelProvider(model) == "openai" {
		model = fallback(runtime.UtilityModel, model)
	}
	if options.Model != "" {
		model = options.Model
	}
	if strings.TrimSpace(model) == "" {
		return selected, bad("Choose a language model in Settings.")
	}
	selected = llmSelection{Protocol: "openai", Provider: providerForEndpoint(runtime.BaseURL), Endpoint: runtime.BaseURL, Model: model, MaxTokens: options.MaxTokens, Temperature: 0.1}
	if selected.MaxTokens <= 0 {
		selected.MaxTokens = number(settings, "max_tokens", 4096)
	}
	if v, ok := settings["temperature"].(float64); ok {
		selected.Temperature = v
	}
	if options.Temperature != nil {
		selected.Temperature = *options.Temperature
	}
	family := modelProvider(model)
	if family != "openai" {
		fallbackKey, endpoint := s.cfg.AnthropicKey, "https://api.anthropic.com/v1"
		if family == "google" {
			fallbackKey, endpoint = s.cfg.GoogleKey, "https://generativelanguage.googleapis.com/v1beta"
		}
		key, err := s.providerKey(ctx, user, family, fallbackKey)
		if err != nil {
			return selected, err
		}
		if key != "" {
			selected.Protocol, selected.Provider, selected.Endpoint, selected.Key = family, family, endpoint, key
		} else if selected.Provider == "openai" {
			return selected, bad("Add a " + family + " API key in Settings, or configure a compatible gateway for this model.")
		}
	}
	if selected.Protocol == "openai" {
		selected.Key, err = s.providerKey(ctx, user, "openai", s.cfg.LLMKey)
		if err != nil {
			return selected, err
		}
		if selected.Key == "" && selected.Provider == "openai" {
			return selected, bad("Add an OpenAI API key or configure a custom model endpoint in Settings.")
		}
	}
	selected.Endpoint, err = validateLLMEndpoint(selected.Endpoint)
	if err != nil {
		return selected, err
	}
	return selected, s.allowAIProvider(selected.Provider)
}

func (s *Server) utilityModel(ctx context.Context, user string) (string, error) {
	selected, err := s.selectLLM(withLLMOptions(ctx, llmOptions{Utility: true}), user)
	return selected.Model, err
}

// Estimates must work before an API key has been entered.
func (s *Server) utilityModelName(ctx context.Context, user string) (string, error) {
	runtime, err := s.llmRuntime(ctx)
	if err != nil {
		return "", err
	}
	settings, err := s.userSettingsWithRuntime(ctx, user, runtime)
	if err != nil {
		return "", err
	}
	model := str(settings, "llm_model")
	if modelProvider(model) == "openai" {
		model = fallback(runtime.UtilityModel, model)
	}
	return model, nil
}

func (s *Server) llmRoutes() {
	for _, method := range []string{"GET", "POST", "DELETE"} {
		s.api(method+" /api/v1/admin/integrations/local-llm", false, s.localLLM)
		s.api(method+" /api/v1/admin/integrations/courtlistener", false, s.courtIntegration)
	}
}
