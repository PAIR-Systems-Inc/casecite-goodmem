package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetupModelsResumesLostCreationResponseWithoutDuplicates(t *testing.T) {
	state := M{}
	stored := M{}
	posts, saves := 0, 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			if len(stored) == 0 {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{}`))
				return
			}
			_ = json.NewEncoder(w).Encode(stored)
			return
		}
		posts++
		if str(state, "llm_id") == "" || saves != 1 {
			t.Error("POST occurred before durable resource ID")
		}
		_ = json.NewDecoder(r.Body).Decode(&stored)
		if str(object(object(stored["credentials"])["apiKey"]), "inlineSecret") != "test-only" {
			t.Error("missing structured credential")
		}
		// Server creates the resource, but the client does not get its response.
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer s.Close()
	g := NewGoodMem(Config{GoodMemURL: s.URL, GoodMemKey: "fixture"})
	payload := M{"providerType": "OPENAI", "endpointUrl": s.URL, "modelIdentifier": "fixture-model", "credentials": M{"kind": "CREDENTIAL_KIND_API_KEY", "apiKey": M{"inlineSecret": "test-only"}}}
	save := func() error { saves++; return nil }
	if ensureSetupModel(context.Background(), g, state, save, "llm_id", "llms", "llmId", payload) == nil {
		t.Fatal("lost response was not reported")
	}
	if err := ensureSetupModel(context.Background(), g, state, save, "llm_id", "llms", "llmId", payload); err != nil {
		t.Fatal(err)
	}
	if posts != 1 || saves != 1 {
		t.Fatalf("created duplicates: posts=%d saves=%d", posts, saves)
	}
	payload["modelIdentifier"] = "changed-model"
	if err := ensureSetupModel(context.Background(), g, state, save, "llm_id", "llms", "llmId", payload); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatal("silently replaced existing model")
	}
}

func TestSetupModelsStopsBeforeRemoteMutationWhenJournalFails(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected request") }))
	defer s.Close()
	g := NewGoodMem(Config{GoodMemURL: s.URL})
	want := errors.New("disk full")
	err := ensureSetupModel(context.Background(), g, M{}, func() error { return want }, "llm_id", "llms", "llmId", M{})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
}
