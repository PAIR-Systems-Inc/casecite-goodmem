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

func systemCall(t *testing.T, f *fixture, path, access, cookie, accept string, want int) (M, string) {
	t.Helper()
	r, _ := http.NewRequest("GET", f.http.URL+path, nil)
	if access != "" {
		r.Header.Set("Authorization", "Bearer "+access)
	}
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: "access_token", Value: cookie})
	}
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s: got %d want %d: %s", path, resp.StatusCode, want, data)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("diagnostics must not be cached")
	}
	out := M{}
	_ = json.Unmarshal(data, &out)
	return out, string(data)
}

func TestSystemHealthMetricsPrivacyReadinessAndCounts(t *testing.T) {
	f := newFixture(t)
	owner := f.admin()
	viewer := f.account(owner, "viewer@example.test")
	doc := f.upload(owner, "", "contract.txt", "Notice is 30 days.")
	f.s.cfg.Debug = false
	public, _ := systemCall(t, f, "/health", "", "", "", 200)
	if str(public, "status") != "healthy" || str(public, "started_at") == "" || public["components"] != nil || public["version"] != nil {
		t.Fatal(public)
	}
	for _, access := range []string{"", viewer, "expired"} {
		v, _ := systemCall(t, f, "/health", access, "", "", 200)
		if v["components"] != nil || v["started_at"] != public["started_at"] {
			t.Fatal(v)
		}
	}
	for _, cookie := range []bool{false, true} {
		bearer, c := owner, ""
		if cookie {
			bearer, c = "", owner
		}
		v, _ := systemCall(t, f, "/health", bearer, c, "", 200)
		if str(v, "version") != applicationVersion || object(object(v["components"])["database"])["healthy"] != true || object(object(v["components"])["llm"])["byok"] != true {
			t.Fatal(v)
		}
		m, _ := systemCall(t, f, "/metrics", bearer, c, "", 200)
		if number(object(m["documents"]), "total_count", 0) != 1 || number(object(m["documents"]), "indexed_count", 0) != 1 || m["redis"] != nil || str(object(m["jobs"]), "type") != "postgresql" {
			t.Fatal(m)
		}
	}
	systemCall(t, f, "/metrics", "", "", "", 401)
	systemCall(t, f, "/metrics", viewer, "", "", 403)
	_, text := systemCall(t, f, "/metrics", owner, "", "text/plain", 200)
	if !strings.Contains(text, "casecite_documents_total 1\n") || !strings.Contains(text, "casecite_goodmem_healthy 1\n") || strings.Contains(text, str(doc, "id")) {
		t.Fatal(text)
	}
	f.s.cfg.RequireMFA = true
	security, _ := systemCall(t, f, "/health", owner, "", "", 200)
	if security["components"] != nil {
		t.Fatal(security)
	}
	systemCall(t, f, "/metrics", owner, "", "", 403)
	f.s.cfg.RequireMFA = false
	routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/v1/spaces/space" {
			http.Error(w, "secret upstream hostname", 503)
			return true
		}
		return false
	})
	v, text := systemCall(t, f, "/health", "", "", "", 200)
	if str(v, "status") != "degraded" || strings.Contains(text, "secret") {
		t.Fatal(v)
	}
	v, text = systemCall(t, f, "/ready", "", "", "", 503)
	if v["ready"] != false || len(stringsOf(v["issues"])) != 1 || strings.Contains(text, "secret") {
		t.Fatal(v)
	}
	if err := f.s.db.Close(); err != nil {
		t.Fatal(err)
	}
	systemCall(t, f, "/health", owner, "", "", 200)
	systemCall(t, f, "/metrics", owner, "", "", 503)
}

func TestReindexOwnerScopeDeduplicationAndStoredOriginal(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	other := f.account(access, "other@example.test")
	user := str(f.call("GET", "/auth/me", access, nil, 200), "id")
	private := f.upload(other, "", "private.txt", "Another user's document.")
	matter := str(f.call("POST", "/matters", access, M{"name": "Client A"}, 200), "id")
	doc := f.upload(access, matter, "notice.txt", "Notice is 30 days.")
	id, original := str(doc, "id"), str(doc, "memory_id")
	if _, err := f.s.db.Exec(`UPDATE cc_objects SET data=data || '{"folder_path":"/Client A","metadata":{"classification":"contract"}}'::jsonb WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	first := str(f.call("POST", "/settings/reindex?async=true", access, nil, 200), "job_id")
	if again := str(f.call("POST", "/settings/reindex?async=true", access, nil, 200), "job_id"); again != first {
		t.Fatal("duplicate rebuild", first, again)
	}
	awaitChatFixture(t, runChatFixtureJob(t, f, first))
	job := f.call("GET", "/jobs/"+first, access, nil, 200)
	if str(job, "status") != "completed" || number(object(job["result"]), "reindexed", 0) != 1 || len(stringsOf(object(job["result"])["errors"])) != 0 {
		t.Fatal(job)
	}
	current := f.call("GET", "/documents/"+id, access, nil, 200)
	if str(current, "memory_id") != original || documentSearchMemory(current) == original || str(current, "matter_id") != matter || str(current, "folder_path") != "/Client A" || str(object(current["metadata"]), "classification") != "contract" {
		t.Fatal(current)
	}
	if got := f.call("GET", "/documents/"+str(private, "id"), other, nil, 200); documentSearchMemory(got) != str(private, "memory_id") {
		t.Fatal("another owner's document rebuilt", got)
	}
	bytes, err := f.s.gm.Content(context.Background(), original)
	if err != nil || string(bytes) != "Notice is 30 days." {
		t.Fatal(string(bytes), err)
	}
	// A second rebuild retires only the old derived index. The original and
	// current index remain protected from the delayed garbage collector.
	second := str(f.call("POST", "/settings/reindex?async=true", access, nil, 200), "job_id")
	awaitChatFixture(t, runChatFixtureJob(t, f, second))
	if _, err = f.s.db.Exec(`UPDATE cc_kv SET value=value || '{"not_before":"2000-01-01T00:00:00Z"}'::jsonb WHERE key LIKE 'memory-delete:%'`); err != nil {
		t.Fatal(err)
	}
	f.s.cleanupDocumentOCR(context.Background())
	if _, err = f.s.gm.Content(context.Background(), documentSearchMemory(current)); err == nil {
		t.Fatal("retired index retained")
	}
	if _, err = f.s.gm.Content(context.Background(), original); err != nil {
		t.Fatal("original removed", err)
	}
	var results int
	if err = f.s.db.QueryRow(`SELECT count(*) FROM cc_objects WHERE kind='result' AND owner_id=$1`, user).Scan(&results); err != nil || results != 0 {
		t.Fatal("rebuild is not a legal analysis", results, err)
	}
}

func TestReindexFailureKeepsWorkingIndexAndContinues(t *testing.T) {
	for _, failure := range []string{"upload", "empty", "missing"} {
		t.Run(failure, func(t *testing.T) {
			f := newFixture(t)
			access := f.admin()
			badDoc := f.upload(access, "", "bad.txt", "Unreadable replacement.")
			goodDoc := f.upload(access, "", "good.txt", "Notice is 30 days.")
			var newIndex atomic.Value
			routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
				if failure == "missing" && r.URL.Path == "/v1/memories/"+str(badDoc, "memory_id")+"/content" {
					http.NotFound(w, r)
					return true
				}
				if r.Method == "POST" && r.URL.Path == "/v1/memories" {
					raw, _ := io.ReadAll(r.Body)
					r.Body = io.NopCloser(bytes.NewReader(raw))
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
						return true
					}
					defer r.MultipartForm.RemoveAll()
					r.Body = io.NopCloser(bytes.NewReader(raw))
					var in M
					_ = json.Unmarshal([]byte(r.FormValue("request")), &in)
					if str(object(in["metadata"]), "filename") == "bad.txt" {
						newIndex.Store(str(in, "memoryId"))
						if failure == "upload" {
							http.Error(w, "private provider diagnostic", 503)
							return true
						}
					}
				}
				if failure == "empty" && r.URL.Path == "/v1/memories:retrieve" && newIndex.Load() != nil {
					var in M
					_ = json.NewDecoder(r.Body).Decode(&in)
					if strings.Contains(string(jsonBytes(in)), newIndex.Load().(string)) {
						w.Header().Set("Content-Type", "application/x-ndjson")
						w.WriteHeader(200)
						return true
					}
					r.Body = io.NopCloser(strings.NewReader(string(jsonBytes(in))))
				}
				return false
			})
			job := str(f.call("POST", "/settings/reindex?async=true", access, nil, 200), "job_id")
			awaitChatFixture(t, runChatFixtureJob(t, f, job))
			result := object(f.call("GET", "/jobs/"+job, access, nil, 200)["result"])
			if number(result, "reindexed", -1) != 1 || len(stringsOf(result["errors"])) != 1 || strings.Contains(string(jsonBytes(result)), "private provider") {
				t.Fatal(result)
			}
			if got := f.call("GET", "/documents/"+str(badDoc, "id"), access, nil, 200); str(got, "status") != "indexed" || documentSearchMemory(got) != str(badDoc, "memory_id") {
				t.Fatal("failed rebuild destroyed old index", got)
			}
			if got := f.call("GET", "/documents/"+str(goodDoc, "id"), access, nil, 200); documentSearchMemory(got) == str(goodDoc, "memory_id") {
				t.Fatal("rebuild stopped at first failure", got)
			}
		})
	}
}

func TestReindexCancellationAndDeletionCannotPublish(t *testing.T) {
	for _, action := range []string{"cancel", "delete", "disable"} {
		t.Run(action, func(t *testing.T) {
			f := newFixture(t)
			access := f.admin()
			user := str(f.call("GET", "/auth/me", access, nil, 200), "id")
			doc := f.upload(access, "", "notice.txt", "Notice is 30 days.")
			entered, release := make(chan struct{}), make(chan struct{})
			var once atomic.Bool
			routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method == "POST" && r.URL.Path == "/v1/memories" && once.CompareAndSwap(false, true) {
					close(entered)
					<-release
				}
				return false
			})
			job := str(f.call("POST", "/settings/reindex?async=true", access, nil, 200), "job_id")
			done := runChatFixtureJob(t, f, job)
			<-entered
			switch action {
			case "cancel":
				f.call("DELETE", "/jobs/"+job, access, nil, 200)
			case "delete":
				f.call("DELETE", "/documents/"+str(doc, "id"), access, nil, 200)
			case "disable":
				if _, err := f.s.db.Exec(`UPDATE cc_users SET active=false WHERE id=$1`, user); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			awaitChatFixture(t, done)
			if action == "delete" {
				f.call("GET", "/documents/"+str(doc, "id"), access, nil, 404)
			} else {
				var raw []byte
				if err := f.s.db.QueryRow(`SELECT data FROM cc_objects WHERE id=$1`, str(doc, "id")).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var current M
				_ = json.Unmarshal(raw, &current)
				if documentSearchMemory(current) != str(doc, "memory_id") {
					t.Fatal("late rebuild published", current)
				}
			}
			if _, err := f.s.db.Exec(`UPDATE cc_kv SET value=value || '{"not_before":"2000-01-01T00:00:00Z"}'::jsonb WHERE key LIKE 'memory-delete:%'`); err != nil {
				t.Fatal(err)
			}
			f.s.cleanupDocumentOCR(context.Background())
			f.mu.Lock()
			count := len(f.memories)
			f.mu.Unlock()
			want := 1
			if action == "delete" {
				want = 0
			}
			if count != want {
				t.Fatal("orphaned rebuild memory", count, want)
			}
		})
	}
}

func TestReindexReusesGoodMemOCRAndPreservesPages(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	doc := f.upload(access, "", "scan.pdf", string(ocrFile(t, "scanned_notice.pdf")))
	var calls atomic.Int32
	routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v1/ocr:document" {
			return false
		}
		calls.Add(1)
		writeJSON(w, 200, M{"pageCount": 1, "pages": []M{ocrPage(0, "Notice is 30 days.")}})
		return true
	})
	runReviewJob(t, f, access, str(doc, "ingest_job_id"))
	job := str(f.call("POST", "/settings/reindex?async=true", access, nil, 200), "job_id")
	awaitChatFixture(t, runChatFixtureJob(t, f, job))
	if result := object(f.call("GET", "/jobs/"+job, access, nil, 200)["result"]); number(result, "reindexed", 0) != 1 {
		t.Fatal(result)
	}
	if calls.Load() != 1 {
		t.Fatal("rebuilding unnecessarily repeated OCR", calls.Load())
	}
	current := f.call("GET", "/documents/"+str(doc, "id"), access, nil, 200)
	if str(current, "memory_id") != str(doc, "memory_id") || documentSearchMemory(current) == documentSearchMemory(doc) {
		t.Fatal(current)
	}
	if content, err := f.s.gm.Content(context.Background(), documentSearchMemory(current)); err != nil || !strings.Contains(string(content), "Notice is 30 days.") {
		t.Fatal(string(content), err)
	}
	pages, err := f.s.gm.Pages(context.Background(), str(doc, "memory_id"))
	if err != nil || len(pages) != 1 {
		t.Fatal(pages, err)
	}
}

func TestClearDocumentsOwnScopePartialFailuresAndPermissions(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	other := f.account(access, "other@example.test")
	private := f.upload(other, "", "private.txt", "Keep private original.")
	badDoc := f.upload(access, "", "bad.txt", "Provider deletion will fail.")
	f.upload(access, "", "good.txt", "Can be removed.")
	routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "DELETE" && strings.Contains(r.URL.Path, str(badDoc, "memory_id")) {
			http.Error(w, "secret", 503)
			return true
		}
		return false
	})
	partial := f.call("POST", "/settings/clear", access, nil, 200)
	if str(partial, "status") != "partial" || number(partial, "cleared_documents", 0) != 1 || number(partial, "failed_documents", 0) != 1 {
		t.Fatal(partial)
	}
	f.call("GET", "/documents/"+str(private, "id"), other, nil, 200)
	f.call("GET", "/documents/"+str(badDoc, "id"), access, nil, 200)
	failed := f.call("POST", "/settings/clear", access, nil, 502)
	if strings.Contains(str(failed, "detail"), "secret") {
		t.Fatal(failed)
	}
	f.call("POST", "/settings/reindex", "expired", nil, 401)
	f.call("POST", "/settings/clear", "expired", nil, 401)
	otherID := str(f.call("GET", "/auth/me", other, nil, 200), "id")
	if _, err := f.s.db.Exec(`UPDATE cc_users SET role='viewer' WHERE id=$1`, otherID); err != nil {
		t.Fatal(err)
	}
	f.call("POST", "/settings/reindex", other, nil, 403)
	f.call("POST", "/settings/clear", other, nil, 403)
}
