package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const contractFixtureText = `Acme Corporation ("Provider") and Bravo LLC ("Customer").

Customer shall pay all invoices within thirty days.

This agreement is governed by the laws of California.

"Services" means the work described in Schedule A. Services include installation.`

func contractFixture(t *testing.T, hooks ...func(string)) (*fixture, *[]string, *sync.Mutex) {
	f := newFixture(t)
	calls := []string{}
	mu := &sync.Mutex{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body M
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/embeddings") {
			items := stringsOf(body["input"])
			data := []M{}
			for i := range items {
				vector := []float64{0, 1}
				if len(items) == 32 {
					vector = []float64{1, 0}
				}
				data = append(data, M{"index": i, "embedding": vector})
			}
			writeJSON(w, 200, M{"data": data})
			return
		}
		messages := maps(body["messages"])
		system, prompt := str(messages[0], "content"), str(messages[len(messages)-1], "content")
		mu.Lock()
		calls = append(calls, system+"\n"+prompt)
		mu.Unlock()
		for _, hook := range hooks {
			hook(system)
		}
		var result any
		switch {
		case strings.Contains(system, "Classify this contract"):
			result = M{"contract_type": "nda"}
		case strings.Contains(system, "Return a comprehensive JSON analysis"):
			result = M{"summary": "The parties provide and pay for Services.", "risks": []M{{"description": "Payment is due quickly", "severity": "medium"}}, "missing_clauses": []M{{"clause": "Governing Law", "importance": "critical"}}}
		case strings.Contains(system, "Identify every issue that matters"):
			result = M{"issues": []M{{"title": "Payment timing", "severity": "major", "quote": "Customer shall pay all invoices within thirty days.", "why": "Check time to process invoices."}, {"title": "Fabrication", "quote": "Customer pays one million dollars immediately."}}}
		case strings.Contains(system, "Assess each finding"):
			result = M{"issues": []M{{"ref": "ai-0", "title": "Payment timing", "status": "unusual", "severity": "major", "span_start": 999999, "span_end": 999999, "why": "Allow time to process invoices.", "suggested_language": "Extend payment to forty-five days."}, {"ref": "fabricated-ref", "title": "Must not survive"}}}
		case strings.Contains(system, "Each claim asserts"):
			result = M{"verdicts": []M{{"index": 0, "addressed": true, "quote": "This agreement is governed by the laws of California."}}}
		case strings.Contains(system, "Extract concise key terms"):
			result = M{"fields": M{"payment_terms": M{"value": "Thirty days", "quote": "Customer shall pay all invoices within thirty days."}, "liability_cap": M{"value": "$1 million", "quote": "Liability is limited to one million dollars."}}}
		case strings.Contains(system, "Redraft one clause"):
			result = M{"proposed_text": "Customer shall pay all invoices within forty-five days.", "rationale": "Allow invoice processing time."}
		case strings.Contains(system, "Answer the lawyer's request"):
			result = M{"answer": "Invoices are due within thirty days. [2] An unsupported claim [1]. Smith v. Jones, 123 F.3d 456.", "citations": []string{"A fabricated first quote.", "Customer shall pay all invoices within thirty days."}}
		case strings.Contains(system, "Classify a lawyer's message"):
			result = M{"intent": "redline", "instructions": ""}
		case strings.Contains(system, "Compare contracts clause by clause"):
			result = M{"overall": "Payment timing changed.", "clauses": []M{{"topic": "Payment", "status": "changed", "summary": "More time to pay.", "quote_a": "Customer shall pay all invoices within thirty days.", "quote_b": "Customer shall pay all invoices within forty-five days."}, {"topic": "Fabricated", "quote_a": "Secret provision never in the contract."}}}
		case strings.Contains(system, "planning a long document"):
			result = M{"title": "Services Agreement", "sections": []M{{"title": "Services", "brief": "Describe the services", "target_words": 450}, {"title": "Payment", "brief": "State payment terms", "target_words": 450}, {"title": "Termination", "brief": "Notice and cure", "target_words": 450}}, "definitions": "Provider and Customer", "style_guide": "Numbered sections"}
		case strings.Contains(system, "writing ONE section"):
			result = strings.Repeat("The parties agree to perform the obligations in this section. ", 25)
		case strings.Contains(system, "senior reviewer of a long legal document"):
			result = "Services Agreement\n\n1. Services\nShortened.\n\n=== RECONCILE NOTES ===\n- Simplified."
		case strings.Contains(system, "expert legal document drafter"):
			result = M{"title": "Draft Agreement", "text": "Provider shall perform the Services. Customer shall pay agreed fees."}
		default:
			t.Errorf("Unhandled contract prompt: %s", system)
			result = M{}
		}
		answer, ok := result.(string)
		if !ok {
			answer = string(jsonBytes(result))
		}
		writeJSON(w, 200, M{"choices": []M{{"message": M{"content": answer}, "finish_reason": "stop"}}, "usage": M{"total_tokens": 100}})
	}))
	t.Cleanup(provider.Close)
	f.s.cfg.LLMURL = provider.URL
	f.s.cfg.EmbeddingURL = provider.URL
	return f, &calls, mu
}
func TestCancelledAnalysisCannotPublishSavedResults(t *testing.T) {
	var f *fixture
	id := uuid()
	f, _, _ = contractFixture(t, func(system string) {
		if strings.Contains(system, "Redraft one clause") {
			if _, err := f.s.db.Exec(`UPDATE cc_jobs SET status='cancelled' WHERE id=$1`, id); err != nil {
				t.Error(err)
			}
		}
	})
	access := f.admin()
	user := str(f.call("GET", "/auth/me", access, nil, 200), "id")
	input := M{"document_text": contractFixtureText, "mode": "redline", "message": "Allow more time to pay", "document_ids": []string{}}
	if _, err := f.s.db.Exec(`INSERT INTO cc_jobs(id,user_id,kind,input,status,lease_token,lease_until) VALUES($1,$2,'contract.analysis',$3,'running','test-lease',now()+interval '1 minute')`, id, user, jsonBytes(input)); err != nil {
		t.Fatal(err)
	}
	f.s.runJob(id, user, "contract.analysis", "test-lease", jsonBytes(input))
	var count int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM cc_objects WHERE owner_id=$1 AND kind IN ('analysis','result')`, user).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cancelled analysis published %d durable outputs", count)
	}
	job := f.call("GET", "/jobs/"+id, access, nil, 200)
	if str(job, "status") != "cancelled" || job["result"] != nil {
		t.Fatal(job)
	}
}

func waitContractJob(t *testing.T, f *fixture, access, id string) M {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		job := f.call("GET", "/jobs/"+id, access, nil, 200)
		switch str(job, "status") {
		case "completed":
			return object(job["result"])
		case "failed":
			t.Fatalf("contract job failed: %v", job)
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatal("contract job did not complete")
	return nil
}
func TestContractPipelineGroundingPersistenceAndExports(t *testing.T) {
	f, _, _ := contractFixture(t)
	owner := f.admin()
	outsider := f.account(owner, "outside-contract@example.test")
	f.s.StartWorkers()
	doc := f.upload(owner, "", "agreement.txt", contractFixtureText)
	did := str(doc, "id")
	f.call("POST", "/contract-analysis/analyze", owner, M{"document_text": contractFixtureText, "effective_date": "not-a-date"}, 400)
	f.call("POST", "/contract-analysis/compare", owner, M{"document_ids": []string{did, did}}, 400)
	clarify := f.call("POST", "/contract-analysis/chat", owner, M{"document_id": did, "message": "Redline this"}, 200)
	if str(clarify, "type") != "clarify" {
		t.Fatal(clarify)
	}
	answer := f.call("POST", "/contract-analysis/chat", owner, M{"document_id": did, "message": "When do we pay?", "mode": "ask"}, 200)
	if len(maps(answer["citations"])) != 1 || len(stringsOf(answer["case_law_removed"])) == 0 || !strings.Contains(str(answer, "answer"), caseRedaction) {
		t.Fatal(answer)
	}
	if !strings.Contains(str(answer, "answer"), "thirty days. [1]") || !strings.Contains(str(answer, "answer"), "[unverified citation]") {
		t.Fatal("answer points at a discarded quote", answer)
	}
	started := f.call("POST", "/contract-analysis/chat", owner, M{"document_id": did, "message": "Give the customer more time to pay", "mode": "redline"}, 202)
	if str(started, "type") != "redline_started" {
		t.Fatal(started)
	}
	result := waitContractJob(t, f, owner, str(started, "job_id"))
	if str(result, "kind") != "redlines" {
		t.Fatal(result)
	}
	if len(maps(result["parties"])) != 2 || len(maps(result["obligations"])) != 1 || len(maps(result["deadlines"])) != 1 {
		t.Fatalf("deterministic extraction missing: %v", result)
	}
	issues := maps(result["issues"])
	foundAddressed, foundAI := false, false
	for _, issue := range issues {
		if strings.Contains(str(issue, "title"), "Fabrication") || str(issue, "ref") == "fabricated-ref" {
			t.Fatal("unverified model finding escaped")
		}
		if str(issue, "ref") == "ai-0" {
			foundAI = true
			if str(issue, "grounding") != "span" || number(issue, "span_start", 0) == 999999 {
				t.Fatal(issue)
			}
		}
		if strings.HasPrefix(str(issue, "title"), "Addressed:") {
			foundAddressed = true
			if str(issue, "status") != "info" {
				t.Fatal(issue)
			}
		}
	}
	if !foundAddressed || !foundAI {
		t.Fatal(issues)
	}
	if object(object(result["key_terms"])["liability_cap"])["verified"] != false {
		t.Fatal("fabricated key-term quote marked verified")
	}
	edits := maps(result["redlines"])
	if len(edits) != 1 || str(edits[0], "original_text") != "Customer shall pay all invoices within thirty days." {
		t.Fatal(edits)
	}
	id := str(result, "analysis_id")
	stored := f.call("GET", "/contract-analysis/analyses/"+id, owner, nil, 200)
	if len(maps(object(stored["summary"])["redlines"])) != 1 {
		t.Fatal(stored)
	}
	listing := f.call("GET", "/contract-analysis/analyses?limit=1", owner, nil, 200)
	if number(maps(listing["analyses"])[0], "issues", -1) != len(issues) {
		t.Fatal(listing)
	}
	f.call("GET", "/contract-analysis/analyses/"+id, outsider, nil, 404)
	req, _ := http.NewRequest("POST", f.http.URL+"/api/v1/contract-analysis/analyses/"+id+"/redline-export", bytes.NewReader(jsonBytes(M{"overrides": M{str(edits[0], "ref"): "Customer shall pay within sixty days."}})))
	req.Header.Set("Authorization", "Bearer "+owner)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode, string(b), err)
	}
	xml := zipParts(t, b)["word/document.xml"]
	if !strings.Contains(revisionView(t, xml, true), "Customer shall pay within sixty days.") || !strings.Contains(revisionView(t, xml, false), "Customer shall pay all invoices within thirty days.") {
		t.Fatal("tracked override lost accept/reject fidelity")
	}
	f.call("DELETE", "/documents/"+did, owner, nil, 200)
	f.call("GET", "/contract-analysis/analyses/"+id, owner, nil, 200)
	f.call("DELETE", "/contract-analysis/analyses/"+id, owner, nil, 200)
	f.call("GET", "/contract-analysis/analyses/"+id, owner, nil, 404)
}
func TestContractDraftingAndMultiDocumentComparisonJobs(t *testing.T) {
	f, calls, mu := contractFixture(t)
	owner := f.admin()
	f.s.StartWorkers()
	a := f.upload(owner, "", "baseline.txt", contractFixtureText)
	b := f.upload(owner, "", "amended.txt", strings.ReplaceAll(contractFixtureText, "thirty days", "forty-five days"))
	c := f.upload(owner, "", "third.txt", contractFixtureText)
	compare := f.call("POST", "/contract-analysis/compare", owner, M{"document_ids": []string{str(a, "id"), str(b, "id"), str(c, "id")}, "focus": "Payment terms"}, 202)
	result := waitContractJob(t, f, owner, str(compare, "job_id"))
	if str(result, "kind") != "comparison_set" || len(maps(result["comparisons"])) != 2 {
		t.Fatal(result)
	}
	pair := maps(result["comparisons"])[0]
	clauses := maps(pair["clauses"])
	if clauses[0]["verified_a"] != true || clauses[0]["verified_b"] != true || clauses[1]["verified_a"] != false || clauses[1]["quote_a"] != nil {
		t.Fatal(clauses)
	}
	planJob := f.call("POST", "/contract-analysis/draft/plan", owner, M{"message": "Draft a services agreement", "target_pages": 3, "reference_document_ids": []string{str(a, "id")}}, 202)
	if str(planJob, "type") != "draft_plan_started" {
		t.Fatal(planJob)
	}
	planned := waitContractJob(t, f, owner, str(planJob, "job_id"))
	if str(planned, "kind") != "draft_plan" {
		t.Fatal(planned)
	}
	job := f.call("POST", "/contract-analysis/draft/generate", owner, M{"message": "Use customer-protective terms", "reference_document_ids": []string{str(a, "id")}, "plan": planned["plan"]}, 202)
	if str(job, "type") != "draft_started" {
		t.Fatal(job)
	}
	draft := waitContractJob(t, f, owner, str(job, "job_id"))
	if str(draft, "kind") != "draft" || len(maps(draft["sections"])) != 3 || !strings.Contains(strings.Join(stringsOf(draft["reconcile_notes"]), " "), "discarded") {
		t.Fatal(draft)
	}
	if !strings.Contains(str(draft, "text"), "3. Termination") {
		t.Fatal("shortened reconciliation replaced complete draft")
	}
	mu.Lock()
	defer mu.Unlock()
	sectionCalls := 0
	for _, call := range *calls {
		if strings.Contains(call, "writing ONE section") {
			sectionCalls++
			if !strings.Contains(call, contractFixtureText) || !strings.Contains(call, "3. Termination") {
				t.Fatal("section omitted full reference or shared plan")
			}
		}
	}
	if sectionCalls != 3 {
		t.Fatal(sectionCalls)
	}
	var progress []byte
	if err := f.s.db.QueryRowContext(context.Background(), `SELECT progress FROM cc_jobs WHERE id=$1`, str(job, "job_id")).Scan(&progress); err != nil || !bytes.Contains(progress, []byte("Reconciling")) {
		t.Fatal(string(progress), err)
	}
}
