package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDocumentOCRIndexRetrySearchAndPublication(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	user := str(f.call("GET", "/auth/me", access, nil, 200), "id")
	doc := f.upload(access, "", "scan.pdf", string(ocrFile(t, "scanned_notice.pdf")))
	id, original, index, first := str(doc, "id"), str(doc, "memory_id"), str(doc, "index_memory_id"), str(doc, "ingest_job_id")
	if original == index || index == "" || first == "" || str(doc, "status") != "processing" {
		t.Fatal(doc)
	}
	var calls atomic.Int32
	routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/v1/memories/"+original {
			writeJSON(w, 200, M{"processingStatus": "FAILED", "pageImageStatus": "COMPLETED"})
			return true
		}
		if r.URL.Path != "/v1/ocr:document" {
			return false
		}
		if calls.Add(1) == 1 {
			writeJSON(w, 200, M{"pageCount": 1, "pages": []M{{"pageIndex": 0, "status": M{"code": 13}}}})
		} else {
			writeJSON(w, 200, M{"pageCount": 1, "pages": []M{ocrPage(0, "SCANNED AGREEMENT\nNotice is 30 days.")}})
		}
		return true
	})
	// Original-file ingestion failure must not override the queued OCR status.
	if got := f.call("GET", "/documents/"+id, access, nil, 200); str(got, "status") != "processing" {
		t.Fatal(got)
	}
	claimReviewJob(t, f, first)()
	failed := f.call("GET", "/documents/"+id, access, nil, 200)
	if str(failed, "status") != "failed" || !strings.Contains(str(failed, "error"), "pages 1") {
		t.Fatal(failed)
	}
	var jobs int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM cc_jobs WHERE kind='document.ocr_index'`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatal(jobs, err)
	}
	// Concurrent explicit retries reserve one new job, preserving OCR cache.
	ids := make(chan string, 6)
	for range 6 {
		go func() {
			result, err := f.s.enqueueDocumentOCR(context.Background(), user, id, true)
			if err != nil {
				ids <- err.Error()
			} else {
				ids <- str(result, "job_id")
			}
		}()
	}
	second := <-ids
	for range 5 {
		if got := <-ids; got != second {
			t.Fatal("duplicate retry job", second, got)
		}
	}
	if second == "" || second == first {
		t.Fatal("missing retry", second)
	}
	result := runReviewJob(t, f, access, second)
	if str(result, "index_memory_id") != index {
		t.Fatal(result)
	}
	indexed := f.call("GET", "/documents/"+id, access, nil, 200)
	if str(indexed, "status") != "indexed" || str(indexed, "memory_id") != original || indexed["error"] != nil {
		t.Fatal(indexed)
	}
	if _, err := f.s.gm.Content(context.Background(), original); err != nil {
		t.Fatal("original lost", err)
	}
	answer, err := f.s.research(context.Background(), user, M{"document_ids": []string{id}, "prompt": "What notice is required?"})
	if err != nil {
		t.Fatal(err)
	}
	citations := maps(answer["citations"])
	if len(citations) != 1 || str(citations[0], "document_id") != id || !strings.Contains(str(citations[0], "passage"), "Notice is 30 days.") {
		t.Fatal(answer)
	}
	f.mu.Lock()
	filters := append([]string{}, f.retrieved...)
	f.mu.Unlock()
	for _, filter := range filters {
		if strings.Contains(filter, original) || !strings.Contains(filter, index) {
			t.Fatal("search must use the complete OCR index only", filter)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("unnecessary repeated OCR", calls.Load())
	}
	var saved int
	if err = f.s.db.QueryRow(`SELECT count(*) FROM cc_objects WHERE kind='result'`).Scan(&saved); err != nil || saved != 0 {
		t.Fatal("ingestion polluted user analyses", saved, err)
	}
	if retry := f.call("POST", "/documents/"+id+"/retry-ocr", access, nil, 200); str(retry, "job_id") != second {
		t.Fatal("successful document reindexed unnecessarily", retry)
	}
}

func TestDocumentOCRIndexDeleteDuringRemoteCreation(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	doc := f.upload(access, "", "scan.pdf", string(ocrFile(t, "scanned_notice.pdf")))
	id, index := str(doc, "id"), str(doc, "index_memory_id")
	creating, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/v1/ocr:document" {
			writeJSON(w, 200, M{"pageCount": 1, "pages": []M{ocrPage(0, "Notice is 30 days.")}})
			return true
		}
		if r.Method != "POST" || r.URL.Path != "/v1/memories" {
			return false
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return false
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		if !bytes.Contains(data, []byte(index)) {
			return false
		}
		close(creating)
		<-release
		// Simulate a write committed remotely after the delete returned.
		f.mu.Lock()
		f.memories[index] = []byte("Notice is 30 days.")
		f.mu.Unlock()
		writeJSON(w, 200, M{"memoryId": index})
		return true
	})
	run := claimReviewJob(t, f, str(doc, "ingest_job_id"))
	done := make(chan struct{})
	go func() { defer close(done); run() }()
	select {
	case <-creating:
	case <-time.After(4 * time.Second):
		t.Fatal("index creation did not start")
	}
	f.call("DELETE", "/documents/"+id, access, nil, 200)
	once.Do(func() { close(release) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled index job did not stop")
	}
	f.call("GET", "/documents/"+id, access, nil, 404)
	var cached int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM cc_document_text WHERE document_id=$1`, id).Scan(&cached); err != nil || cached != 0 {
		t.Fatal("OCR cache resurrected", cached, err)
	}
	if _, err := f.s.db.Exec(`UPDATE cc_kv SET value=jsonb_set(value,'{not_before}',to_jsonb((now()-interval '1 minute')::text)) WHERE starts_with(key,'memory-delete:')`); err != nil {
		t.Fatal(err)
	}
	f.s.cleanupDocumentOCR(context.Background())
	f.mu.Lock()
	_, left := f.memories[index]
	f.mu.Unlock()
	if left {
		t.Fatal("late OCR memory escaped durable cleanup")
	}
}

func TestDocumentOCRAccountDeletionPurgesBothMemories(t *testing.T) {
	f := newFixture(t)
	admin := f.admin()
	owner := f.account(admin, "scans-owner@example.test")
	user := str(f.call("GET", "/auth/me", owner, nil, 200), "id")
	doc := f.upload(owner, "", "scan.pdf", string(ocrFile(t, "scanned_notice.pdf")))
	routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v1/ocr:document" {
			return false
		}
		writeJSON(w, 200, M{"pageCount": 1, "pages": []M{ocrPage(0, "Notice is 30 days.")}})
		return true
	})
	runReviewJob(t, f, owner, str(doc, "ingest_job_id"))
	f.call("DELETE", "/admin/users/"+user, admin, nil, 200)
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range []string{str(doc, "memory_id"), str(doc, "index_memory_id")} {
		if _, left := f.memories[id]; left {
			t.Fatal("account purge retained memory", id)
		}
	}
}

func TestDocumentOCRRedlinesUseCanonicalText(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	user := str(f.call("GET", "/auth/me", access, nil, 200), "id")
	doc := f.upload(access, "", "scan.pdf", string(ocrFile(t, "scanned_notice.pdf")))
	routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v1/ocr:document" {
			return false
		}
		writeJSON(w, 200, M{"pageCount": 1, "pages": []M{ocrPage(0, "📜 Notice is 30 days.\nPayment is due monthly.")}})
		return true
	})
	id := uuid()
	err := f.s.saveObject(context.Background(), user, "analysis", id, "", M{"document_id": doc["id"], "redlines": []M{{"ref": "notice", "kind": "replace", "original_text": "Notice is 30 days.", "proposed_text": "Notice is 45 days.", "span_start": 3, "span_end": 21}}})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", f.http.URL+"/api/v1/contract-analysis/analyses/"+id+"/redline-export", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || response.Header.Get("X-CaseCite-Applied-Edits") != "1" {
		t.Fatal(response.StatusCode, response.Header, err)
	}
	xml := zipParts(t, content)["word/document.xml"]
	if !strings.HasPrefix(revisionView(t, xml, true), "📜 Notice is 45 days.\nPayment is due monthly.") || !strings.HasPrefix(revisionView(t, xml, false), "📜 Notice is 30 days.\nPayment is due monthly.") {
		t.Fatal("OCR redline changed the wrong range or lost text")
	}
}
