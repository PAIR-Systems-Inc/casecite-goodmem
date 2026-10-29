package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const reviewSource = "📜 This Agreement is effective as of January 5, 2026.\nGOVERNING LAW. This Agreement shall be governed by the laws of the State of Delaware.\nPayment is due within 30 days."

var reviewTestFields = []M{{"name": "Governing law", "type": "text"}, {"name": "Effective date", "type": "date"}, {"name": "Liability cap", "type": "money"}}

func reviewFixture(t *testing.T, hook func() int) *fixture {
	f := newFixture(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mode := 0
		if hook != nil {
			mode = hook()
		}
		if mode == 2 {
			http.Error(w, "Provider failure", 503)
			return
		}
		law := "Delaware"
		quote := "This Agreement shall be governed by the laws of the State of Delaware."
		if mode == 1 {
			law, quote = "Texas", "This Agreement is governed by Texas law."
		}
		answer := M{"fields": M{"governing_law": M{"value": law, "quote": quote}, "effective_date": M{"value": "2026-01-05", "quote": "This Agreement is effective as of January 5, 2026."}, "liability_cap": M{"value": "USD 1 million", "quote": "Liability is capped at one million dollars."}}}
		writeJSON(w, 200, M{"choices": []M{{"message": M{"content": string(jsonBytes(answer))}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(provider.Close)
	f.s.cfg.LLMURL = provider.URL
	f.s.cfg.LLMUtilityModel = "test-utility"
	return f
}

// Exercise the real queue finalizer without depending on the scheduler tick.
func claimReviewJob(t *testing.T, f *fixture, id string) func() {
	t.Helper()
	var user, kind string
	var input []byte
	lease := token()
	err := f.s.db.QueryRow(`UPDATE cc_jobs SET status='running',lease_token=$2,lease_until=now()+interval '30 seconds' WHERE id=$1 AND status='pending' RETURNING user_id,kind,input`, id, lease).Scan(&user, &kind, &input)
	if err != nil {
		t.Fatal(err)
	}
	return func() { f.s.runJob(id, user, kind, lease, input) }
}
func runReviewJob(t *testing.T, f *fixture, access, id string) M {
	t.Helper()
	claimReviewJob(t, f, id)()
	job := f.call("GET", "/jobs/"+id, access, nil, 200)
	if str(job, "status") != "completed" {
		t.Fatal(job)
	}
	return object(job["result"])
}
func getReviewCell(t *testing.T, f *fixture, access, table, doc, key string) M {
	t.Helper()
	for _, c := range maps(f.call("GET", "/review-tables/"+table, access, nil, 200)["cells"]) {
		if str(c, "document_id") == doc && str(c, "field_key") == key {
			return c
		}
	}
	t.Fatalf("Missing cell %s/%s", doc, key)
	return nil
}

func TestReviewTablesAPIRerunsAndExports(t *testing.T) {
	var mode atomic.Int32
	f := reviewFixture(t, func() int { return int(mode.Load()) })
	owner := f.admin()
	member := f.account(owner, "review-member@example.test")
	outsider := f.account(owner, "review-stranger@example.test")
	memberID := str(f.call("GET", "/auth/me", member, nil, 200), "id")
	mid := str(f.call("POST", "/matters", owner, M{"name": "Diligence"}, 200), "id")
	f.call("POST", "/matters/"+mid+"/members", owner, M{"email": "review-member@example.test"}, 200)
	doc := str(f.upload(owner, mid, "agreement.txt", reviewSource), "id")
	private := str(f.upload(owner, "", "private.txt", reviewSource), "id")
	f.call("POST", "/review-tables", owner, M{"name": " ", "fields": reviewTestFields}, 422)
	f.call("POST", "/review-tables", owner, M{"name": "Invalid", "fields": []M{{"name": "Law"}, {"name": "Law!"}}}, 422)
	f.call("POST", "/review-tables", member, M{"name": "Private source", "fields": reviewTestFields, "document_ids": []string{private}}, 404)
	table := f.call("POST", "/review-tables", owner, M{"name": "Diligence table", "matter_id": mid, "fields": reviewTestFields}, 201)
	tid := str(table, "id")
	base := "/review-tables/" + tid
	if number(table, "document_count", 0) != 1 || len(maps(table["fields"])) != 3 {
		t.Fatal(table)
	}
	f.call("GET", base, outsider, nil, 404)
	f.call("PATCH", base, member, M{"name": "Other"}, 404)
	f.call("DELETE", base, member, nil, 404)
	f.call("POST", base+"/documents", member, M{"document_ids": []string{private}}, 404)
	// Saved schemas, including the exact four original built-ins.
	schemas := maps(f.call("GET", "/review-tables/schemas", member, nil, 200)["schemas"])
	if len(schemas) != 4 || len(maps(schemas[0]["fields"])) != 12 {
		t.Fatal(schemas)
	}
	f.call("PATCH", "/review-tables/schemas/builtin:nda", owner, M{"name": "Changed"}, 404)
	schema := f.call("POST", "/review-tables/schemas", owner, M{"name": "Firm diligence", "matter_id": mid, "fields": reviewTestFields}, 201)
	sid := str(schema, "id")
	if len(maps(f.call("GET", "/review-tables/schemas", member, nil, 200)["schemas"])) != 5 {
		t.Fatal("shared schema missing")
	}
	f.call("PATCH", "/review-tables/schemas/"+sid, member, M{"name": "Changed"}, 404)
	f.call("PATCH", "/review-tables/schemas/"+sid, owner, M{"matter_id": nil}, 200)
	if len(maps(f.call("GET", "/review-tables/schemas", member, nil, 200)["schemas"])) != 4 {
		t.Fatal("private schema remained shared")
	}
	f.call("DELETE", "/review-tables/schemas/"+sid, owner, nil, 200)

	// Human review may precede the first extraction; it is never overwritten.
	f.call("PATCH", base+"/cells/"+doc+"/effective_date", member, M{"override_value": "2026-02-01", "review_status": "approved", "note": "Signed amendment"}, 200)
	f.call("PATCH", base+"/cells/"+doc+"/effective_date", member, M{}, 422)
	f.call("PATCH", base+"/cells/"+private+"/governing_law", member, M{"review_status": "approved"}, 404)
	f.call("POST", base+"/run", member, M{"field_keys": []string{"unknown"}}, 400)
	started := f.call("POST", base+"/run", member, nil, 202)
	f.call("POST", base+"/run", owner, M{}, 409)
	result := runReviewJob(t, f, member, str(started, "job_id"))
	if number(result, "extracted", 0) != 2 || number(result, "unverified", 0) != 1 {
		t.Fatal(result)
	}
	law := getReviewCell(t, f, member, tid, doc, "governing_law")
	start, ok := byteOffsetUTF16(reviewSource, number(law, "span_start", -1))
	if !ok || !strings.HasPrefix(reviewSource[start:], str(law, "quote")) {
		t.Fatal(law)
	}
	date := getReviewCell(t, f, member, tid, doc, "effective_date")
	if str(date, "review_status") != "approved" || str(date, "effective_value") != "2026-02-01" || str(date, "value") != "2026-01-05" {
		t.Fatal(date)
	}
	text := f.call("GET", base+"/documents/"+doc+"/text", member, nil, 200)
	if str(text, "text") != reviewSource || number(text, "length", 0) != byteToUTF16(reviewSource, len(reviewSource)) {
		t.Fatal(text)
	}
	bulk := f.call("POST", base+"/cells/bulk", member, M{"cells": []M{{"document_id": doc, "field_key": "governing_law"}, {"document_id": doc, "field_key": "governing_law"}, {"document_id": "foreign", "field_key": "governing_law"}}, "review_status": "approved"}, 200)
	if number(bulk, "updated", 0) != 1 {
		t.Fatal(bulk)
	}
	started = f.call("POST", base+"/run", member, M{"document_ids": []string{doc}, "field_keys": []string{"governing_law"}}, 202)
	result = runReviewJob(t, f, member, str(started, "job_id"))
	if number(result, "changed", -1) != 0 || str(getReviewCell(t, f, member, tid, doc, "governing_law"), "review_status") != "approved" {
		t.Fatal(result)
	}
	mode.Store(1)
	started = f.call("POST", base+"/run", member, M{"field_keys": []string{"governing_law"}}, 202)
	result = runReviewJob(t, f, member, str(started, "job_id"))
	if number(result, "changed", 0) != 1 {
		t.Fatal(result)
	}
	law = getReviewCell(t, f, member, tid, doc, "governing_law")
	if str(law, "review_status") != "pending" || str(law, "status") != "unverified" {
		t.Fatal(law)
	}
	if str(getReviewCell(t, f, member, tid, doc, "effective_date"), "review_status") != "approved" {
		t.Fatal("scoped rerun changed another column")
	}

	for _, format := range []string{"xlsx", "csv", "json"} {
		req, _ := http.NewRequest("GET", f.http.URL+"/api/v1"+base+"/export?format="+format+"&include_quotes=false&reviewed_only=true&fields=effective_date", nil)
		req.Header.Set("Authorization", "Bearer "+member)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		content, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || len(content) == 0 || !strings.Contains(resp.Header.Get("Content-Disposition"), "."+format) {
			t.Fatal(resp.StatusCode, string(content))
		}
		if format == "json" {
			out := M{}
			json.Unmarshal(content, &out)
			cells := maps(out["cells"])
			if len(cells) != 1 || str(cells[0], "effective_value") != "2026-02-01" || cells[0]["quote"] != nil {
				t.Fatal(out)
			}
		}
	}
	f.call("GET", base+"/export?status=bogus", member, nil, 400)
	f.call("GET", base+"/export?fields=bogus", member, nil, 400)
	f.call("GET", base+"/export", outsider, nil, 404)
	runs := maps(f.call("GET", base+"/runs", member, nil, 200)["runs"])
	if len(runs) != 3 || str(runs[0], "status") != "completed" || runs[0]["finished_at"] == nil || len(stringsOf(object(runs[0]["scope"])["field_keys"])) != 1 {
		t.Fatal(runs)
	}
	if f.call("GET", base, member, nil, 200)["last_run_at"] == nil {
		t.Fatal("last run not recorded")
	}
	f.call("GET", base+"/runs?limit=0", member, nil, 400)
	// A member running a table with an owner's private source gets error cells,
	// not a silent omission or the private source's content.
	f.call("POST", base+"/documents", owner, M{"document_ids": []string{private}}, 200)
	estimate := f.call("GET", base+"/estimate", member, nil, 200)
	if number(estimate, "documents", 0) != 1 || number(estimate, "llm_calls", 0) != 1 {
		t.Fatal(estimate)
	}
	started = f.call("POST", base+"/run", member, M{"document_ids": []string{private}}, 202)
	result = runReviewJob(t, f, member, str(started, "job_id"))
	if number(result, "errors", 0) != 3 {
		t.Fatal(result)
	}
	mode.Store(2)
	started = f.call("POST", base+"/run", owner, M{"document_ids": []string{doc}}, 202)
	result = runReviewJob(t, f, owner, str(started, "job_id"))
	if number(result, "errors", 0) != 3 {
		t.Fatal(result)
	}
	// Adding and running only new documents returns the job in the same shape
	// as the upstream API. Duplicate IDs do not re-run existing rows.
	newDoc := str(f.upload(owner, mid, "second.txt", reviewSource), "id")
	added := f.call("POST", base+"/documents", owner, M{"document_ids": []string{doc, newDoc, newDoc}, "run": true}, 200)
	if len(stringsOf(added["added"])) != 1 || number(object(added["run"]), "documents", 0) != 1 {
		t.Fatal(added)
	}
	runReviewJob(t, f, owner, str(object(added["run"]), "job_id"))
	f.call("DELETE", base+"/documents/"+private, owner, nil, 200)
	f.call("PATCH", base, owner, M{"fields": []M{{"name": "Governing law"}}, "description": "Narrowed"}, 200)
	remaining := maps(f.call("GET", base, member, nil, 200)["cells"])
	if len(remaining) != 2 {
		t.Fatal(remaining)
	}
	f.call("DELETE", "/matters/"+mid+"/members/"+memberID, owner, nil, 200)
	f.call("GET", base, member, nil, 404)
	f.call("DELETE", base, owner, nil, 200)
	for _, tableName := range []string{"cc_review_cells", "cc_review_runs"} {
		var n int
		if err := f.s.db.QueryRow("SELECT count(*) FROM "+tableName+" WHERE table_id=$1", tid).Scan(&n); err != nil || n != 0 {
			t.Fatal(n, err)
		}
	}
}

func TestReviewRunConcurrentChangesAndCancellation(t *testing.T) {
	for _, scenario := range []string{"human-review", "remove-field", "remove-document", "cancel", "revoke"} {
		t.Run(scenario, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			f := reviewFixture(t, func() int { once.Do(func() { close(entered) }); <-release; return 0 })
			owner := f.admin()
			member := f.account(owner, "runner@example.test")
			memberID := str(f.call("GET", "/auth/me", member, nil, 200), "id")
			mid := str(f.call("POST", "/matters", owner, M{"name": "Concurrent"}, 200), "id")
			f.call("POST", "/matters/"+mid+"/members", owner, M{"email": "runner@example.test"}, 200)
			doc := str(f.upload(owner, mid, "race.txt", reviewSource), "id")
			table := f.call("POST", "/review-tables", owner, M{"name": "Race", "matter_id": mid, "fields": reviewTestFields}, 201)
			tid := str(table, "id")
			base := "/review-tables/" + tid
			job := str(f.call("POST", base+"/run", member, M{}, 202), "job_id")
			run := claimReviewJob(t, f, job)
			done := make(chan struct{})
			go func() { defer close(done); run() }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("provider was not called")
			}
			switch scenario {
			case "human-review":
				f.call("PATCH", base+"/cells/"+doc+"/governing_law", owner, M{"override_value": "Virginia", "review_status": "approved", "note": "Counsel correction"}, 200)
			case "remove-field":
				f.call("PATCH", base, owner, M{"fields": []M{{"name": "Effective date", "type": "date"}}}, 200)
			case "remove-document":
				f.call("DELETE", base+"/documents/"+doc, owner, nil, 200)
			case "cancel":
				f.call("DELETE", "/jobs/"+job, member, nil, 200)
			case "revoke":
				f.call("DELETE", "/matters/"+mid+"/members/"+memberID, owner, nil, 200)
			}
			close(release)
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("run did not finish")
			}
			cells := maps(f.call("GET", base, owner, nil, 200)["cells"])
			switch scenario {
			case "human-review":
				c := getReviewCell(t, f, owner, tid, doc, "governing_law")
				if str(c, "effective_value") != "Virginia" || str(c, "review_status") != "approved" || str(c, "note") != "Counsel correction" {
					t.Fatal(c)
				}
			case "remove-field":
				if len(cells) != 1 || str(cells[0], "field_key") != "effective_date" {
					t.Fatal(cells)
				}
			default:
				if len(cells) != 0 {
					t.Fatalf("late result persisted after %s: %v", scenario, cells)
				}
			}
			runs := maps(f.call("GET", base+"/runs", owner, nil, 200)["runs"])
			want := "completed"
			if scenario == "cancel" {
				want = "cancelled"
			}
			if scenario == "revoke" {
				want = "failed"
			}
			if len(runs) != 1 || str(runs[0], "status") != want || runs[0]["finished_at"] == nil {
				t.Fatal(runs)
			}
		})
	}
}

func TestReviewRunHistorySurvivesQueueRetentionAndLeaseExpiry(t *testing.T) {
	f := reviewFixture(t, nil)
	owner := f.admin()
	doc := str(f.upload(owner, "", "history.txt", reviewSource), "id")
	table := f.call("POST", "/review-tables", owner, M{"name": "History", "fields": reviewTestFields, "document_ids": []string{doc}}, 201)
	base := "/review-tables/" + str(table, "id")
	job := str(f.call("POST", base+"/run", owner, M{}, 202), "job_id")
	claimReviewJob(t, f, job)
	if _, err := f.s.db.ExecContext(context.Background(), `UPDATE cc_jobs SET status='failed',error='Worker lease expired',updated_at=now() WHERE id=$1`, job); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec(`DELETE FROM cc_jobs WHERE id=$1`, job); err != nil {
		t.Fatal(err)
	}
	runs := maps(f.call("GET", base+"/runs", owner, nil, 200)["runs"])
	if len(runs) != 1 || str(runs[0], "status") != "failed" || str(runs[0], "error") != "Worker lease expired" || runs[0]["job_id"] != nil {
		t.Fatal(runs)
	}
}
