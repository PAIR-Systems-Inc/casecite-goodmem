package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type modelTransport func(*http.Request) (*http.Response, error)

func (f modelTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProviderPythonOracle(t *testing.T) {
	b, err := os.ReadFile("testdata/provider_behavior.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Baseline string
		Fixtures []M
	}
	if err = json.Unmarshal(b, &oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.Baseline != "6605cb3474cf0c2894080af216d5fed2a81fc997" || len(oracle.Fixtures) < 120 {
		t.Fatal("Incomplete upstream oracle")
	}
	for _, row := range oracle.Fixtures {
		switch str(row, "kind") {
		case "endpoint":
			if got := providerForEndpoint(str(row, "input")); got != str(row, "expected") {
				t.Errorf("endpoint %s: %s", str(row, "input"), got)
			}
		case "model":
			if got := modelProvider(str(row, "input")); got != str(row, "expected") {
				t.Errorf("model %s: %s", str(row, "input"), got)
			}
		case "policy":
			s := Server{cfg: Config{ProviderPolicyEnabled: row["enabled"] == true, ApprovedAIProviders: stringsOf(row["approved"])}}
			if got := s.allowAIProvider(str(row, "input")) == nil; got != (row["expected"] == true) {
				t.Errorf("policy %v", row)
			}
		case "adapt":
			payload := object(row["input"])
			changed := adaptModelParameters(payload, jsonBytes(M{"error": M{"message": row["message"]}}))
			if changed != (row["changed"] == true) || string(jsonBytes(payload)) != string(jsonBytes(row["expected"])) {
				t.Errorf("adapt: %v => %v", row, payload)
			}
		}
	}
}

func TestProviderCompleteResponsesOnly(t *testing.T) {
	for _, tc := range []struct {
		protocol string
		data     M
		want     string
	}{
		{"openai", M{"choices": []M{{"message": M{"content": "answer"}, "finish_reason": "stop"}}}, "answer"},
		{"anthropic", M{"content": []M{{"type": "thinking", "thinking": "private reasoning"}, {"type": "text", "text": "first "}, {"type": "text", "text": "second"}}, "stop_reason": "end_turn"}, "first second"},
		{"google", M{"candidates": []M{{"content": M{"parts": []M{{"text": "private reasoning", "thought": true}, {"text": "answer"}}}, "finishReason": "STOP"}}}, "answer"},
		{"openai", M{"choices": []M{{"message": M{"content": "partial"}, "finish_reason": "length"}}}, ""},
		{"openai", M{"choices": []M{{"message": M{"content": "partial", "refusal": "No"}, "finish_reason": "stop"}}}, ""},
		{"openai", M{"choices": []M{{"message": M{"content": "partial"}, "finish_reason": "content_filter"}}}, ""},
		{"anthropic", M{"content": []M{{"type": "text", "text": "partial"}}, "stop_reason": "max_tokens"}, ""},
		{"anthropic", M{"content": []M{{"type": "text", "text": "partial"}}, "stop_reason": "refusal"}, ""},
		{"anthropic", M{"content": []M{{"type": "text", "text": "partial"}}, "stop_reason": "end_turn", "stop_details": M{"type": "refusal"}}, ""},
		{"anthropic", M{"content": []M{{"type": "tool_use", "name": "shell"}}, "stop_reason": "tool_use"}, ""},
		{"google", M{"candidates": []M{{"content": M{"parts": []M{{"text": "partial"}}}, "finishReason": "MAX_TOKENS"}}}, ""},
		{"google", M{"candidates": []M{{"content": M{"parts": []M{{"text": "partial"}}}, "finishReason": "SAFETY"}}}, ""},
		{"google", M{"candidates": []M{{"content": M{"parts": []M{{"text": "partial"}, {"functionCall": M{"name": "shell"}}}}, "finishReason": "STOP"}}}, ""},
		{"google", M{"promptFeedback": M{"blockReason": "SAFETY"}}, ""},
		{"anthropic", M{}, ""},
		{"openai", M{}, ""},
	} {
		answer, _, err := generationResponse(tc.protocol, tc.data)
		if answer != tc.want || (err != nil) != (tc.want == "") {
			t.Errorf("%s %v => %q %v", tc.protocol, tc.data, answer, err)
		}
	}
}

func TestProviderTransportNegotiationRetriesAndBoundaries(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		var in M
		json.NewDecoder(r.Body).Decode(&in)
		switch call {
		case 1:
			if number(in, "max_tokens", 0) != 321 {
				t.Error(in)
			}
			writeJSON(w, 400, M{"error": M{"message": "Unsupported parameter: max_tokens", "param": "max_tokens"}})
		case 2:
			if number(in, "max_completion_tokens", 0) != 321 {
				t.Error(in)
			}
			writeJSON(w, 400, M{"error": M{"message": "Unsupported value: temperature", "param": "temperature"}})
		case 3:
			if in["temperature"] != nil {
				t.Error(in)
			}
			w.Header().Set("Retry-After", "0")
			writeJSON(w, 429, M{"error": M{"message": "Too many requests"}})
		default:
			writeJSON(w, 200, M{"choices": []M{{"message": M{"content": "complete"}, "finish_reason": "stop"}}})
		}
	}))
	defer provider.Close()
	s := Server{client: provider.Client()}
	data, err := s.modelRequest(context.Background(), "self_hosted", provider.URL, http.Header{}, M{"max_tokens": 321, "temperature": 0.1}, true, 4<<20)
	if err != nil || str(maps(data["choices"])[0], "finish_reason") != "stop" || calls.Load() != 4 {
		t.Fatal(data, err, calls.Load())
	}

	for _, tc := range []struct {
		status    int
		body      string
		wantCalls int
	}{
		{401, `{"error":{"message":"echo-private-key-and-document"}}`, 1},
		{500, `{"error":{"message":"temporary failure"}}`, 3},
		{200, `{"answer":"first"}{"answer":"trailing"}`, 1},
		{200, strings.Repeat("x", 129), 1},
	} {
		calls.Store(0)
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(tc.status)
			io.WriteString(w, tc.body)
		}))
		_, err = s.modelRequest(context.Background(), "self_hosted", upstream.URL, http.Header{}, M{}, false, 128)
		upstream.Close()
		if err == nil || strings.Contains(err.Error(), "echo-private") || calls.Load() != int32(tc.wantCalls) {
			t.Fatal(tc, err, calls.Load())
		}
	}
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	if _, err = s.modelRequest(context.Background(), "self_hosted", redirect.URL, http.Header{"Authorization": []string{"Bearer private"}}, M{}, false, 128); err == nil || called {
		t.Fatal("followed provider redirect", err)
	}
	s.cfg.ProviderPolicyEnabled = true
	s.cfg.ApprovedAIProviders = []string{"anthropic"}
	if _, err = s.modelRequest(context.Background(), "self_hosted", target.URL, http.Header{}, M{}, false, 128); err == nil || called {
		t.Fatal("provider policy allowed outbound request")
	}
}

func TestProviderRetryCancellation(t *testing.T) {
	entered := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
		close(entered)
	}))
	defer provider.Close()
	s := Server{client: provider.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.modelRequest(ctx, "self_hosted", provider.URL, http.Header{}, M{}, false, 128)
		done <- err
	}()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled retry remained blocked")
	}
}

func TestModelSelectionNativeProtocolsAndUtilityCalls(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	user := str(f.call("GET", "/auth/me", access, nil, 200), "id")
	f.s.cfg.LLMURL = "https://api.openai.com/v1"
	f.s.cfg.LLMKey = ""
	f.s.cfg.LLMUtilityModel = "gpt-utility"
	for _, family := range []string{"anthropic", "google"} {
		model := "claude-test"
		if family == "google" {
			model = "gemini-test"
		}
		f.call("PUT", "/settings/rag", access, M{"llm_model": model, "max_tokens": 789, "temperature": 0.2}, 200)
		f.call("POST", "/user/keys/save", access, M{"key_type": family, "api_key": "personal-" + family + "-key"}, 200)
		called := 0
		f.s.client.Transport = modelTransport(func(r *http.Request) (*http.Response, error) {
			called++
			var in M
			json.NewDecoder(r.Body).Decode(&in)
			var response M
			if family == "anthropic" {
				if r.URL.Host != "api.anthropic.com" || r.URL.Path != "/v1/messages" || r.Header.Get("Authorization") != "Bearer personal-anthropic-key" || r.Header.Get("anthropic-version") != "2026-10-21" || number(in, "max_tokens", 0) != 789 || in["temperature"] != nil {
					t.Error("wrong Claude request", r.URL, in)
				}
				if str(maps(in["system"])[0], "text") == "" || str(maps(in["messages"])[0], "content") != "test evidence" {
					t.Error("lost prompt", in)
				}
				response = M{"content": []M{{"type": "text", "text": `{"answer":"native"}`}}, "stop_reason": "end_turn", "usage": M{"input_tokens": 4, "output_tokens": 3}}
			} else {
				if r.URL.Host != "generativelanguage.googleapis.com" || r.URL.Path != "/v1beta/models/gemini-test:generateContent" || r.URL.RawQuery != "" || r.Header.Get("x-goog-api-key") != "personal-google-key" || number(object(in["generationConfig"]), "maxOutputTokens", 0) != 789 {
					t.Error("wrong Gemini request", r.URL, in)
				}
				if str(object(in["generationConfig"]), "responseMimeType") != "application/json" {
					t.Error(in)
				}
				response = M{"candidates": []M{{"content": M{"parts": []M{{"text": `{"answer":"native"}`}}}, "finishReason": "STOP"}}, "usageMetadata": M{"totalTokenCount": 7}}
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(jsonBytes(response))))}, nil
		})
		if err := f.s.requireResearchModel(context.Background(), user); err != nil {
			t.Fatal(err)
		}
		result, stats, err := f.s.structuredWithStats(context.Background(), user, "Return JSON", "test evidence")
		if err != nil || str(result, "answer") != "native" || str(stats, "model") != model || str(stats, "provider") != family || called != 1 {
			t.Fatal(result, stats, err, called)
		}
		f.s.cfg.ProviderPolicyEnabled = true
		f.s.cfg.ApprovedAIProviders = []string{"self_hosted"}
		if _, _, err = f.s.llm(context.Background(), user, "system", "private", false); err == nil || called != 1 {
			t.Fatal("native key bypassed endpoint policy", err, called)
		}
		f.s.cfg.ProviderPolicyEnabled = false
	}
	// A missing family key cannot silently route a Claude/Gemini model to OpenAI.
	f.call("DELETE", "/user/keys/google", access, nil, 200)
	if _, err := f.s.selectLLM(context.Background(), user); err == nil {
		t.Fatal("missing Google key accepted")
	}
	// A deliberately configured compatible gateway may serve that model.
	f.s.cfg.LLMURL = "http://local-model:8080/v1"
	selected, err := f.s.selectLLM(context.Background(), user)
	if err != nil || selected.Protocol != "openai" || selected.Provider != "self_hosted" || selected.Key != "" || selected.Model != "gemini-test" {
		t.Fatal(selected, err)
	}
}

func TestModelAdminRuntimePersistsAndRevertsToEnvironment(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	viewer := f.account(access, "provider-viewer@example.test")
	user := str(f.call("GET", "/auth/me", access, nil, 200), "id")
	path := "/admin/integrations/local-llm"
	for _, method := range []string{"GET", "POST", "DELETE"} {
		f.call(method, path, viewer, M{}, 403)
	}
	for _, endpoint := range []string{"https://", "https://user:password@example.test/v1", "https://example.test/v1?key=secret", "file:///tmp/model", "https://example.test/v1#secret"} {
		f.call("POST", path, access, M{"base_url": endpoint, "chat_model": "local"}, 400)
	}
	f.call("POST", path, access, M{"base_url": "http://localhost:1234/v1", "chat_model": "   "}, 400)
	var called atomic.Int32
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		var in M
		json.NewDecoder(r.Body).Decode(&in)
		if r.Header.Get("Authorization") != "" || str(in, "model") != "local-chat" {
			t.Error("keyless local request", in)
		}
		writeJSON(w, 200, M{"choices": []M{{"message": M{"content": "local answer"}, "finish_reason": "stop"}}})
	}))
	defer local.Close()
	f.s.cfg.LLMKey = ""
	saved := f.call("POST", path, access, M{"base_url": local.URL + "/v1/", "chat_model": " local-chat "}, 200)
	if saved["configured"] != true || str(saved, "utility_model") != "local-chat" || str(saved, "status") != "saved" {
		t.Fatal(saved)
	}
	var encrypted string
	if err := f.s.kvGet(context.Background(), localLLMKey, &encrypted); err != nil || strings.Contains(encrypted, "local-chat") {
		t.Fatal("runtime not encrypted", err)
	}
	other := &Server{cfg: f.s.cfg, db: f.s.db}
	runtime, err := other.llmRuntime(context.Background())
	if err != nil || runtime.ChatModel != "local-chat" {
		t.Fatal(runtime, err)
	}
	if settings := f.call("GET", "/settings/rag", access, nil, 200); str(settings, "llm_model") != "local-chat" {
		t.Fatal(settings)
	}
	if err = f.s.requireResearchModel(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	answer, _, err := f.s.llm(context.Background(), user, "system", "test", false)
	if err != nil || answer != "local answer" || called.Load() != 1 {
		t.Fatal(answer, err)
	}
	reset := f.call("DELETE", path, access, nil, 200)
	if str(reset, "base_url") != f.s.cfg.LLMURL || str(reset, "chat_model") != f.s.cfg.LLMModel {
		t.Fatal("clear did not restore deployment endpoint", reset)
	}
	f.s.cfg.ProviderPolicyEnabled = true
	f.s.cfg.ApprovedAIProviders = []string{"self_hosted"}
	f.call("POST", path, access, M{"base_url": "https://api.openai.com/v1", "chat_model": "gpt-test"}, 403)
	after, _ := other.llmRuntime(context.Background())
	if after.BaseURL != f.s.cfg.LLMURL {
		t.Fatal("blocked config changed endpoint")
	}
}

func TestProviderCourtListenerInstanceCredential(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	viewer := f.account(access, "court-settings-viewer@example.test")
	user := str(f.call("GET", "/auth/me", access, nil, 200), "id")
	path := "/admin/integrations/courtlistener"
	for _, method := range []string{"GET", "POST", "DELETE"} {
		f.call(method, path, viewer, M{}, 403)
	}
	if initial := f.call("GET", path, access, nil, 200); initial["configured"] != false || initial["source"] != "none" || initial["masked"] != nil {
		t.Fatal(initial)
	}
	for _, token := range []string{"          ", "too-short", "a-token\nwith-control", strings.Repeat("k", 201)} {
		f.call("POST", path, access, M{"api_token": token}, 400)
	}
	key := "court-instance-secret-token"
	saved := f.call("POST", path, access, M{"api_token": key}, 200)
	if saved["configured"] != true || str(saved, "source") != "instance" || strings.Contains(string(jsonBytes(saved)), key) {
		t.Fatal("invalid credential status")
	}
	other := &Server{cfg: f.s.cfg, db: f.s.db}
	if got, err := other.providerKey(context.Background(), user, "courtlistener", ""); err != nil || got != key {
		t.Fatal("runtime credential was not shared", err)
	}
	var stored string
	if err := f.s.kvGet(context.Background(), instanceCourtKey, &stored); err != nil || stored == key {
		t.Fatal("unencrypted instance credential", err)
	}
	f.s.cfg.CourtListenerToken = "environment-court-token"
	reset := f.call("DELETE", path, access, nil, 200)
	if reset["source"] != "env" || reset["configured"] != true {
		t.Fatal(reset)
	}
	if got, err := f.s.providerKey(context.Background(), user, "courtlistener", ""); err != nil || got != f.s.cfg.CourtListenerToken {
		t.Fatal("environment fallback lost", err)
	}
	var leaked int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM cc_audit WHERE details::text LIKE $1`, "%"+key+"%").Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("audit contains credential", err)
	}
}

func TestProviderPolicyEnvironmentFormats(t *testing.T) {
	file := t.TempDir() + "/goodmem.json"
	if err := savePrivateJSON(file, GoodMemSecurity{Version: 1, ControllerID: "fixture-controller", APIKey: "fixture", SpaceID: "space", Subjects: []GoodMemSubject{{"fixture-reader", "reader-key"}}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOODMEM_CONFIG_FILE", file)
	t.Setenv("DATABASE_URL", "postgres://fixture/test")
	t.Setenv("GOODMEM_API_KEY", "fixture")
	t.Setenv("GOODMEM_SPACE_ID", "space")
	t.Setenv("SECRET_KEY", strings.Repeat("s", 40))
	t.Setenv("DEBUG", "true")
	t.Setenv("HIPAA_ENFORCEMENT_ENABLED", "true")
	for _, value := range []string{`["anthropic","self_hosted"]`, "anthropic,self_hosted"} {
		t.Setenv("APPROVED_AI_PROVIDERS", value)
		config, err := LoadConfig()
		if err != nil {
			t.Fatal(err)
		}
		s := Server{cfg: config}
		if s.allowAIProvider("anthropic") != nil || s.allowAIProvider("self_hosted") != nil || s.allowAIProvider("openai") == nil {
			t.Fatal("invalid policy parse")
		}
	}
	t.Setenv("APPROVED_AI_PROVIDERS", "")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	s := Server{cfg: config}
	if s.allowAIProvider("self_hosted") == nil {
		t.Fatal("empty enforced policy allowed AI")
	}
	t.Setenv("APPROVED_AI_PROVIDERS", `[123]`)
	if _, err = LoadConfig(); err == nil {
		t.Fatal("invalid allowlist accepted")
	}
}

func TestProviderDraftSingleRetryBudget(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	user := str(f.call("GET", "/auth/me", access, nil, 200), "id")
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		writeJSON(w, 429, M{"error": M{"message": "rate limited"}})
	}))
	defer provider.Close()
	f.s.cfg.LLMURL = provider.URL
	plan := M{"title": "Memo", "sections": []M{{"number": 1, "title": "Notice", "brief": "Explain notice requirements", "target_words": 200}}}
	if _, err := f.s.generateContractDraft(context.Background(), user, "Draft memo", nil, M{"plan": plan}); err == nil || calls.Load() != 3 {
		t.Fatal("draft multiplied provider retries", err, calls.Load())
	}
}
