package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestWorkflowDeepResearchGroundingBudgetAndScope(t *testing.T) {
	f := newFixture(t)
	owner := f.admin()
	doc := str(f.upload(owner, "", "selected.txt", "Notice is 30 days."), "id")
	private := f.upload(owner, "", "unselected.txt", "UNSELECTED SECRET should never be searched.")
	var synthCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := M{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		messages := maps(payload["messages"])
		prompt := str(messages[len(messages)-1], "content")
		if strings.Contains(prompt, "UNSELECTED SECRET") {
			t.Error("research scope leaked")
		}
		answer := M{}
		switch {
		case strings.Contains(prompt, "Break it into"):
			answer = M{"sub_questions": []M{{"question": "What notice is required?", "tools": []string{"documents", "case_law"}}, {"question": "What is the notice period?", "tools": []string{"documents"}}, {"question": "What deadlines are stated?", "tools": []string{"documents"}}}}
		case strings.Contains(prompt, "senior legal researcher"):
			synthCalls.Add(1)
			answer = M{"answer": "Notice is 30 days. [P1, P999]", "key_findings": []M{{"text": "Notice is required.", "refs": []string{"P1", "P999"}}}, "open_questions": []string{"How was notice delivered?"}}
		default:
			answer = M{"answer": "Notice is 30 days. [P1] Bad reference [P99].", "claims": []M{{"text": "Notice is 30 days.", "refs": []string{"P1"}, "quote": "Notice is 30 days."}, {"text": "Unsupported quote", "refs": []string{"P1"}, "quote": "Notice is 90 days."}, {"text": "Invented passage", "refs": []string{"P999"}, "quote": "Invented"}}, "followup": "Notice exceptions and deadline"}
		}
		writeJSON(w, 200, M{"choices": []M{{"message": M{"content": string(jsonBytes(answer))}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(provider.Close)
	f.s.cfg.LLMURL = provider.URL
	f.s.cfg.ResearchConcurrency = 3
	f.s.cfg.ResearchFollowupDepth = 1
	f.s.cfg.ResearchMaxToolCalls = 6
	step := testWorkflowStep("research", "deep_research", M{"question": "What notice is required?", "tools": []string{"documents"}})
	w := f.call("POST", "/workflows", owner, M{"name": "Grounded research", "steps": []M{step}}, 201)
	started := f.call("POST", "/workflows/"+str(w, "id")+"/run", owner, M{"document_ids": []string{doc}}, 202)
	runReviewJob(t, f, owner, str(started, "job_id"))
	run := f.call("GET", workflowRunPath(str(w, "id"), str(started, "run_id")), owner, nil, 200)
	output := object(maps(run["step_runs"])[0]["output"])
	data := object(output["data"])
	verification := object(data["verification"])
	if number(verification, "quotes_verified", 0) != 3 || number(verification, "quotes_unverified", 0) != 3 || number(verification, "invalid_refs_stripped", 0) < 5 || strings.Contains(str(data, "answer"), "P999") || synthCalls.Load() != 1 {
		t.Fatal(data)
	}
	user := str(f.call("GET", "/auth/me", owner, nil, 200), "id")
	saved, err := f.s.getObject(context.Background(), user, "research_run", str(data, "run_id"))
	if err != nil {
		t.Fatal(err)
	}
	result := object(saved["result"])
	scope := object(result["scope"])
	if number(scope, "tool_calls", 0) != 6 || number(scope, "passages_collected", 0) != 1 || scope["case_law_searched"] != false || len(maps(result["sub_questions"])) != 3 {
		t.Fatal(result)
	}
	if len(maps(result["sources"])) != 1 || str(maps(result["sources"])[0], "document_id") != doc || str(maps(data["sources"])[0], "kind") != "document" {
		t.Fatal(data)
	}
	f.mu.Lock()
	retrieved := append([]string{}, f.retrieved...)
	f.mu.Unlock()
	for _, filter := range retrieved {
		if strings.Contains(filter, str(private, "memory_id")) {
			t.Fatal("retrieval included unselected memory")
		}
	}
}

func TestCourtListenerTreatmentContextExactIdentityAndFailure(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	user := str(f.call("GET", "/auth/me", access, nil, 200), "id")
	var partial atomic.Bool
	var mu sync.Mutex
	orders := map[string]int{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token court-test" {
			t.Error("missing CourtListener authentication")
		}
		query := r.URL.Query().Get("q")
		order := r.URL.Query().Get("order_by")
		if strings.HasPrefix(query, "citation:") {
			writeJSON(w, 200, M{"results": []M{{"cluster_id": 99, "caseName": "Wrong page", "citation": []string{"410 U.S. 1130"}, "opinions": []M{{"id": 999}}}, {"cluster_id": 7, "caseName": "Roe v. Wade", "caseNameShort": "Roe", "citation": []string{"410 U.S. 113"}, "citeCount": 22, "absolute_url": "/opinion/7/roe/", "opinions": []M{{"id": 111}}}}})
			return
		}
		if !strings.HasPrefix(query, "cites:(111)") {
			t.Errorf("wrong opinion identity in query: %s", query)
			http.Error(w, "bad identity", 400)
			return
		}
		mu.Lock()
		orders[order]++
		mu.Unlock()
		if partial.Load() && order == "citeCount desc" {
			http.Error(w, "failure", 503)
			return
		}
		snippet := "Opening summary says overruled, unrelated to the target citation."
		if r.URL.Query().Get("highlight") == "on" {
			snippet = "We followed <mark>410 U.S. 113</mark> in this case."
			if order == "citeCount desc" {
				snippet = "We overruled <mark>410 U.S. 113</mark>."
			}
		}
		writeJSON(w, 200, M{"results": []M{{"cluster_id": 10, "caseName": "Citing case", "dateFiled": "2024-01-01", "absolute_url": "/opinion/10/citing/", "opinions": []M{{"id": 222, "snippet": snippet}}}}})
	}))
	t.Cleanup(provider.Close)
	f.s.cfg.CourtListenerURL = provider.URL
	f.s.cfg.CourtListenerToken = "court-test"
	result, err := f.s.validateCourtCitation(context.Background(), user, "410 U.S. 113")
	if err != nil {
		t.Fatal(err)
	}
	if str(result, "case_name") != "Roe v. Wade" || str(result, "warning_level") != "warning" || result["is_good_law"] != nil || len(maps(result["negative_citations"])) != 1 || number(result, "citing_cases_analyzed", 0) != 1 {
		t.Fatal(result)
	}
	mu.Lock()
	both := orders["dateFiled desc"] > 0 && orders["citeCount desc"] > 0
	mu.Unlock()
	if !both {
		t.Fatal("missing citation ordering")
	}
	partial.Store(true)
	result, err = f.s.validateCourtCitation(context.Background(), user, "410 U.S. 113")
	if err != nil {
		t.Fatal(err)
	}
	if result["is_good_law"] != nil || str(result, "warning_level") != "unknown" {
		t.Fatal("partial scan reported clean", result)
	}
	if courtMatches("410 U.S. 113", M{"citation": []string{"410 U.S. 1130"}}) || courtMatches("410 U.S. 1130", M{"citation": []string{"410 U.S. 113"}}) {
		t.Fatal("substring page match accepted")
	}
	if !courtMatches("410 U.S. 113, 120", M{"citation": []string{"410 U.S. 113"}}) {
		t.Fatal("pinpoint citation rejected")
	}
	if !courtMatches("Roe v. Wade, 410 U.S. 113 (1973)", M{"citation": []string{"410 U.S. 113"}}) {
		t.Fatal("case name and parenthetical citation rejected")
	}
}

func TestResearchQuoteMustMatchCompletelyAndBudgetIsAtomic(t *testing.T) {
	source := "The notice provision requires written notice at least thirty days before termination."
	step := M{}
	recordResearchClaims(step, []M{
		{"text": "Valid", "refs": []string{"P1"}, "quote": source},
		{"text": "Partly fabricated", "refs": []string{"P1"}, "quote": source + " No notice is required for the buyer."},
	}, []M{{"ref": "P1", "text": source}})
	claims := maps(step["claims"])
	if len(claims) != 2 || claims[0]["verified"] != true || claims[1]["verified"] != false || number(step, "quotes_unverified", 0) != 1 {
		t.Fatal("partly matching quote was marked verified", step)
	}
	runner := &researchRunner{maxCalls: 4}
	var wg sync.WaitGroup
	var admitted atomic.Int32
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if runner.take() {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 4 || runner.remaining() != 0 {
		t.Fatal("tool budget overrun", admitted.Load())
	}
}

func TestWorkflowResearchEmptyScopeDoesNotExpand(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	user := str(f.call("GET", "/auth/me", access, nil, 200), "id")
	f.upload(access, "", "unselected.txt", "Never retrieve this")
	runner := &researchRunner{s: f.s, user: user, documentIDs: []string{}, passages: []M{}, byKey: map[string]M{}, maxPassages: 60}
	n, err := runner.documents(context.Background(), "q1", "notice")
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
	f.mu.Lock()
	count := len(f.retrieved)
	f.mu.Unlock()
	if count != 0 {
		t.Fatal("empty scope made a retrieval request")
	}
}
