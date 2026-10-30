package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestClauseEmbeddingProvidersAndPrivateEndpoint(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	user := str(f.call("GET", "/auth/me", access, nil, 200), "id")
	f.s.cfg.LLMKey = ""
	f.s.cfg.LLMURL = "https://chat-gateway.example/v1"
	ctx := context.Background()
	for _, family := range []string{"voyage", "cohere", "openai"} {
		f.call("POST", "/user/keys/save", access, M{"key_type": family, "api_key": "personal-" + family + "-embedding"}, 200)
		calls := 0
		f.s.client.Transport = modelTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			var in M
			json.NewDecoder(r.Body).Decode(&in)
			var response M
			switch family {
			case "voyage":
				if r.URL.Host != "api.voyageai.com" || str(in, "model") != "voyage-law-2" || str(in, "input_type") != "document" {
					t.Error(r.URL, in)
				}
			case "cohere":
				if r.URL.Host != "api.cohere.ai" || r.URL.Path != "/v1/embed" || str(in, "model") != "embed-english-v3.0" || str(in, "input_type") != "search_document" || len(stringsOf(in["texts"])) != 2 {
					t.Error(r.URL, in)
				}
			case "openai":
				if r.URL.Host != "api.openai.com" || str(in, "model") != "text-embedding-3-small" {
					t.Error("chat endpoint leaked into embedding request", r.URL, in)
				}
			}
			if r.Header.Get("Authorization") != "Bearer personal-"+family+"-embedding" {
				t.Error("wrong embedding credential")
			}
			if family == "cohere" {
				response = M{"embeddings": [][]float64{{1, 0}, {0, 1}}}
			} else {
				response = M{"data": []M{{"index": 1, "embedding": []float64{0, 1}}, {"index": 0, "embedding": []float64{1, 0}}}}
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(jsonBytes(response))))}, nil
		})
		vectors, err := f.s.contractEmbeddings(ctx, user, []string{"canonical", "clause"})
		if err != nil || len(vectors) != 2 || vectors[0][0] != 1 || vectors[1][1] != 1 || calls != 1 {
			t.Fatal(vectors, err, calls)
		}
		f.s.cfg.ProviderPolicyEnabled = true
		f.s.cfg.ApprovedAIProviders = []string{"self_hosted"}
		if _, err = f.s.contractEmbeddings(ctx, user, []string{"private"}); err == nil || calls != 1 {
			t.Fatal("embedding bypassed policy", err, calls)
		}
		f.s.cfg.ProviderPolicyEnabled = false
		if family != "openai" {
			f.call("DELETE", "/user/keys/"+family, access, nil, 200)
		}
	}
	f.s.cfg.EmbeddingURL = "http://embeddings:8001/v1"
	f.s.cfg.EmbeddingModel = "local-embedding"
	f.s.cfg.EmbeddingKey = "instance-only"
	route, err := f.s.clauseEmbeddingRoute(ctx, user)
	if err != nil || route.Key != "instance-only" || route.Provider != "self_hosted" || route.Model != "local-embedding" {
		t.Fatal(route, err)
	}
	f.s.cfg.EmbeddingKey = ""
	f.s.client.Transport = modelTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "embeddings:8001" || r.Header.Get("Authorization") != "" {
			t.Error("personal key sent to custom embedding server")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":[{"index":0,"embedding":[0.1,0.2]}]}`))}, nil
	})
	if _, err = f.s.contractEmbeddings(ctx, user, []string{"private"}); err != nil {
		t.Fatal(err)
	}
}

func TestClauseEmbeddingRejectsMissingDuplicateAndMismatchedVectors(t *testing.T) {
	for _, response := range []string{
		`{"data":[{"index":0,"embedding":[1,2]}]}`,
		`{"data":[{"index":0,"embedding":[1,2]},{"index":0,"embedding":[1,2]}]}`,
		`{"data":[{"embedding":[1,2]},{"index":1,"embedding":[1,2]}]}`,
		`{"data":[{"index":0,"embedding":[1,2]},{"index":1,"embedding":[1]}]}`,
		`{"data":[{"index":0,"embedding":[]},{"index":1,"embedding":[1,2]}]}`,
		`{"data":[{"index":0,"embedding":[null,2]},{"index":1,"embedding":[1,2]}]}`,
	} {
		s := Server{client: &http.Client{Transport: modelTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(response))}, nil
		})}}
		if _, err := s.contractEmbeddingsAt(context.Background(), embeddingRoute{Provider: "self_hosted", Kind: "openai", Endpoint: "http://local"}, []string{"one", "two"}); err == nil {
			t.Errorf("accepted invalid vectors: %s", response)
		}
	}
}
