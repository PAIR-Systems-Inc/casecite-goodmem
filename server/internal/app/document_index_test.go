package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type indexFixture struct {
	mu               sync.Mutex
	spaces, requests map[string]M
	creates          int
	disabled         bool
}

func indexCatalogFixture(t *testing.T, f *fixture) *indexFixture {
	t.Helper()
	x := &indexFixture{spaces: map[string]M{}, requests: map[string]M{}}
	routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		x.mu.Lock()
		defer x.mu.Unlock()
		base := M{"embedderId": "base", "displayName": "Default embedding", "modelIdentifier": "base-model", "dimensionality": 1536, "distributionType": "DENSE", "supportedModalities": []string{"TEXT"}, "endpointUrl": "https://api.openai.com/v1", "credentials": "never-return-this"}
		alt := M{"embedderId": "alt", "displayName": "Local legal embedding", "modelIdentifier": "local-law", "dimensionality": 768, "distributionType": "DENSE", "supportedModalities": []string{"TEXT"}, "endpointUrl": "http://local-model:8000/v1", "credentials": "never-return-this", "labels": M{"casecite": "enabled"}}
		switch {
		case r.URL.Path == "/v1/embedders":
			entries := []M{base, {"embedderId": "other-app", "modelIdentifier": "private", "distributionType": "DENSE", "supportedModalities": []string{"TEXT"}, "dimensionality": 768}}
			if !x.disabled {
				entries = append(entries, alt)
			}
			writeJSON(w, 200, M{"embedders": entries})
			return true
		case r.URL.Path == "/v1/embedders/base":
			writeJSON(w, 200, base)
			return true
		case r.URL.Path == "/v1/spaces/space":
			writeJSON(w, 200, M{"spaceId": "space", "spaceEmbedders": []M{{"embedderId": "base"}}})
			return true
		case r.URL.Path == "/v1/spaces" && r.Method == "POST":
			m := M{}
			json.NewDecoder(r.Body).Decode(&m)
			// v1.0.334 uses authorization grants; the older publicRead flag
			// shown in some REST documentation is no longer an accepted field.
			allowed := map[string]bool{"spaceId": true, "name": true, "labels": true, "spaceEmbedders": true, "defaultChunkingConfig": true, "ownerId": true}
			for key := range m {
				if !allowed[key] {
					t.Errorf("unknown GoodMem create-space field %q", key)
					w.WriteHeader(400)
					return true
				}
			}
			id := str(m, "spaceId")
			if x.spaces[id] != nil {
				w.WriteHeader(409)
				return true
			}
			x.spaces[id] = m
			x.creates++
			writeJSON(w, 201, m)
			return true
		case strings.HasPrefix(r.URL.Path, "/v1/spaces/"):
			if m := x.spaces[strings.TrimPrefix(r.URL.Path, "/v1/spaces/")]; m != nil {
				writeJSON(w, 200, m)
			} else {
				w.WriteHeader(404)
			}
			return true
		case r.URL.Path == "/v1/memories" && r.Method == "POST":
			data, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(data))
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return false
			}
			m := M{}
			json.Unmarshal([]byte(r.FormValue("request")), &m)
			x.requests[str(m, "memoryId")] = m
			r.MultipartForm.RemoveAll()
			r.MultipartForm, r.Form, r.PostForm = nil, nil, nil
			r.Body = io.NopCloser(bytes.NewReader(data))
			return false
		case r.URL.Path == "/v1/memories:retrieve":
			m := M{}
			json.NewDecoder(r.Body).Decode(&m)
			w.Header().Set("Content-Type", "application/x-ndjson")
			enc := json.NewEncoder(w)
			enc.Encode(M{"resultSetBoundary": M{"resultSetId": "r", "kind": "BEGIN", "stageName": "vector_search"}})
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, key := range maps(m["spaceKeys"]) {
				for id, data := range f.memories {
					if str(x.requests[id], "spaceId") == str(key, "spaceId") && strings.Contains(str(key, "filter"), "'"+id+"'") {
						enc.Encode(M{"retrievedItem": M{"chunk": M{"resultSetId": "r", "relevanceScore": 0.1, "chunk": M{"chunkId": id + "-chunk", "memoryId": id, "chunkText": string(data)}}}})
					}
				}
			}
			return true
		case r.URL.Path == "/v1/ocr:document":
			writeJSON(w, 200, M{"pageCount": 1, "pages": []M{ocrPage(0, "Notice is 30 days.")}})
			return true
		}
		return false
	})
	return x
}

func TestDocumentIndexCatalogAndSettings(t *testing.T) {
	f := newFixture(t)
	x := indexCatalogFixture(t, f)
	access := f.admin()
	f.call("GET", "/settings/document-embedders", "", nil, 401)
	catalog := f.call("GET", "/settings/document-embedders", access, nil, 200)
	if len(maps(catalog["models"])) != 2 || str(catalog, "default_model") != "base-model" || strings.Contains(string(jsonBytes(catalog)), "never-return") || strings.Contains(string(jsonBytes(catalog)), "endpointUrl") {
		t.Fatal(catalog)
	}
	f.call("PUT", "/settings/rag", access, M{"document_embedder_id": "other-app"}, 400)
	f.call("PUT", "/settings/rag", access, M{"document_embedder_id": "alt", "chunk_size": 400, "chunk_overlap": 0}, 200)
	f.call("PUT", "/settings", access, M{"llm_model": "new-chat", "top_k": 12}, 200)
	settings := f.call("GET", "/settings/rag", access, nil, 200)
	if str(settings, "document_embedder_id") != "alt" || number(settings, "chunk_overlap", -1) != 0 {
		t.Fatal(settings)
	}
	x.mu.Lock()
	if x.creates != 0 {
		t.Error("saving a preference must not create remote resources")
	}
	x.disabled = true
	x.mu.Unlock()
	f.call("PUT", "/settings/rag", access, M{"document_embedder_id": "alt"}, 400)
	// A disabled selection blocks new indexing rather than silently using a
	// different model; changing the selection to the default remains possible.
	user := str(f.call("GET", "/auth/me", access, nil, 200), "id")
	if _, _, err := f.s.beginDocumentIngest(context.Background(), user, "", uuid(), "test.txt", "local", "", []byte("Notice is 30 days."), M{}); err == nil {
		t.Fatal("disabled model accepted")
	}
	f.call("PUT", "/settings/rag", access, M{"document_embedder_id": ""}, 200)
}

func TestDocumentIndexCatalogRespectsProvisionedExecutionAuthority(t *testing.T) {
	f := newFixture(t)
	indexCatalogFixture(t, f)
	f.s.cfg.GoodMemSecurity = &GoodMemSecurity{EmbedderIDs: []string{"base"}}
	access := f.admin()
	catalog := f.call("GET", "/settings/document-embedders", access, nil, 200)
	models := maps(catalog["models"])
	if len(models) != 1 || str(models[0], "id") != "base" {
		t.Fatal("model catalog advertised an unprovisioned embedder", catalog)
	}
	// A public enablement label is not an execution grant for this controller.
	f.call("PUT", "/settings/rag", access, M{"document_embedder_id": "alt"}, 400)
}

func TestDocumentIndexProfilesMixedSearchAndRebuildSnapshot(t *testing.T) {
	f := newFixture(t)
	x := indexCatalogFixture(t, f)
	access := f.admin()
	old := f.upload(access, "", "old.txt", "Old notice is 30 days.")
	f.call("PUT", "/settings/rag", access, M{"document_embedder_id": "alt", "chunk_size": 400, "chunk_overlap": 20}, 200)
	newDoc := f.upload(access, "", "new.txt", "New notice is 60 days.")
	profile := object(newDoc["index_profile"])
	if str(profile, "space_id") == "space" || str(profile, "model") != "local-law" {
		t.Fatal(profile)
	}
	x.mu.Lock()
	request := x.requests[str(newDoc, "memory_id")]
	chunk := object(object(request["chunkingConfig"])["recursive"])
	x.mu.Unlock()
	if number(chunk, "chunkSize", 0) != 400 || number(chunk, "chunkOverlap", -1) != 20 || str(chunk, "lengthMeasurement") != "CHARACTER_COUNT" {
		t.Fatal(chunk)
	}
	hits, err := f.s.searchDocumentIndexes(context.Background(), "notice", []M{old, newDoc}, 5, false)
	if err != nil || len(hits) != 2 {
		t.Fatal(hits, err)
	}
	only, err := f.s.searchDocumentIndexes(context.Background(), "notice", []M{newDoc}, 5, false)
	if err != nil || len(only) != 1 || only[0].MemoryID != documentSearchMemory(newDoc) {
		t.Fatal(only, err)
	}
	f.call("PUT", "/settings/rag", access, M{"chunk_size": 700, "chunk_overlap": 60}, 200)
	job := str(f.call("POST", "/settings/reindex?async=true", access, nil, 200), "job_id")
	f.call("PUT", "/settings/rag", access, M{"document_embedder_id": "", "chunk_size": 900}, 200)
	awaitChatFixture(t, runChatFixtureJob(t, f, job))
	result := object(f.call("GET", "/jobs/"+job, access, nil, 200)["result"])
	if number(result, "reindexed", 0) != 2 || str(result, "embedding_model") != "local-law" {
		t.Fatal(result)
	}
	for _, prior := range []M{old, newDoc} {
		current := f.call("GET", "/documents/"+str(prior, "id"), access, nil, 200)
		p := object(current["index_profile"])
		if str(current, "memory_id") != str(prior, "memory_id") || str(p, "space_id") != str(profile, "space_id") || number(p, "chunk_size", 0) != 700 {
			t.Fatal(current)
		}
	}
}

func TestDocumentOCRKeepsReservedEmbeddingProfile(t *testing.T) {
	f := newFixture(t)
	x := indexCatalogFixture(t, f)
	access := f.admin()
	f.call("PUT", "/settings/rag", access, M{"document_embedder_id": "alt", "chunk_size": 600, "chunk_overlap": 0}, 200)
	doc := f.upload(access, "", "scan.pdf", string(ocrFile(t, "scanned_notice.pdf")))
	f.call("PUT", "/settings/rag", access, M{"document_embedder_id": "", "chunk_size": 900}, 200)
	runReviewJob(t, f, access, str(doc, "ingest_job_id"))
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, id := range []string{str(doc, "memory_id"), str(doc, "index_memory_id")} {
		m := x.requests[id]
		chunk := object(object(m["chunkingConfig"])["recursive"])
		if str(m, "spaceId") != str(object(doc["index_profile"]), "space_id") || number(chunk, "chunkSize", 0) != 600 || number(chunk, "chunkOverlap", -1) != 0 {
			t.Fatal(m)
		}
	}
}

func TestDocumentIndexConcurrentSpaceCreation(t *testing.T) {
	f := newFixture(t)
	x := indexCatalogFixture(t, f)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			profile, err := f.s.resolveIndexProfile(context.Background(), M{"document_embedder_id": "alt"})
			if err != nil || str(profile, "space_id") == "" {
				t.Error(profile, err)
			}
		})
	}
	wg.Wait()
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.creates != 1 {
		t.Fatal("duplicate GoodMem spaces", x.creates)
	}
}

func TestDocumentIndexRejectsMismatchedSpaceAndProviderPolicy(t *testing.T) {
	f := newFixture(t)
	x := indexCatalogFixture(t, f)
	settings := M{"document_embedder_id": "alt"}
	profile, err := f.s.resolveIndexProfile(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	x.mu.Lock()
	x.spaces[str(profile, "space_id")]["spaceEmbedders"] = []M{{"embedderId": "base"}}
	x.mu.Unlock()
	if _, err = f.s.resolveIndexProfile(context.Background(), settings); err == nil {
		t.Fatal("wrong space association accepted")
	}
	f.s.cfg.ProviderPolicyEnabled = true
	f.s.cfg.ApprovedAIProviders = []string{"openai"}
	catalog, err := f.s.documentEmbeddingCatalog(context.Background())
	if err != nil || len(maps(catalog["models"])) != 1 {
		t.Fatal(catalog, err)
	}
	if _, err = f.s.resolveIndexProfile(context.Background(), settings); err == nil {
		t.Fatal("disallowed provider accepted")
	}
	if _, err = f.s.searchDocumentIndexes(context.Background(), "notice", []M{{"memory_id": "private", "index_profile": profile}}, 3, false); err == nil {
		t.Fatal("search ignored changed provider policy")
	}
	f.s.cfg.ApprovedAIProviders = []string{"self_hosted"}
	if _, err = f.s.resolveIndexProfile(context.Background(), M{}); err == nil {
		t.Fatal("instance-default model bypassed provider policy")
	}
	if _, err = f.s.searchDocumentIndexes(context.Background(), "notice", []M{{"memory_id": "legacy"}}, 3, false); err == nil {
		t.Fatal("legacy document bypassed provider policy")
	}
}
