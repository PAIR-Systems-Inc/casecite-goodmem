package app

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func TestRetrievalFinalStageAndScope(t *testing.T) {
	stream := `{"resultSetBoundary":{"resultSetId":"raw","kind":"BEGIN","stageName":"retrieve"}}
{"retrievedItem":{"chunk":{"resultSetId":"raw","relevanceScore":-0.9,"chunk":{"chunkId":"first","memoryId":"allowed","chunkText":"raw text"}}}}
{"resultSetBoundary":{"resultSetId":"ranked","kind":"BEGIN","stageName":"rerank"}}
{"retrievedItem":{"chunk":{"resultSetId":"ranked","relevanceScore":3.4,"chunk":{"chunkId":"secret","memoryId":"denied","chunkText":"must not escape"}}}}
{"retrievedItem":{"chunk":{"resultSetId":"ranked","relevanceScore":2.8,"chunk":{"chunkId":"final","memoryId":"allowed","chunkText":"final text"}}}}
`
	hits, err := decodeRetrieval(strings.NewReader(stream), map[string]bool{"allowed": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Text != "final text" || hits[0].Score != 2.8 {
		t.Fatalf("unexpected hits: %+v", hits)
	}
	if _, err = decodeRetrieval(strings.NewReader(`{"status":{"code":"PERMISSION_DENIED"}}`), nil); err == nil {
		t.Fatal("upstream failure must not become an empty success")
	}
}
func TestEmptyScopeNeverQueriesGoodMem(t *testing.T) {
	g := NewGoodMem(Config{GoodMemURL: "http://invalid.invalid"})
	hits, err := g.Search(context.Background(), "private", nil, 10)
	if err != nil || len(hits) != 0 {
		t.Fatal(hits, err)
	}
}
func TestExactQuoteVerification(t *testing.T) {
	m := M{"quote": "Notice is 30 days."}
	verifyQuote(m, SourceDocument{"d", "agreement", "😀 Notice is 30 days.", "full text"})
	if m["quote_verified"] != true || m["span_start"] != 3 {
		t.Fatal(m)
	}
	m["quote"] = "Notice is 60 days."
	verifyQuote(m, SourceDocument{"d", "agreement", "Notice is 30 days.", "full text"})
	if m["quote_verified"] != false || m["span_start"] != nil {
		t.Fatal(m)
	}
}
func TestWordRoundTrip(t *testing.T) {
	b, err := wordDocument("Draft & review", "A < B\nPayment is due in 30 days.")
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil || len(z.File) != 3 {
		t.Fatal("invalid DOCX", err)
	}
	text, err := extractText(b, "draft.docx")
	if err != nil || !strings.Contains(text, "A < B") {
		t.Fatal(text, err)
	}
}
func TestCredentialAndCSRFTampering(t *testing.T) {
	s := &Server{cfg: Config{Secret: strings.Repeat("a", 40)}}
	value, err := s.crypt("private-key", false)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := s.crypt(value, true)
	if err != nil || plain != "private-key" {
		t.Fatal(err)
	}
	s.cfg.Secret = strings.Repeat("b", 40)
	if _, err = s.crypt(value, true); err == nil {
		t.Fatal("wrong key decrypted credential")
	}
	token := s.csrfValue()
	if !s.validCSRF(token) || s.validCSRF(token+"0") {
		t.Fatal("invalid CSRF validation")
	}
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL for PostgreSQL integration tests")
	}
	root, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schemaName := "test_" + strings.ReplaceAll(uuid(), "-", "")
	if _, err = root.Exec(`CREATE SCHEMA ` + schemaName); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schemaName)
	u.RawQuery = q.Encode()
	db, err := OpenDB(context.Background(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); root.Exec(`DROP SCHEMA ` + schemaName + ` CASCADE`); root.Close() })
	return db
}

type fixture struct {
	s         *Server
	http      *httptest.Server
	t         *testing.T
	mu        sync.Mutex
	memories  map[string][]byte
	retrieved []string
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{t: t, memories: map[string][]byte{}}
	gm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("x-api-key") != "fixture-key" {
			http.Error(w, "key", 401)
			return
		}
		switch {
		case r.URL.Path == "/v1/spaces/space":
			writeJSON(w, 200, M{"spaceId": "space", "spaceEmbedders": []M{{"embedderId": "fixture"}}})
		case r.URL.Path == "/v1/embedders/fixture":
			writeJSON(w, 200, M{"embedderId": "fixture", "modelIdentifier": "fixture-model", "endpointUrl": "http://local-model:8000/v1"})
		case r.Method == "POST" && r.URL.Path == "/v1/memories":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			defer r.MultipartForm.RemoveAll()
			m := M{}
			if err := json.Unmarshal([]byte(r.FormValue("request")), &m); err != nil {
				t.Error(err)
			}
			if m["extractPageImages"] != true && str(object(m["metadata"]), "casecite_source_memory_id") == "" {
				t.Error("page extraction not requested")
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				return
			}
			defer file.Close()
			b, _ := io.ReadAll(file)
			f.memories[str(m, "memoryId")] = b
			writeJSON(w, 200, M{"memoryId": m["memoryId"]})
		case r.URL.Path == "/v1/memories:retrieve":
			m := M{}
			json.NewDecoder(r.Body).Decode(&m)
			scope := str(maps(m["spaceKeys"])[0], "filter")
			f.retrieved = append(f.retrieved, scope)
			w.Header().Set("Content-Type", "application/x-ndjson")
			enc := json.NewEncoder(w)
			enc.Encode(M{"resultSetBoundary": M{"resultSetId": "r", "kind": "BEGIN", "stageName": "retrieve"}})
			for id, b := range f.memories {
				if strings.Contains(scope, id) {
					enc.Encode(M{"retrievedItem": M{"chunk": M{"resultSetId": "r", "relevanceScore": -0.8, "chunk": M{"chunkId": uuid(), "memoryId": id, "chunkText": string(b), "chunkSequenceNumber": 0}}}})
				}
			}
		default:
			p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			if len(p) < 3 {
				http.NotFound(w, r)
				return
			}
			id := p[2]
			b, ok := f.memories[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if r.Method == "DELETE" {
				delete(f.memories, id)
				w.WriteHeader(204)
			} else if strings.HasSuffix(r.URL.Path, "/content") {
				w.Write(b)
			} else if strings.HasSuffix(r.URL.Path, "/pages") {
				writeJSON(w, 200, M{"pageImages": []M{{"pageIndex": 0, "dpi": 144, "contentType": "image/png"}}})
			} else if strings.HasSuffix(r.URL.Path, "/image") {
				w.Header().Set("Content-Type", "image/png")
				w.Write([]byte("PNG fixture"))
			} else {
				writeJSON(w, 200, M{"memoryId": id, "processingStatus": "COMPLETED"})
			}
		}
	}))
	t.Cleanup(gm.Close)
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := M{}
		json.NewDecoder(r.Body).Decode(&m)
		messages := maps(m["messages"])
		prompt := str(messages[len(messages)-1], "content")
		answer := "The agreement requires 30 days of notice. [1]"
		if _, ok := m["response_format"]; ok {
			start := strings.Index(prompt, "Evidence JSON:\n")
			docs := []SourceDocument{}
			if start >= 0 {
				json.Unmarshal([]byte(prompt[start+len("Evidence JSON:\n"):]), &docs)
			}
			did := ""
			if len(docs) > 0 {
				did = docs[0].ID
			}
			out := M{"answer": "Review: notice is 30 days.", "findings": []M{{"document_id": did, "title": "Notice", "severity": "minor", "quote": "Notice is 30 days.", "explanation": "Allow time to give notice."}, {"document_id": did, "title": "Invented quote", "quote": "Notice is 90 days."}}}
			if strings.Contains(prompt, "EVERY selected document") {
				rows := []M{}
				for _, d := range docs {
					rows = append(rows, M{"document_id": d.ID, "cells": []M{{"field": "Notice", "value": "30 days", "quote": "Notice is 30 days."}}})
				}
				out["rows"] = rows
			}
			answer = string(jsonBytes(out))
		}
		writeJSON(w, 200, M{"choices": []M{{"message": M{"content": answer}, "finish_reason": "stop"}}, "usage": M{"total_tokens": 100}})
	}))
	t.Cleanup(llm.Close)
	cfg := Config{Debug: true, PublicURL: "http://localhost", Secret: strings.Repeat("s", 40), BootstrapToken: "setup-token", GoodMemURL: gm.URL, GoodMemKey: "fixture-key", GoodMemSpace: "space", LLMURL: llm.URL, LLMKey: "llm-fixture", LLMModel: "test-model", SessionTTL: time.Hour, IdleTTL: time.Hour, MaxUpload: 1 << 20}
	f.s = New(cfg, testDB(t), fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("frontend")}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	f.http = httptest.NewServer(f.s)
	t.Cleanup(func() { f.http.Close(); f.s.Close() })
	return f
}
func (f *fixture) call(method, path, token string, in any, want int) M {
	f.t.Helper()
	var b io.Reader
	if in != nil {
		b = bytes.NewReader(jsonBytes(in))
	}
	req, _ := http.NewRequest(method, f.http.URL+"/api/v1"+path, b)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		f.t.Fatalf("%s %s: got %d want %d: %s", method, path, resp.StatusCode, want, data)
	}
	out := M{}
	json.Unmarshal(data, &out)
	return out
}
func (f *fixture) admin() string {
	return str(f.call("POST", "/auth/register", "bootstrap", M{"name": "Owner", "email": "owner@example.test", "password": "Cedar!Trail7826", "bootstrap_token": "setup-token"}, 200), "access_token")
}
func (f *fixture) account(admin, email string) string {
	f.call("POST", "/users", admin, M{"name": email, "email": email, "password": "Cedar!Trail7826"}, 200)
	r := f.call("POST", "/auth/login", "login", M{"email": email, "password": "Cedar!Trail7826"}, 200)
	token := str(r, "access_token")
	f.call("POST", "/auth/change-password", token, M{"current_password": "Cedar!Trail7826", "new_password": "River!Grove4928"}, 200)
	return token
}
func (f *fixture) upload(token, matter, name, content string) M {
	f.t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	p, _ := w.CreateFormFile("file", name)
	p.Write([]byte(content))
	w.WriteField("matter_id", matter)
	w.Close()
	req, _ := http.NewRequest("POST", f.http.URL+"/api/v1/documents", &b)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", w.FormDataContentType())
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer r.Body.Close()
	out := M{}
	json.NewDecoder(r.Body).Decode(&out)
	if r.StatusCode != 200 {
		f.t.Fatal(r.StatusCode, out)
	}
	return f.call("GET", "/documents/"+str(out, "id"), token, nil, 200)
}
func (f *fixture) waitJob(token, id string) M {
	f.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		m := f.call("GET", "/jobs/"+id, token, nil, 200)
		if str(m, "status") == "completed" {
			return f.call("GET", "/results/"+id, token, nil, 200)
		}
		if str(m, "status") == "failed" {
			f.t.Fatal(m)
		}
		time.Sleep(60 * time.Millisecond)
	}
	f.t.Fatal("job timeout")
	return nil
}

func TestEndToEndPermissionsAndJobs(t *testing.T) {
	f := newFixture(t)
	owner := f.admin()
	member := f.account(owner, "member@example.test")
	outsider := f.account(owner, "outsider@example.test")
	matter := f.call("POST", "/matters", owner, M{"name": "Shared matter"}, 200)
	mid := str(matter, "id")
	f.call("POST", "/matters/"+mid+"/members", owner, M{"email": "member@example.test"}, 200)
	doc := f.upload(owner, mid, "agreement.txt", "Notice is 30 days. Payment is due monthly.")
	did := str(doc, "id")
	secret := f.upload(owner, "", "private.txt", "Private negotiation strategy.")
	if str(doc, "status") != "indexed" {
		t.Fatal(doc)
	}
	f.call("GET", "/documents/"+did, outsider, nil, 404)
	f.call("GET", "/documents/"+did+"/pages", member, nil, 200)
	f.call("GET", "/documents/"+did+"/pages/0/image", outsider, nil, 404)
	f.call("GET", "/documents/"+did+"/file", member, nil, 200)
	f.call("POST", "/jobs", member, M{"kind": "research", "document_ids": []string{str(secret, "id")}, "prompt": "secrets"}, 400)
	f.call("DELETE", "/documents/"+did, member, nil, 403)
	f.s.StartWorkers()
	job := f.call("POST", "/jobs", member, M{"kind": "research", "document_ids": []string{did}, "prompt": "What notice?", "matter_id": mid}, 202)
	jid := str(job, "job_id")
	result := f.waitJob(member, jid)
	if len(maps(result["citations"])) != 1 {
		t.Fatal(result)
	}
	f.mu.Lock()
	scope := f.retrieved[len(f.retrieved)-1]
	f.mu.Unlock()
	if !strings.Contains(scope, str(doc, "memory_id")) || strings.Contains(scope, str(secret, "memory_id")) {
		t.Fatal("SQL ACL was not propagated", scope)
	}
	for _, kind := range []string{"analyze", "draft", "compare", "review"} {
		ids := []string{did}
		if kind == "compare" {
			ids = append(ids, str(f.upload(member, mid, "revision.txt", "Notice is 60 days."), "id"))
		}
		j := f.call("POST", "/jobs", member, M{"kind": kind, "document_ids": ids, "fields": []string{"Notice"}, "prompt": "Review the notice terms"}, 202)
		r := f.waitJob(member, str(j, "job_id"))
		if kind == "analyze" {
			findings := maps(r["findings"])
			if findings[0]["quote_verified"] != true || findings[1]["quote_verified"] != false {
				t.Fatal(findings)
			}
		}
		if kind == "review" {
			f.call("PATCH", "/results/"+str(r, "id")+"/cells", member, M{"document_id": did, "field": "Notice", "review_status": "approved"}, 200)
			f.call("GET", "/results/"+str(r, "id")+"/export?format=csv", member, nil, 200)
		}
		f.call("GET", "/results/"+str(r, "id")+"/export?format=docx", member, nil, 200)
	}
	// Follow-up keeps inherited ACL dependencies even if it selects other sources.
	child := f.call("POST", "/jobs", member, M{"kind": "research", "document_ids": []string{}, "prompt": "Follow up", "parent_id": jid}, 202)
	childID := str(child, "job_id")
	f.waitJob(member, childID)
	uid := str(f.call("GET", "/auth/me", member, nil, 200), "id")
	f.call("DELETE", "/matters/"+mid+"/members/"+uid, owner, nil, 200)
	f.call("GET", "/results/"+jid, member, nil, 403)
	f.call("GET", "/results/"+childID, member, nil, 403)
	f.call("GET", "/jobs/"+jid, member, nil, 403)
	f.call("POST", "/auth/logout", member, nil, 200)
	f.call("GET", "/auth/me", member, nil, 401)
}
func TestAtomicKVQueueAndCSRF(t *testing.T) {
	f := newFixture(t)
	owner := f.admin()
	f.call("POST", "/matters", "", M{"name": "csrf test"}, 403)
	f.call("POST", "/auth/register", "register", M{"name": "Intruder", "email": "intruder@example.test", "password": "Cedar!Trail7826"}, 403)
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := f.s.claim(context.Background(), "nonce", time.Minute)
			if err != nil {
				t.Error(err)
			}
			if ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatal("nonce accepted", wins, "times")
	}
	for i := 0; i < 5; i++ {
		f.call("POST", "/jobs", owner, M{"kind": "draft", "prompt": fmt.Sprint(i)}, 202)
	}
	f.call("POST", "/jobs", owner, M{"kind": "draft"}, 429)
	jobs := maps(f.call("GET", "/jobs", owner, nil, 200)["jobs"])
	f.call("DELETE", "/jobs/"+str(jobs[0], "job_id"), owner, nil, 200)
	f.s.StartWorkers()
	time.Sleep(1200 * time.Millisecond)
	cancelled := f.call("GET", "/jobs/"+str(jobs[0], "job_id"), owner, nil, 200)
	if str(cancelled, "status") != "cancelled" {
		t.Fatal(cancelled)
	}
}
