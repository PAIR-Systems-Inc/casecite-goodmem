package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testWorkflowStep(name, kind string, params M) M {
	return M{"name": name, "kind": kind, "params": params}
}
func workflowRunPath(wid, rid string) string { return "/workflows/" + wid + "/runs/" + rid }

func TestWorkflowAPIApprovalsVersionsAndScope(t *testing.T) {
	f := newFixture(t)
	owner := f.admin()
	member := f.account(owner, "workflow-member@example.test")
	outsider := f.account(owner, "workflow-other@example.test")
	memberID := str(f.call("GET", "/auth/me", member, nil, 200), "id")
	matter := str(f.call("POST", "/matters", owner, M{"name": "Workflow Matter"}, 200), "id")
	f.call("POST", "/matters/"+matter+"/members", owner, M{"email": "workflow-member@example.test"}, 200)
	doc := str(f.upload(owner, matter, "agreement.txt", reviewSource), "id")
	private := str(f.upload(owner, "", "private.txt", reviewSource), "id")
	gate := testWorkflowStep("gate", "approval", M{"approvers": "owner", "title": "Supervising review"})
	cites := testWorkflowStep("cites", "citation_check", M{"from_steps": []string{}})
	validate := f.call("POST", "/workflows/validate", owner, M{"steps": []M{testWorkflowStep("x", "extract_fields", M{"fields": []M{}}), testWorkflowStep("x", "analyze_contract", M{}), testWorkflowStep("memo", "draft_document", M{"instructions": "Use {{later.summary}}"})}}, 200)
	if validate["valid"] != false || len(maps(validate["issues"])) < 3 {
		t.Fatal(validate)
	}
	if len(maps(f.call("GET", "/workflows/templates", owner, nil, 200)["templates"])) != 3 {
		t.Fatal("templates missing")
	}
	f.call("POST", "/workflows", member, M{"name": "Private fixed source", "steps": []M{{"name": "review", "kind": "analyze_contract", "inputs": M{"source": "documents", "document_ids": []string{private}}}}}, 404)
	f.call("POST", "/workflows", owner, M{"name": "Foreign approver", "steps": []M{testWorkflowStep("gate", "approval", M{"approvers": []string{memberID}})}}, 422)
	w := f.call("POST", "/workflows", owner, M{"name": "Intake", "matter_id": matter, "steps": []M{gate, cites}}, 201)
	wid := str(w, "id")
	base := "/workflows/" + wid
	f.call("PATCH", base, member, M{"name": "Forbidden"}, 404)
	f.call("GET", base, outsider, nil, 404)
	started := f.call("POST", base+"/run", member, M{}, 202)
	rid := str(started, "run_id")
	path := workflowRunPath(wid, rid)
	if number(started, "documents", 0) != 1 {
		t.Fatal(started)
	}
	parked := runReviewJob(t, f, member, str(started, "job_id"))
	if str(parked, "status") != "awaiting_approval" {
		t.Fatal(parked)
	}
	run := f.call("GET", path, owner, nil, 200)
	if run["can_approve"] != true || run["job_id"] != nil || str(maps(run["step_runs"])[0], "status") != "awaiting" || !slices.Contains(stringsOf(run["input_document_ids"]), doc) {
		t.Fatal(run)
	}
	f.call("POST", path+"/approve", member, M{}, 403)
	changed := testWorkflowStep("replacement", "approval", M{"approvers": "any_member"})
	w = f.call("PATCH", base, owner, M{"name": "Renamed", "steps": []M{changed}, "change_note": "New review process"}, 200)
	if number(w, "version", 0) != 2 {
		t.Fatal(w)
	}
	w = f.call("POST", base+"/versions/1/restore", owner, M{}, 200)
	if number(w, "version", 0) != 3 || str(w, "name") != "Renamed" {
		t.Fatal(w)
	}
	versions := f.call("GET", base+"/versions", member, nil, 200)
	if len(maps(versions["versions"])) != 3 {
		t.Fatal(versions)
	}
	v1 := f.call("GET", base+"/versions/1", member, nil, 200)
	if str(v1, "name") != "Intake" {
		t.Fatal(v1)
	}
	// Two simultaneous decisions must produce one continuation, never two jobs.
	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("POST", f.http.URL+"/api/v1"+path+"/approve", strings.NewReader(`{"note":"Proceed"}`))
			req.Header.Set("Authorization", "Bearer "+owner)
			req.Header.Set("Content-Type", "application/json")
			resp, e := http.DefaultClient.Do(req)
			if e != nil {
				statuses <- 0
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			statuses <- resp.StatusCode
		}()
	}
	wg.Wait()
	close(statuses)
	got := []int{}
	for code := range statuses {
		got = append(got, code)
	}
	slices.Sort(got)
	if !slices.Equal(got, []int{202, 400}) {
		t.Fatal(got)
	}
	run = f.call("GET", path, owner, nil, 200)
	runReviewJob(t, f, owner, str(run, "job_id"))
	run = f.call("GET", path, member, nil, 200)
	if str(run, "status") != "completed" || number(run, "workflow_version", 0) != 1 || number(run, "completed_steps", 0) != 2 || len(maps(run["step_definitions"])) != 2 {
		t.Fatal(run)
	}
	if str(object(object(maps(run["step_runs"])[0]["output"])["data"]), "note") != "Proceed" {
		t.Fatal(run)
	}
	// A shared reader who did not run the job cannot reset completed work.
	other := f.account(owner, "workflow-reader@example.test")
	f.call("POST", "/matters/"+matter+"/members", owner, M{"email": "workflow-reader@example.test"}, 200)
	f.call("POST", path+"/steps/1/retry", other, nil, 403)
	retry := f.call("POST", path+"/steps/1/retry", member, nil, 202)
	runReviewJob(t, f, member, str(retry, "job_id"))
	if number(f.call("GET", path, owner, nil, 200), "resumed_count", 0) != 1 {
		t.Fatal("retry not recorded")
	}
	list := f.call("GET", "/workflows/runs?matter_id="+matter, member, nil, 200)
	if len(maps(list["runs"])) != 1 || str(maps(list["runs"])[0], "workflow_name") != "Renamed" {
		t.Fatal(list)
	}
	f.call("POST", base+"/archive", owner, nil, 200)
	if len(maps(f.call("GET", "/workflows", member, nil, 200)["workflows"])) != 0 {
		t.Fatal("archived visible")
	}
	f.call("POST", base+"/run", owner, M{}, 400)
	if len(maps(f.call("GET", "/workflows?include_archived=true", member, nil, 200)["workflows"])) != 1 {
		t.Fatal("archive history missing")
	}
	f.call("POST", base+"/unarchive", owner, nil, 200)
	copy := f.call("POST", base+"/duplicate", member, nil, 201)
	if number(copy, "version", 0) != 1 || copy["matter_id"] != nil || str(copy, "role") != "owner" {
		t.Fatal(copy)
	}
	template := f.call("POST", "/workflows/from-template/nda_intake?matter_id="+matter, member, nil, 201)
	if str(template, "template_key") != "nda_intake" || len(maps(template["steps"])) != 3 {
		t.Fatal(template)
	}
	// The generic POST action route must enforce the same permission as the
	// original explicit routes, including template creation and every action.
	f.call("PATCH", "/admin/users/"+memberID+"/role", owner, M{"role": "viewer"}, 200)
	member = str(f.call("POST", "/auth/login", "login", M{"email": "workflow-member@example.test", "password": "River!Grove4928"}, 200), "access_token")
	for _, action := range []string{"run", "duplicate", "archive", "unarchive"} {
		f.call("POST", base+"/"+action, member, M{}, 403)
	}
	f.call("POST", "/workflows/from-template/nda_intake", member, M{}, 403)
	f.call("PATCH", "/admin/users/"+memberID+"/role", owner, M{"role": "attorney"}, 200)
	member = str(f.call("POST", "/auth/login", "login", M{"email": "workflow-member@example.test", "password": "River!Grove4928"}, 200), "access_token")
	f.call("DELETE", base, owner, nil, 200)
	f.call("GET", path, member, nil, 404)
	var n int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM cc_workflow_steps WHERE run_id=$1`, rid).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestWorkflowFailedPreflightIsAtomicAndResumeReusesWork(t *testing.T) {
	f := newFixture(t)
	owner := f.admin()
	var calls atomic.Int32
	var failing atomic.Bool
	failing.Store(true)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if failing.Load() {
			http.Error(w, "failure", 503)
			return
		}
		writeJSON(w, 200, M{"choices": []M{{"message": M{"content": `{"title":"Memo","text":"Complete draft"}`}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(provider.Close)
	f.s.cfg.LLMURL = provider.URL
	cites := testWorkflowStep("cites", "citation_check", M{"from_steps": []string{}})
	draft := testWorkflowStep("memo", "draft_document", M{"instructions": "Draft a memo based on {{cites.summary}}"})
	tail := testWorkflowStep("tail", "citation_check", M{"from_steps": []string{"memo"}})
	w := f.call("POST", "/workflows", owner, M{"name": "Failure and resume", "steps": []M{cites, draft, tail}}, 201)
	wid := str(w, "id")
	started := f.call("POST", "/workflows/"+wid+"/run", owner, M{}, 202)
	rid := str(started, "run_id")
	path := workflowRunPath(wid, rid)
	claimReviewJob(t, f, str(started, "job_id"))()
	run := f.call("GET", path, owner, nil, 200)
	rows := maps(run["step_runs"])
	if str(run, "status") != "failed" || str(rows[0], "status") != "completed" || str(rows[1], "status") != "failed" || str(rows[2], "status") != "skipped" {
		t.Fatal(run)
	}
	finished := str(rows[0], "finished_at")
	failedCalls := calls.Load()
	if failedCalls != 3 {
		t.Fatal("unexpected shared provider retry budget", failedCalls)
	}
	configuredEndpoint1 := f.s.cfg.LLMURL
	f.s.cfg.LLMURL = "https://api.openai.com/v1"
	f.s.cfg.LLMKey = ""
	f.call("POST", path+"/resume", owner, nil, 400)
	if str(f.call("GET", path, owner, nil, 200), "status") != "failed" {
		t.Fatal("failed preflight mutated state")
	}
	f.s.cfg.LLMURL = configuredEndpoint1
	f.s.cfg.LLMKey = "llm-fixture"
	failing.Store(false)
	resumed := f.call("POST", path+"/resume", owner, nil, 202)
	runReviewJob(t, f, owner, str(resumed, "job_id"))
	run = f.call("GET", path, owner, nil, 200)
	if str(run, "status") != "completed" || str(maps(run["step_runs"])[0], "finished_at") != finished || calls.Load() != failedCalls+1 {
		t.Fatal(run, calls.Load())
	}
	// Approval transition also rolls back when the queue is full.
	gate := testWorkflowStep("gate", "approval", M{})
	draft = testWorkflowStep("memo", "draft_document", M{"instructions": "Draft a standalone memo"})
	w = f.call("POST", "/workflows", owner, M{"name": "Approval admission", "steps": []M{gate, draft}}, 201)
	wid = str(w, "id")
	started = f.call("POST", "/workflows/"+wid+"/run", owner, M{}, 202)
	rid = str(started, "run_id")
	path = workflowRunPath(wid, rid)
	runReviewJob(t, f, owner, str(started, "job_id"))
	user := str(f.call("GET", "/auth/me", owner, nil, 200), "id")
	queued := []string{}
	for i := 0; i < 5; i++ {
		id := uuid()
		queued = append(queued, id)
		if _, err := f.s.db.Exec(`INSERT INTO cc_jobs(id,user_id,kind,input) VALUES($1,$2,'research','{}')`, id, user); err != nil {
			t.Fatal(err)
		}
	}
	f.call("POST", path+"/approve", owner, M{"note": "Must not commit"}, 429)
	run = f.call("GET", path, owner, nil, 200)
	if str(run, "status") != "awaiting_approval" || str(maps(run["step_runs"])[0], "status") != "awaiting" {
		t.Fatal("queue failure mutated approval", run)
	}
	for _, id := range queued {
		if _, err := f.s.db.Exec(`DELETE FROM cc_jobs WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	configuredEndpoint2 := f.s.cfg.LLMURL
	f.s.cfg.LLMURL = "https://api.openai.com/v1"
	f.s.cfg.LLMKey = ""
	f.call("POST", path+"/approve", owner, nil, 400)
	f.s.cfg.LLMURL = configuredEndpoint2
	f.s.cfg.LLMKey = "llm-fixture"
	approved := f.call("POST", path+"/approve", owner, M{"note": "Proceed"}, 202)
	runReviewJob(t, f, owner, str(approved, "job_id"))
	started = f.call("POST", "/workflows/"+wid+"/run", owner, M{}, 202)
	path = workflowRunPath(wid, str(started, "run_id"))
	runReviewJob(t, f, owner, str(started, "job_id"))
	rejected := f.call("POST", path+"/reject", owner, M{"note": "Needs revision"}, 200)
	if str(rejected, "status") != "rejected" || str(maps(rejected["step_runs"])[1], "status") != "skipped" {
		t.Fatal(rejected)
	}
}

func TestWorkflowCancellationDiscardsIncompleteStepProducts(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f := reviewFixture(t, func() int { once.Do(func() { close(entered) }); <-release; return 0 })
	owner := f.admin()
	doc := str(f.upload(owner, "", "agreement.txt", reviewSource), "id")
	w := f.call("POST", "/workflows", owner, M{"name": "Cancellable extraction", "steps": []M{testWorkflowStep("extract", "extract_fields", M{"fields": reviewTestFields}), testWorkflowStep("gate", "approval", M{})}}, 201)
	started := f.call("POST", "/workflows/"+str(w, "id")+"/run", owner, M{"document_ids": []string{doc}}, 202)
	job := str(started, "job_id")
	work := claimReviewJob(t, f, job)
	done := make(chan struct{})
	go func() { work(); close(done) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("model did not start")
	}
	f.call("DELETE", "/jobs/"+job, owner, nil, 200)
	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not stop")
	}
	run := f.call("GET", workflowRunPath(str(w, "id"), str(started, "run_id")), owner, nil, 200)
	if str(run, "status") != "cancelled" || str(maps(run["step_runs"])[1], "status") != "skipped" {
		t.Fatal(run)
	}
	if len(maps(f.call("GET", "/review-tables", owner, nil, 200)["tables"])) != 0 {
		t.Fatal("cancelled step left a saved table")
	}
	var n int
	f.s.db.QueryRow(`SELECT count(*) FROM cc_review_cells`).Scan(&n)
	if n != 0 {
		t.Fatal("cancelled step left cells")
	}
}

func TestWorkflowResearchOracle(t *testing.T) {
	b, err := os.ReadFile("testdata/research.json")
	if err != nil {
		t.Fatal(err)
	}
	fixture := M{}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	for key, count := range map[string]int{"plans": 12, "refs": 5, "excerpts": 9, "treatments": 15, "verdicts": 50} {
		if len(maps(fixture[key])) != count {
			t.Fatalf("incomplete %s fixtures", key)
		}
	}
	for _, c := range maps(fixture["plans"]) {
		got := normalizeResearchPlan(object(c["raw"]), object(c["available"]), number(c, "cap", 8))
		if string(jsonBytes(got)) != string(jsonBytes(c["expected"])) {
			t.Fatalf("plan: %s != %s", jsonBytes(got), jsonBytes(c["expected"]))
		}
	}
	for _, c := range maps(fixture["refs"]) {
		got := researchJoinRefs(str(c, "text"), stringsOf(c["allowed"]))
		if string(jsonBytes(got)) != string(jsonBytes(c["expected"])) {
			t.Fatalf("refs: %s != %s", jsonBytes(got), jsonBytes(c["expected"]))
		}
	}
	for _, c := range maps(fixture["excerpts"]) {
		if got := researchExcerpt(str(c, "text"), str(c, "query"), number(c, "limit", 4000)); got != str(c, "expected") {
			t.Fatalf("excerpt differs for %s", str(c, "query"))
		}
	}
	for _, c := range maps(fixture["treatments"]) {
		if got := courtTreatment(str(c, "text")); got != str(c, "expected") {
			t.Fatalf("treatment: %q => %s, want %s", str(c, "text"), got, str(c, "expected"))
		}
	}
	for _, c := range maps(fixture["verdicts"]) {
		level, good := courtVerdict(number(c, "negative", 0), number(c, "caution", 0), number(c, "read", 0))
		if level != str(c, "level") || good != c["good"] {
			t.Fatal("verdict differs", c, level, good)
		}
	}
}

func TestWorkflowSkipDecisionSurvivesResumeAndHistoryOutlivesJob(t *testing.T) {
	f := newFixture(t)
	owner := f.admin()
	var failModel atomic.Bool
	failModel.Store(true)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failModel.Load() {
			http.Error(w, "failure", 503)
			return
		}
		writeJSON(w, 200, M{"choices": []M{{"message": M{"content": `{"title":"Memo","text":"Finished"}`}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(provider.Close)
	f.s.cfg.LLMURL = provider.URL
	w := f.call("POST", "/workflows", owner, M{"name": "Skip an unavailable extraction", "steps": []M{testWorkflowStep("extract", "extract_fields", M{"fields": reviewTestFields}), testWorkflowStep("memo", "draft_document", M{"instructions": "Draft a standalone memo"})}}, 201)
	started := f.call("POST", "/workflows/"+str(w, "id")+"/run", owner, M{}, 202)
	path := workflowRunPath(str(w, "id"), str(started, "run_id"))
	claimReviewJob(t, f, str(started, "job_id"))()
	skipped := f.call("POST", path+"/steps/0/skip", owner, nil, 202)
	claimReviewJob(t, f, str(skipped, "job_id"))()
	run := f.call("GET", path, owner, nil, 200)
	if str(run, "status") != "failed" || str(maps(run["step_runs"])[0], "status") != "skipped" {
		t.Fatal(run)
	}
	failModel.Store(false)
	resumed := f.call("POST", path+"/resume", owner, nil, 202)
	runReviewJob(t, f, owner, str(resumed, "job_id"))
	run = f.call("GET", path, owner, nil, 200)
	if str(run, "status") != "completed" || str(maps(run["step_runs"])[0], "status") != "skipped" || number(run, "completed_steps", 0) != 1 {
		t.Fatal("resume repeated a user-skipped step", run)
	}
	if _, err := f.s.db.Exec(`DELETE FROM cc_jobs WHERE id=$1`, str(resumed, "job_id")); err != nil {
		t.Fatal(err)
	}
	run = f.call("GET", path, owner, nil, 200)
	if run["job_id"] != nil || str(run, "status") != "completed" {
		t.Fatal("history disappeared", run)
	}
	retry := f.call("POST", path+"/steps/1/retry", owner, nil, 202)
	_ = claimReviewJob(t, f, str(retry, "job_id"))
	if _, err := f.s.db.Exec(`UPDATE cc_jobs SET status='failed',error='Worker lease expired',updated_at=now() WHERE id=$1`, str(retry, "job_id")); err != nil {
		t.Fatal(err)
	}
	run = f.call("GET", path, owner, nil, 200)
	if str(run, "status") != "failed" || !strings.Contains(str(run, "error"), "lease expired") {
		t.Fatal(run)
	}
}

func TestWorkflowNotificationsUseTransitionSnapshotAndRespectOptOut(t *testing.T) {
	f := newFixture(t)
	owner := f.admin()
	member := f.account(owner, "notification-member@example.test")
	ownerID := str(f.call("GET", "/auth/me", owner, nil, 200), "id")
	memberID := str(f.call("GET", "/auth/me", member, nil, 200), "id")
	mid := str(f.call("POST", "/matters", owner, M{"name": "Notifications"}, 200), "id")
	f.call("POST", "/matters/"+mid+"/members", owner, M{"email": "notification-member@example.test"}, 200)
	f.s.cfg.WorkflowNotifications = true
	f.s.cfg.SMTPHost = "unused.test"
	f.s.cfg.SMTPFrom = "test@example.test"
	w := f.call("POST", "/workflows", owner, M{"name": "<script>private</script>", "matter_id": mid, "steps": []M{testWorkflowStep("first", "approval", M{"title": "First gate", "approvers": []string{memberID}}), testWorkflowStep("second", "approval", M{"title": "Second gate", "approvers": "owner"})}}, 201)
	started := f.call("POST", "/workflows/"+str(w, "id")+"/run", owner, M{}, 202)
	path := workflowRunPath(str(w, "id"), str(started, "run_id"))
	runReviewJob(t, f, owner, str(started, "job_id"))
	// Move on before dispatching the first event: its title must not become the second gate.
	approved := f.call("POST", path+"/approve", member, M{"note": "Checked"}, 202)
	runReviewJob(t, f, member, str(approved, "job_id"))
	if err := f.s.kvSet(context.Background(), "settings:"+ownerID, M{"workflow_email_notifications": false}, 0); err != nil {
		t.Fatal(err)
	}
	if err := f.s.deliverWorkflowNotification(context.Background()); err != nil {
		t.Fatal(err)
	}
	var encrypted string
	if err := f.s.db.QueryRow(`SELECT payload FROM cc_outbox ORDER BY created_at DESC LIMIT 1`).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	plain, err := f.s.crypt(encrypted, true)
	if err != nil {
		t.Fatal(err)
	}
	var email outboundEmail
	if err = json.Unmarshal([]byte(plain), &email); err != nil {
		t.Fatal(err)
	}
	if email.To != "notification-member@example.test" || !email.HTML || !strings.Contains(email.Body, "First gate") || strings.Contains(email.Body, "Second gate") || strings.Contains(email.Body, "<script>") || !strings.Contains(email.Body, "&lt;script&gt;") {
		t.Fatal(email)
	}
	if err = f.s.deliverWorkflowNotification(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	f.s.db.QueryRow(`SELECT count(*) FROM cc_outbox`).Scan(&count)
	if count != 1 {
		t.Fatal("opt-out not honored", count)
	}
	f.s.cfg.WorkflowNotifications = false
	f.call("POST", path+"/reject", owner, M{"note": "Needs work"}, 200)
	if err = f.s.deliverWorkflowNotification(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.s.db.QueryRow(`SELECT count(*) FROM cc_outbox`).Scan(&count)
	if count != 1 {
		t.Fatal("disabled notifications queued mail")
	}
}
