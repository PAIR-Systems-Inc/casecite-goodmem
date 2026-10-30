package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLegacyExcelTextIndexRetrySearchAndRebuild(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	user := str(f.call("GET", "/auth/me", access, nil, 200), "id")
	var source extractionFixture
	for _, item := range extractionFixtures(t) {
		if item.Name == "biff2-notice.xls" {
			source = item
		}
	}
	if source.Improved == nil {
		t.Fatal("missing native reference")
	}
	doc := f.upload(access, "", source.Name, string(source.Content))
	id, original, index, first := str(doc, "id"), str(doc, "memory_id"), str(doc, "index_memory_id"), str(doc, "ingest_job_id")
	if doc["text_index_required"] != true || doc["ocr_required"] == true || index == "" || index == original || first == "" {
		t.Fatal(doc)
	}
	var failUpload atomic.Bool
	failUpload.Store(true)
	var creates atomic.Int32
	routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/v1/ocr:document" {
			t.Error("native Excel invoked OCR")
			http.Error(w, "not an OCR document", 400)
			return true
		}
		if r.Method == "GET" && r.URL.Path == "/v1/memories/"+original {
			writeJSON(w, 200, M{"processingStatus": "FAILED"})
			return true
		}
		if r.Method != "POST" || r.URL.Path != "/v1/memories" {
			return false
		}
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return true
		}
		defer r.MultipartForm.RemoveAll()
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var input M
		if err := json.Unmarshal([]byte(r.FormValue("request")), &input); err != nil {
			t.Error(err)
			return true
		}
		metadata := object(input["metadata"])
		if input["extractPageImages"] != false || str(input, "contentType") != "text/plain" || str(metadata, "text_extraction") != "native" || str(metadata, "casecite_source_memory_id") != original {
			t.Error("wrong native index metadata", input)
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return true
		}
		text, err := io.ReadAll(file)
		file.Close()
		if err != nil || string(text) != *source.Improved {
			t.Error("index must contain the complete native text", err)
		}
		creates.Add(1)
		if failUpload.Load() {
			http.Error(w, "Embedding service unavailable", 503)
			return true
		}
		return false
	})
	if current := f.call("GET", "/documents/"+id, access, nil, 200); str(current, "status") != "processing" {
		t.Fatal(current)
	}
	claimReviewJob(t, f, first)()
	if current := f.call("GET", "/documents/"+id, access, nil, 200); str(current, "status") != "failed" {
		t.Fatal(current)
	}
	other := f.account(access, "legacy-outsider@example.test")
	f.call("POST", "/documents/"+id+"/retry-index", other, nil, 404)
	failUpload.Store(false)
	second := str(f.call("POST", "/documents/"+id+"/retry-index", access, nil, 200), "job_id")
	if second == first || second == "" {
		t.Fatal("no new retry")
	}
	runReviewJob(t, f, access, second)
	current := f.call("GET", "/documents/"+id, access, nil, 200)
	if str(current, "status") != "indexed" || str(current, "memory_id") != original || documentSearchMemory(current) != index {
		t.Fatal(current)
	}
	content := f.call("GET", "/documents/"+id+"/content", access, nil, 200)
	if str(content, "text") != *source.Improved || str(object(content["extraction"]), "method") != "native" {
		t.Fatal(content)
	}
	answer, err := f.s.research(context.Background(), user, M{"document_ids": []string{id}, "prompt": "What notice is required?"})
	if err != nil {
		t.Fatal(err)
	}
	citations := maps(answer["citations"])
	if len(citations) != 1 || str(citations[0], "document_id") != id {
		t.Fatal(answer)
	}
	f.mu.Lock()
	filters := append([]string{}, f.retrieved...)
	f.mu.Unlock()
	for _, filter := range filters {
		if strings.Contains(filter, original) || !strings.Contains(filter, index) {
			t.Fatal("search used the unreadable original", filter)
		}
	}
	rebuild := str(f.call("POST", "/settings/reindex?async=true", access, nil, 200), "job_id")
	awaitChatFixture(t, runChatFixtureJob(t, f, rebuild))
	job := f.call("GET", "/jobs/"+rebuild, access, nil, 200)
	if str(job, "status") != "completed" || number(object(job["result"]), "reindexed", 0) != 1 {
		t.Fatal(job)
	}
	current = f.call("GET", "/documents/"+id, access, nil, 200)
	if str(current, "memory_id") != original || documentSearchMemory(current) == index || creates.Load() != 3 {
		t.Fatal(current, creates.Load())
	}
	originalBytes, err := f.s.gm.Content(context.Background(), original)
	if err != nil || !bytes.Equal(originalBytes, source.Content) {
		t.Fatal("original bytes changed", err)
	}
	f.call("DELETE", "/documents/"+id, access, nil, 200)
	if _, err = f.s.gm.Content(context.Background(), original); err == nil {
		t.Fatal("original retained after deletion")
	}
	if _, err = f.s.gm.Content(context.Background(), documentSearchMemory(current)); err == nil {
		t.Fatal("derived index retained after deletion")
	}
}
