package install

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorProviderProtocols(t *testing.T) {
	seen := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Error("missing provider authorization")
		}
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			t.Error("invalid request")
		}
		seen[r.URL.Path]++
		switch r.URL.Path {
		case "/embeddings":
			if payload["model"] == "voyage-test" && (payload["input_type"] != "document" || payload["output_dimension"] != float64(3)) {
				t.Error("Voyage embedding parameters missing")
			}
			fmt.Fprint(w, `{"data":[{"embedding":[0.1,0.2,0.3]}]}`)
		case "/chat/completions":
			if payload["max_completion_tokens"] != float64(512) {
				t.Error("missing bounded completion budget")
			}
			fmt.Fprint(w, `{"choices":[{"message":{"content":"OK"}}]}`)
		case "/rerank":
			if len(payload["documents"].([]any)) != 1 {
				t.Error("expected one synthetic document")
			}
			fmt.Fprint(w, `{"data":[{"index":0,"relevance_score":0.9}]}`)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	for _, err := range []error{
		doctorGeneration(ctx, server.URL, "fixture-secret", "gpt-5.4-mini"),
		doctorEmbedding(ctx, server.URL, "fixture-secret", "text-embedding-3-small", "3", false),
		doctorEmbedding(ctx, server.URL, "fixture-secret", "voyage-test", "3", true),
		doctorRerank(ctx, server.URL, "fixture-secret", "test-reranker"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if seen["/embeddings"] != 2 || seen["/chat/completions"] != 1 || seen["/rerank"] != 1 {
		t.Fatal(seen)
	}
	if err := doctorEmbedding(ctx, server.URL, "fixture-secret", "test", "16", false); err == nil {
		t.Fatal("wrong dimensions passed")
	}
}

func TestDoctorNeverLeaksSecretsOrFollowsRedirects(t *testing.T) {
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(401)
		fmt.Fprint(w, "DO-NOT-PRINT-secret provider body")
	}))
	defer server.Close()
	for _, endpoint := range []string{server.URL + "/redirect", server.URL + "/unauthorized", server.URL + "/?key=DO-NOT-PRINT", "http://DO-NOT-PRINT:password@localhost/"} {
		var result any
		err := doctorRequest(context.Background(), "GET", endpoint, doctorAuth("DO-NOT-PRINT"), nil, &result)
		if err == nil || strings.Contains(err.Error(), "DO-NOT-PRINT") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
	if redirected {
		t.Fatal("followed redirect with credentials")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var result any
	if doctorRequest(ctx, "GET", server.URL, nil, nil, &result) == nil {
		t.Fatal("ignored cancellation")
	}
	if err := doctorDatabase(ctx, "postgres://DO-NOT-PRINT@127.0.0.1:1/test?connect_timeout=1"); err == nil || strings.Contains(err.Error(), "DO-NOT-PRINT") {
		t.Fatal("unsafe database error")
	}
}

func TestDoctorOCRRejectsWrongModelAndMalformedResponse(t *testing.T) {
	for _, response := range []string{`{"data":[{"id":"generic-text-model"}]}`, `{"data":[]}`, `not json`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, response) }))
		if doctorOCR(context.Background(), server.URL) == nil {
			t.Fatal("accepted wrong OCR model")
		}
		server.Close()
	}
}

func TestDoctorDefaultDoesNotCallProvidersOrRewriteRuntime(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			fmt.Fprint(w, `{"data":[{"id":"pairsys/pairsys.ocr"}]}`)
		case "/v1/spaces/test-space":
			if r.Header.Get("X-Api-Key") != "scoped-fixture" {
				t.Error("wrong GoodMem credential")
			}
			fmt.Fprint(w, `{"spaceId":"test-space"}`)
		default:
			calls++
			http.Error(w, "unexpected provider call", 500)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "goodmem.json")
	config := fmt.Sprintf(`{"security_version":1,"controller_id":"controller","api_key":"scoped-fixture","space_id":"test-space","base_url":%q}`, server.URL)
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOODMEM_CONFIG_FILE", path)
	t.Setenv("GOODMEM_BASE_URL", server.URL)
	t.Setenv("GOODMEM_OCR_BASE_URL", server.URL)
	t.Setenv("DATABASE_URL", "postgres://fixture@127.0.0.1:1/test?connect_timeout=1")
	for _, key := range []string{"OPENAI_API_KEY", "VOYAGE_API_KEY", "EMBEDDING_API_KEY"} {
		t.Setenv(key, "DO-NOT-PRINT")
	}
	for _, key := range []string{"OPENAI_BASE_URL", "VOYAGE_BASE_URL", "EMBEDDING_BASE_URL"} {
		t.Setenv(key, server.URL)
	}
	var output strings.Builder
	if Doctor(context.Background(), &output, false) == nil {
		t.Fatal("unavailable database should fail")
	}
	if calls != 0 || strings.Contains(output.String(), "DO-NOT-PRINT") {
		t.Fatal("default made provider calls or exposed keys")
	}
	if !strings.Contains(output.String(), "PASS  Scoped GoodMem access") || !strings.Contains(output.String(), "PASS  OCR model") {
		t.Fatal(output.String())
	}
	after, _ := os.ReadFile(path)
	if string(after) != config {
		t.Fatal("runtime configuration changed")
	}
	t.Setenv("GOODMEM_BASE_URL", server.URL+"/different")
	if doctorGoodMem(context.Background()) == nil {
		t.Fatal("sent scoped credential to a different endpoint")
	}
}

// Opt-in smoke test for the actual configured providers. Never runs in CI by
// default and never reads documents. These small requests can incur API charges.
func TestDoctorLiveProviders(t *testing.T) {
	if os.Getenv("CASECITE_DOCTOR_LIVE") != "1" {
		t.Skip("set CASECITE_DOCTOR_LIVE=1 and provider keys to run billable live probes")
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Fatal("OPENAI_API_KEY is required")
	}
	ctx := context.Background()
	t.Run("generation", func(t *testing.T) {
		if err := doctorGeneration(ctx, doctorEnv("OPENAI_BASE_URL", "https://api.openai.com/v1"), key, doctorEnv("LLM_MODEL", "gpt-5.4-mini")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("embeddings", func(t *testing.T) {
		if err := doctorEmbedding(ctx, doctorEnv("EMBEDDING_BASE_URL", "https://api.openai.com/v1"), doctorEnv("EMBEDDING_API_KEY", key), doctorEnv("EMBEDDING_MODEL", "text-embedding-3-small"), doctorEnv("EMBEDDING_DIMENSIONS", "1536"), false); err != nil {
			t.Fatal(err)
		}
	})
	if voyage := os.Getenv("VOYAGE_API_KEY"); voyage != "" {
		t.Run("voyage", func(t *testing.T) {
			base := doctorEnv("VOYAGE_BASE_URL", "https://api.voyageai.com/v1")
			if err := doctorEmbedding(ctx, base, voyage, doctorEnv("VOYAGE_EMBEDDING_MODEL", "voyage-4-large"), doctorEnv("VOYAGE_EMBEDDING_DIMENSIONS", "1024"), true); err != nil {
				t.Fatal(err)
			}
			if err := doctorRerank(ctx, base, voyage, doctorEnv("VOYAGE_RERANKER_MODEL", "rerank-3-lite")); err != nil {
				t.Fatal(err)
			}
		})
	}
	if base := os.Getenv("GOODMEM_OCR_BASE_URL"); base != "" {
		t.Run("ocr-model", func(t *testing.T) {
			if err := doctorOCR(ctx, base); err != nil {
				t.Fatal(err)
			}
		})
	}
}
