package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const researchQuestion = "What notice does the agreement require?"

func TestDeepResearchAPIOracle(t *testing.T) {
	b, err := os.ReadFile("testdata/research_api.json")
	if err != nil {
		t.Fatal(err)
	}
	fixture := M{}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	for i, tc := range maps(fixture["requests"]) {
		question, plan, err := validateResearchRequest(object(tc["input"]), true, 20)
		if (err == nil) != (tc["valid"] == true) {
			t.Fatalf("request %d validity: %v", i, err)
		}
		if err == nil && (question != str(tc, "question") || string(jsonBytes(plan)) != string(jsonBytes(tc["plan"]))) {
			t.Fatalf("request %d normalization: %v", i, plan)
		}
	}
	for i, tc := range maps(fixture["filters"]) {
		ids := []string{}
		for _, doc := range maps(fixture["documents"]) {
			if researchFilterMatches(doc, object(tc["filter"])) {
				ids = append(ids, str(doc, "id"))
			}
		}
		if !slices.Equal(ids, stringsOf(tc["expected"])) {
			t.Fatalf("filter %d: got %v want %v", i, ids, tc["expected"])
		}
	}
}

func researchFixture(t *testing.T, synth func() bool) *fixture {
	f := newFixture(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := M{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		messages := maps(payload["messages"])
		prompt := str(messages[len(messages)-1], "content")
		out := M{}
		switch {
		case strings.Contains(prompt, "Break it into"):
			out = M{"sub_questions": []M{{"id": "untrusted-id", "question": researchQuestion, "tools": []string{"documents"}}, {"question": "Is notice enforceable?", "tools": []string{"case_law", "invalid"}}}}
		case strings.Contains(prompt, "senior legal researcher"):
			if synth != nil && !synth() {
				http.Error(w, "Test synthesis failure", 503)
				return
			}
			out = M{"answer": "The notice period is 30 days. [P1]", "key_findings": []M{{"text": "Notice is 30 days.", "refs": []string{"P1"}}}, "open_questions": []string{"When was notice served?"}}
		default:
			out = M{"answer": "Notice is 30 days. [P1]", "claims": []M{{"text": "Notice period", "quote": "Notice is 30 days.", "refs": []string{"P1"}}}}
		}
		writeJSON(w, 200, M{"choices": []M{{"message": M{"content": string(jsonBytes(out))}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(provider.Close)
	f.s.cfg.LLMURL = provider.URL
	return f
}

func editedResearchPlan() []M {
	return []M{{"id": "edited_1", "question": researchQuestion, "tools": []string{"documents", "documents"}}}
}

func TestDeepResearchPlanningScopeAndPersistence(t *testing.T) {
	f := researchFixture(t, nil)
	owner := f.admin()
	member := f.account(owner, "research-member@example.test")
	outsider := f.account(owner, "research-other@example.test")
	user := str(f.call("GET", "/auth/me", owner, nil, 200), "id")
	matter := str(f.call("POST", "/matters", owner, M{"name": "Research matter"}, 200), "id")
	f.call("POST", "/matters/"+matter+"/members", owner, M{"email": "research-member@example.test"}, 200)
	doc := str(f.upload(owner, matter, "notice.txt", "Notice is 30 days."), "id")
	private := str(f.upload(owner, "", "private.txt", "Do not disclose this document."), "id")
	for _, tc := range []struct {
		token string
		in    M
		ids   []string
		count int
	}{
		{owner, M{}, nil, 2},
		{member, M{"matter_id": matter}, []string{doc}, 1},
		{owner, M{"document_filter": M{"document_ids": []string{private, doc}}}, []string{doc, private}, 2},
		{member, M{"document_filter": M{"document_ids": []string{private}}}, []string{}, 0},
		{owner, M{"matter_id": matter, "document_filter": M{"document_ids": []string{private}}}, []string{private}, 1},
		{owner, M{"document_filter": M{"document_ids": []string{}}}, []string{}, 0},
		{owner, M{"document_filter": M{"matter_ids": []string{matter, "not-readable"}}}, []string{doc}, 1},
	} {
		tc.in["question"] = researchQuestion
		result := f.call("POST", "/deep-research/plan", tc.token, tc.in, 200)
		scope := object(result["scope"])
		got, want := stringsOf(scope["document_ids"]), append([]string{}, tc.ids...)
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) || number(scope, "documents_in_scope", -1) != tc.count || (scope["document_ids"] == nil) != (tc.ids == nil) {
			t.Fatal(tc.in, scope)
		}
		plan := maps(result["plan"])
		if len(plan) != 2 || str(plan[0], "id") != "q1" || strings.Join(stringsOf(plan[1]["tools"]), ",") != "case_law" {
			t.Fatal(result)
		}
	}
	f.call("POST", "/deep-research/plan", outsider, M{"question": researchQuestion, "matter_id": matter}, 404)
	personal := ""
	for _, m := range maps(f.call("GET", "/matters", owner, nil, 200)["matters"]) {
		if m["is_personal"] == true {
			personal = str(m, "id")
		}
	}
	if personal == "" {
		t.Fatal("Personal matter missing")
	}
	result := f.call("POST", "/deep-research/plan", owner, M{"question": researchQuestion, "document_filter": M{"matter_ids": []string{personal}}}, 200)
	if ids := stringsOf(object(result["scope"])["document_ids"]); len(ids) != 1 || ids[0] != private {
		t.Fatal(result)
	}
	started := f.call("POST", "/deep-research/run", owner, M{"question": researchQuestion, "matter_id": matter, "plan": editedResearchPlan()}, 202)
	id, job := str(started, "run_id"), str(started, "job_id")
	url := "/deep-research/" + id
	runReviewJob(t, f, owner, job)
	detail := f.call("GET", url, member, nil, 200)
	if str(detail, "role") != "member" || str(detail, "status") != "completed" || number(detail, "sources_cited", 0) != 1 || str(maps(detail["plan"])[0], "id") != "edited_1" || str(object(detail["result"]), "run_id") != id {
		t.Fatal(detail)
	}
	if len(stringsOf(maps(detail["plan"])[0]["tools"])) != 1 {
		t.Fatal("duplicate tools retained", detail)
	}
	f.call("GET", url, outsider, nil, 404)
	f.call("DELETE", url, member, nil, 404)
	list := f.call("GET", "/deep-research?matter_id="+matter+"&limit=1", member, nil, 200)
	if len(maps(list["runs"])) != 1 {
		t.Fatal(list)
	}
	f.call("GET", "/deep-research?matter_id="+matter, outsider, nil, 404)
	f.call("GET", "/deep-research?limit=201", owner, nil, 422)
	if _, err := f.s.db.Exec(`DELETE FROM cc_jobs WHERE id=$1`, job); err != nil {
		t.Fatal(err)
	}
	detail = f.call("GET", url, owner, nil, 200)
	if detail["job_id"] != nil || detail["result"] == nil || detail["completed_at"] == nil {
		t.Fatal("queue cleanup erased history", detail)
	}
	var audit string
	if err := f.s.db.QueryRow(`SELECT string_agg(details::text,' ') FROM cc_audit WHERE user_id=$1 AND resource_id=$2`, user, id).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audit, researchQuestion) || strings.Contains(audit, "Notice is 30 days.") || !strings.Contains(audit, "sources_cited") {
		t.Fatal("audit must contain counts only", audit)
	}
	f.call("DELETE", url, owner, nil, 200)
	f.call("GET", url, owner, nil, 404)
}

func TestDeepResearchValidationAndAtomicQueueAdmission(t *testing.T) {
	f := researchFixture(t, nil)
	owner := f.admin()
	user := str(f.call("GET", "/auth/me", owner, nil, 200), "id")
	for _, plan := range []any{[]M{}, []M{{"id": "q", "question": "What notice?", "tools": []string{"shell"}}}, []M{{"id": "q", "question": "What notice?", "tools": []string{"documents"}}, {"id": "q", "question": "Which terms?", "tools": []string{"documents"}}}, []any{M{"id": "q", "question": "What notice?", "tools": []string{"documents"}}, "ignored?"}} {
		f.call("POST", "/deep-research/run", owner, M{"question": researchQuestion, "plan": plan}, 422)
	}
	for _, in := range []M{{"question": "   "}, {"question": researchQuestion, "document_filter": M{"sources": []string{"unknown"}}}, {"question": researchQuestion, "document_filter": M{"include_subfolders": "maybe"}}} {
		f.call("POST", "/deep-research/plan", owner, in, 422)
	}
	f.s.cfg.ResearchMaxSubquestions = 1
	f.call("POST", "/deep-research/run", owner, M{"question": researchQuestion, "plan": []M{{"id": "a", "question": "Question one?", "tools": []string{"documents"}}, {"id": "b", "question": "Question two?", "tools": []string{"documents"}}}}, 400)
	f.s.cfg.LLMKey = ""
	f.call("POST", "/deep-research/run", owner, M{"question": researchQuestion, "plan": editedResearchPlan()}, 400)
	f.call("POST", "/deep-research/plan", owner, M{"question": researchQuestion}, 400)
	f.s.cfg.LLMKey = "test-key"
	for range 5 {
		if _, err := f.s.db.Exec(`INSERT INTO cc_jobs(id,user_id,kind,input) VALUES($1,$2,'fixture','{}')`, uuid(), user); err != nil {
			t.Fatal(err)
		}
	}
	f.call("POST", "/deep-research/run", owner, M{"question": researchQuestion, "plan": editedResearchPlan()}, 429)
	if runs := maps(f.call("GET", "/deep-research", owner, nil, 200)["runs"]); len(runs) != 0 {
		t.Fatal("failed admission left a run", runs)
	}
}

func TestDeepResearchCancellationDeletionAndFailedSynthesis(t *testing.T) {
	for _, action := range []string{"cancel", "delete", "failure"} {
		t.Run(action, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			f := researchFixture(t, func() bool { once.Do(func() { close(entered) }); <-release; return action != "failure" })
			owner := f.admin()
			doc := str(f.upload(owner, "", "notice.txt", "Notice is 30 days."), "id")
			started := f.call("POST", "/deep-research/run", owner, M{"question": researchQuestion, "plan": editedResearchPlan(), "document_filter": M{"document_ids": []string{doc}}}, 202)
			url, job := "/deep-research/"+str(started, "run_id"), str(started, "job_id")
			work := claimReviewJob(t, f, job)
			done := make(chan struct{})
			go func() { work(); close(done) }()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				close(release)
				t.Fatal("synthesis did not start")
			}
			if action == "cancel" {
				f.call("DELETE", "/jobs/"+job, owner, nil, 200)
			}
			if action == "delete" {
				f.call("DELETE", url, owner, nil, 200)
			}
			close(release)
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("worker did not finish")
			}
			if action == "delete" {
				f.call("GET", url, owner, nil, 404)
			} else {
				detail := f.call("GET", url, owner, nil, 200)
				status := "cancelled"
				if action == "failure" {
					status = "failed"
				}
				if str(detail, "status") != status || detail["result"] != nil || detail["completed_at"] == nil {
					t.Fatal(detail)
				}
			}
			user := str(f.call("GET", "/auth/me", owner, nil, 200), "id")
			if _, err := f.s.getObject(context.Background(), user, "result", job); !notFound(err) {
				t.Fatal("unfinished run published a result", err)
			}
		})
	}
}

func TestResearchDocumentFilterUnionsAndMetadata(t *testing.T) {
	doc := M{"id": "picked", "source": "google_drive", "metadata": M{"doc_type": "brief", "parent_folder": "/Cases/Notice/Sub/"}}
	for _, tc := range []struct {
		f    M
		want bool
	}{
		{M{"document_ids": []string{"picked"}, "sources": []string{"local"}}, true},
		{M{"document_ids": []string{"another"}}, false},
		{M{"document_ids": []string{"another"}, "folder_paths": []string{"/Cases/Notice/"}}, true},
		{M{"folder_paths": []string{"/Cases/Notice/"}, "include_subfolders": false}, false},
		{M{"folder_paths": []string{"/Cases/Notice/Sub"}, "include_subfolders": false}, true},
		{M{"folder_paths": []string{"/Cases/Not"}}, false},
		{M{"sources": []string{"google_drive"}, "doc_types": []string{"brief"}}, true},
		{M{"sources": []string{"local"}}, false},
		{M{"doc_types": []string{"contract"}}, false},
	} {
		if got := researchFilterMatches(doc, tc.f); got != tc.want {
			t.Fatal(tc.f, got)
		}
	}
	longFolder := "/" + strings.Repeat("a", 120) + "/"
	if _, _, err := validateResearchRequest(M{"question": researchQuestion, "document_filter": M{"folder_paths": []string{longFolder}}}, false, 8); err != nil {
		t.Fatal("valid long folder rejected", err)
	}
}

func TestDeepResearchRevokedSourceAndLeaseFailure(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f := researchFixture(t, func() bool { once.Do(func() { close(entered) }); <-release; return true })
	owner := f.admin()
	member := f.account(owner, "research-revoke@example.test")
	memberID := str(f.call("GET", "/auth/me", member, nil, 200), "id")
	matter := str(f.call("POST", "/matters", owner, M{"name": "Revocable research"}, 200), "id")
	f.call("POST", "/matters/"+matter+"/members", owner, M{"email": "research-revoke@example.test"}, 200)
	f.upload(owner, matter, "notice.txt", "Notice is 30 days.")
	// No explicit IDs: recheck actual sources even for all-readable searches.
	started := f.call("POST", "/deep-research/run", member, M{"question": researchQuestion, "plan": editedResearchPlan()}, 202)
	job := str(started, "job_id")
	work := claimReviewJob(t, f, job)
	done := make(chan struct{})
	go func() { work(); close(done) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("synthesis did not start")
	}
	f.call("DELETE", "/matters/"+matter+"/members/"+memberID, owner, nil, 200)
	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not finish")
	}
	detail := f.call("GET", "/deep-research/"+str(started, "run_id"), member, nil, 200)
	if str(detail, "status") != "failed" || detail["result"] != nil {
		t.Fatal("revoked source published", detail)
	}
	started = f.call("POST", "/deep-research/run", owner, M{"question": researchQuestion, "plan": editedResearchPlan()}, 202)
	if _, err := f.s.db.Exec(`UPDATE cc_jobs SET status='failed',error='Worker lease expired',updated_at=now() WHERE id=$1`, str(started, "job_id")); err != nil {
		t.Fatal(err)
	}
	detail = f.call("GET", "/deep-research/"+str(started, "run_id"), owner, nil, 200)
	if str(detail, "status") != "failed" || detail["completed_at"] == nil || str(detail, "error") != "Worker lease expired" {
		t.Fatal(detail)
	}
}

func TestDeepResearchPlannerFailureIsUpstreamError(t *testing.T) {
	f := newFixture(t)
	owner := f.admin()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, M{"choices": []M{{"message": M{"content": `{"sub_questions":[]}`}}}})
	}))
	t.Cleanup(provider.Close)
	f.s.cfg.LLMURL = provider.URL
	f.call("POST", "/deep-research/plan", owner, M{"question": researchQuestion}, 502)
}
