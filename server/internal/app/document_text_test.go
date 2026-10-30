package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Leave the normal upload/content routes intact and intercept only the OCR
// endpoint (or a deliberate page-image failure) in each independent fixture.
func routeOCR(t *testing.T, f *fixture, hook func(http.ResponseWriter, *http.Request) bool) {
	t.Helper()
	original, err := url.Parse(f.s.gm.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(original)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hook(w, r) {
			proxy.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(provider.Close)
	f.s.gm.URL = provider.URL
	f.s.gm.OCRTimeout = 5 * time.Second
}

func TestDocumentOCRPartialRetryCacheAndSharedConsumers(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	user := str(f.call("GET", "/auth/me", access, nil, 200), "id")
	doc := f.upload(access, "", "mixed_notice.pdf", string(ocrFile(t, "mixed_notice.pdf")))
	id := str(doc, "id")
	var calls atomic.Int32
	routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v1/ocr:document" {
			return false
		}
		var in M
		json.NewDecoder(r.Body).Decode(&in)
		call := calls.Add(1)
		if number(in, "endPage", -1) != 2 {
			t.Error("unexpected end page", in["endPage"])
		}
		if call == 1 {
			if number(in, "startPage", -1) != 1 {
				t.Error("native page should not be OCRed", in["startPage"])
			}
			writeJSON(w, 200, M{"pageCount": 2, "pages": []M{ocrPage(1, "📜 Notice is 30 days."), {"pageIndex": 2, "status": M{"code": 13}}}})
		} else {
			if call != 2 || number(in, "startPage", -1) != 2 {
				t.Error("successful OCR page must not be repeated", call, in["startPage"])
			}
			writeJSON(w, 200, M{"pageCount": 1, "pages": []M{ocrPage(2, "SEARCHABLE HEADER\nNotice is 30 days.\nPayment is due monthly.")}})
		}
		return true
	})
	failed := f.call("GET", "/documents/"+id+"/content", access, nil, 502)
	if !strings.Contains(str(failed, "detail"), "pages 3") {
		t.Fatal(failed)
	}
	partial, err := f.s.cachedDocumentText(context.Background(), doc)
	if err != nil || partial.Complete || len(partial.FailedPages) != 1 || partial.FailedPages[0] != 2 || partial.SHA256 == "" {
		t.Fatal(partial, err)
	}
	content := f.call("GET", "/documents/"+id+"/content", access, nil, 200)
	meta := object(content["extraction"])
	if str(meta, "method") != "native_and_goodmem_ocr" || meta["complete"] != true || number(meta, "page_count", 0) != 3 {
		t.Fatal(content)
	}
	text := str(content, "text")
	if !strings.Contains(text, "Native first page.") || !strings.Contains(text, "SEARCHABLE HEADER") {
		t.Fatal(text)
	}
	runReviewJob(t, f, access, str(doc, "ingest_job_id"))
	full, err := f.s.fullDocuments(context.Background(), user, M{"document_ids": []string{id}}, "notice")
	if err != nil || len(full) != 1 || full[0].Text != text || full[0].Coverage != "full text (GoodMem OCR)" {
		t.Fatal(full, err)
	}
	table := f.call("POST", "/review-tables", access, M{"name": "OCR review", "fields": []M{{"name": "Notice", "type": "duration"}}, "document_ids": []string{id}}, 201)
	preview := f.call("GET", "/review-tables/"+str(table, "id")+"/documents/"+id+"/text", access, nil, 200)
	if str(preview, "text") != text || number(preview, "length", -1) != byteToUTF16(text, len(text)) {
		t.Fatal(preview)
	}
	// Caching must not bypass access control, including another user on the
	// same application instance who can guess the document's ID.
	outsider := f.account(access, "ocr-outsider@example.test")
	f.call("GET", "/documents/"+id+"/content", outsider, nil, 404)
	if calls.Load() != 2 {
		t.Fatal("cached readers incurred OCR again", calls.Load())
	}
}

func TestDocumentOCRCoalescesAcrossServersAndCancelsWaiters(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	doc := f.upload(access, "", "scanned_notice.pdf", string(ocrFile(t, "scanned_notice.pdf")))
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var calls atomic.Int32
	routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v1/ocr:document" {
			return false
		}
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return true
		}
		writeJSON(w, 200, M{"pageCount": 1, "pages": []M{ocrPage(0, "Notice is 30 days.")}})
		return true
	})
	other := &Server{db: f.s.db, gm: f.s.gm, ocr: make(chan struct{}, 2)}
	results := make(chan error, 7)
	go func() { _, err := f.s.readDocumentText(context.Background(), doc); results <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("OCR did not start")
	}
	wait, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := other.readDocumentText(wait, doc); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("waiting request did not cancel", err)
	}
	for range 6 {
		go func() {
			result, err := other.readDocumentText(context.Background(), doc)
			if err == nil && result.Text != "Notice is 30 days." {
				err = errors.New("wrong cached text")
			}
			results <- err
		}()
	}
	once.Do(func() { close(release) })
	for range 7 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent OCR did not finish")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate OCR across application instances", calls.Load())
	}
	var lease *string
	if err := f.s.db.QueryRow(`SELECT lease_token FROM cc_document_text WHERE document_id=$1`, str(doc, "id")).Scan(&lease); err != nil || lease != nil {
		t.Fatal("lease retained", lease, err)
	}
}

func TestDocumentOCRDeletionRevisionAndMembershipChanges(t *testing.T) {
	for _, action := range []string{"delete", "revision", "membership", "cancel"} {
		t.Run(action, func(t *testing.T) {
			f := newFixture(t)
			access := f.admin()
			member := f.account(access, "ocr-member@example.test")
			memberID := str(f.call("GET", "/auth/me", member, nil, 200), "id")
			matter := str(f.call("POST", "/matters", access, M{"name": "OCR matter"}, 200), "id")
			f.call("POST", "/matters/"+matter+"/members", access, M{"email": "ocr-member@example.test"}, 200)
			doc := f.upload(access, matter, "scanned_notice.pdf", string(ocrFile(t, "scanned_notice.pdf")))
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/v1/ocr:document" {
					return false
				}
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
					return true
				}
				writeJSON(w, 200, M{"pageCount": 1, "pages": []M{ocrPage(0, "Notice is 30 days.")}})
				return true
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { _, _, err := f.s.documentText(ctx, memberID, str(doc, "id")); result <- err }()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("OCR did not start")
			}
			var err error
			switch action {
			case "delete":
				_, err = f.s.db.Exec(`DELETE FROM cc_objects WHERE kind='document' AND id=$1`, str(doc, "id"))
			case "revision":
				_, err = f.s.db.Exec(`UPDATE cc_objects SET data=jsonb_set(data,'{memory_id}','"replacement"') WHERE kind='document' AND id=$1`, str(doc, "id"))
			case "membership":
				f.call("DELETE", "/matters/"+matter+"/members/"+memberID, access, nil, 200)
			case "cancel":
				cancel()
			}
			if err != nil {
				t.Fatal(err)
			}
			once.Do(func() { close(release) })
			if err = <-result; err == nil {
				t.Fatal("changed document/access returned OCR text")
			}
			var completed, leased int
			err = f.s.db.QueryRow(`SELECT count(*) FILTER(WHERE data->>'complete'='true'),count(*) FILTER(WHERE lease_token IS NOT NULL) FROM cc_document_text WHERE document_id=$1`, str(doc, "id")).Scan(&completed, &leased)
			if err != nil || leased != 0 || action != "membership" && completed != 0 {
				t.Fatal("invalid cache/lease after change", completed, leased, err)
			}
		})
	}
}

func TestDocumentOCRRenderedPageGapsAndUnavailableBackend(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	// Legacy binary Word needs GoodMem-rendered pages; this controlled fixture
	// deliberately cannot be decoded by CaseCite's native text reader.
	doc := f.upload(access, "", "scan.doc", string([]byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1, 0}))
	var calls atomic.Int32
	var ready atomic.Bool
	routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.URL.Path == "/v1/ocr:document":
			calls.Add(1)
			var in M
			json.NewDecoder(r.Body).Decode(&in)
			if str(in, "format") != "AUTO" {
				t.Error("rendered image requires AUTO format", in)
			}
			writeJSON(w, 200, M{"pageCount": 1, "pages": []M{ocrPage(0, "Notice is 30 days.")}})
		case strings.HasSuffix(r.URL.Path, "/pages"):
			writeJSON(w, 200, M{"pageImages": []M{{"pageIndex": 0, "dpi": 72}, {"pageIndex": 0, "dpi": 144}, {"pageIndex": 2, "dpi": 144}}})
		case r.Method == "GET" && r.URL.Path == "/v1/memories/"+str(doc, "memory_id"):
			status := "PROCESSING"
			if ready.Load() {
				status = "COMPLETED"
			}
			writeJSON(w, 200, M{"memoryId": doc["memory_id"], "processingStatus": "COMPLETED", "pageImageStatus": status})
		default:
			return false
		}
		return true
	})
	f.call("GET", "/documents/"+str(doc, "id")+"/content", access, nil, 422)
	if calls.Load() != 0 {
		t.Fatal("OCR ran before page images were ready")
	}
	ready.Store(true)
	f.call("GET", "/documents/"+str(doc, "id")+"/content", access, nil, 502)
	if calls.Load() != 2 {
		t.Fatal("highest-DPI image must be used once per page", calls.Load())
	}
	cache, err := f.s.cachedDocumentText(context.Background(), doc)
	if err != nil || cache.Complete || len(cache.FailedPages) != 1 || cache.FailedPages[0] != 1 {
		t.Fatal("missing page was silently skipped", cache, err)
	}
}
