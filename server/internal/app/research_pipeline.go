package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type researchRunner struct {
	s                                             *Server
	user                                          string
	documentIDs                                   []string // nil means all readable documents; empty means none.
	mu                                            sync.Mutex
	passages                                      []M
	byKey                                         map[string]M
	used, dropped                                 int
	maxCalls, maxPassages, concurrency, followups int
}

func copyWorkflowMap(m M) M { out := M{}; _ = json.Unmarshal(jsonBytes(m), &out); return out }

func (r *researchRunner) add(key, step string, p M) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old := r.byKey[key]; old != nil {
		old["sub_question_ids"] = uniqueWorkflowStrings(append(stringsOf(old["sub_question_ids"]), step))
		return true
	}
	if len(r.passages) >= r.maxPassages {
		r.dropped++
		return false
	}
	p["ref"] = fmt.Sprintf("P%d", len(r.passages)+1)
	p["sub_question_ids"] = []string{step}
	p["text"] = charSlice(str(p, "text"), 0, 4000)
	r.byKey[key] = p
	r.passages = append(r.passages, p)
	return true
}
func (r *researchRunner) forStep(step string) []M {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []M{}
	for _, p := range r.passages {
		if step == "" || slices.Contains(stringsOf(p["sub_question_ids"]), step) {
			out = append(out, copyWorkflowMap(p))
		}
	}
	return out
}
func (r *researchRunner) take() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.used >= r.maxCalls {
		return false
	}
	r.used++
	return true
}
func (r *researchRunner) remaining() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxCalls - r.used
}

func (r *researchRunner) documents(ctx context.Context, step, query string) (int, error) {
	scope := M{}
	if r.documentIDs != nil {
		scope["document_ids"] = r.documentIDs
	}
	docs, err := r.s.scopedDocuments(ctx, r.user, scope)
	if err != nil {
		return 0, err
	}
	byMemory := map[string]M{}
	for _, doc := range docs {
		memory := documentSearchMemory(doc)
		if memory != "" {
			byMemory[memory] = doc
		}
	}
	hits, err := r.s.searchDocumentIndexes(ctx, query, docs, 6, true)
	if err != nil {
		return 0, err
	}
	added := 0
	for _, hit := range hits {
		doc := byMemory[hit.MemoryID]
		if doc == nil || strings.TrimSpace(hit.Text) == "" {
			continue
		}
		p := M{"source_type": "document", "text": strings.TrimSpace(hit.Text), "title": fallback(str(doc, "filename"), "Document"), "excerpt_source": "index", "document_id": doc["id"], "chunk_index": hit.Sequence, "chunk_id": hit.ID, "score": hit.Score}
		if r.add("document:"+str(doc, "id")+":"+strconv.Itoa(hit.Sequence), step, p) {
			added++
		}
	}
	return added, nil
}

func (r *researchRunner) tools(ctx context.Context, step M, query string) {
	calls := maps(step["tool_calls"])
	for _, tool := range stringsOf(step["tools"]) {
		call := M{"tool": tool, "query": query, "passages": 0}
		var err error
		added := 0
		if !r.take() {
			call["error"] = fmt.Sprintf("The run reached its limit of %d tool calls.", r.maxCalls)
		} else {
			switch tool {
			case "documents":
				added, err = r.documents(ctx, str(step, "id"), query)
			case "case_law":
				var passages []M
				passages, err = r.s.courtResearchPassages(ctx, r.user, query)
				if err == nil {
					for _, p := range passages {
						if r.add("case_law:"+str(p, "opinion_id"), str(step, "id"), p) {
							added++
						}
					}
				}
			default:
				err = bad("Unknown research tool")
			}
			if err != nil {
				call["error"] = workflowSafeError(err)
			}
			call["passages"] = added
		}
		calls = append(calls, call)
		if ctx.Err() != nil {
			break
		}
	}
	step["tool_calls"] = calls
}

func recordResearchClaims(step M, raw any, passages []M) {
	allowed := []string{}
	byRef := map[string]M{}
	for _, p := range passages {
		ref := str(p, "ref")
		allowed = append(allowed, ref)
		byRef[ref] = p
	}
	claims := []M{}
	for _, item := range maps(raw) {
		if len(claims) >= 8 {
			break
		}
		text := strings.TrimSpace(str(item, "text"))
		if text == "" {
			continue
		}
		wanted := researchRefs(item["refs"])
		refs := []string{}
		for _, ref := range wanted {
			if slices.Contains(allowed, ref) {
				refs = append(refs, ref)
			}
		}
		n := len(wanted) - len(refs)
		if n == 0 && len(refs) == 0 {
			n = 1
		}
		step["invalid_refs_stripped"] = number(step, "invalid_refs_stripped", 0) + n
		if len(refs) == 0 {
			continue
		}
		quote := strings.TrimSpace(str(item, "quote"))
		verified := false
		var quoteRef any
		if quote != "" {
			for _, ref := range refs {
				if _, _, ok := findQuote(str(byRef[ref], "text"), quote); ok {
					verified = true
					quoteRef = ref
					break
				}
			}
			key := "quotes_unverified"
			if verified {
				key = "quotes_verified"
			}
			step[key] = number(step, key, 0) + 1
		}
		claims = append(claims, M{"text": text, "refs": refs, "quote": nullable(charSlice(quote, 0, 600)), "verified": verified, "quote_ref": quoteRef})
	}
	step["claims"] = claims
}

func (r *researchRunner) answer(ctx context.Context, step M, followup bool) (string, error) {
	passages := r.forStep(str(step, "id"))
	key := "answer_final"
	if followup {
		key = "answer"
	}
	prompt := strings.NewReplacer("__QUESTION__", researchNeutralize(str(step, "question")), "__PASSAGES__", researchNeutralize(researchPassageLines(passages, 60000))).Replace(str(researchPrompts, key))
	data, err := r.s.structured(ctx, r.user, legalSystem, prompt)
	if err != nil {
		return "", err
	}
	allowed := []string{}
	for _, p := range passages {
		allowed = append(allowed, str(p, "ref"))
	}
	joined := researchJoinRefs(str(data, "answer"), allowed)
	step["answer"] = joined["text"]
	step["invalid_refs_stripped"] = number(step, "invalid_refs_stripped", 0) + number(joined, "stripped", 0)
	recordResearchClaims(step, data["claims"], passages)
	if followup {
		return charSlice(strings.TrimSpace(str(data, "followup")), 0, 300), nil
	}
	return "", nil
}

func (r *researchRunner) runStep(ctx context.Context, step M) {
	start := time.Now()
	r.tools(ctx, step, str(step, "question"))
	follow, e := r.answer(ctx, step, r.followups > 0 && r.remaining() > 0)
	if e == nil && follow != "" && !strings.EqualFold(follow, str(step, "question")) {
		step["followup_query"] = follow
		before := len(r.forStep(str(step, "id")))
		r.tools(ctx, step, follow)
		if len(r.forStep(str(step, "id"))) > before {
			step["quotes_verified"] = 0
			step["quotes_unverified"] = 0
			step["invalid_refs_stripped"] = 0
			_, e = r.answer(ctx, step, false)
		}
	}
	step["status"] = "completed"
	if e != nil {
		step["status"] = "failed"
		step["error"] = workflowSafeError(e)
	}
	step["passages_found"] = len(r.forStep(str(step, "id")))
	step["duration_ms"] = time.Since(start).Milliseconds()
}

func (r *researchRunner) synthesize(ctx context.Context, question string, steps []M) (M, []M, error) {
	all := r.forStep("")
	byRef := map[string]M{}
	for _, p := range all {
		byRef[str(p, "ref")] = p
	}
	refs := []string{}
	for _, step := range steps {
		for _, claim := range maps(step["claims"]) {
			refs = append(refs, stringsOf(claim["refs"])...)
		}
	}
	for _, step := range steps {
		if len(maps(step["claims"])) == 0 {
			for _, p := range r.forStep(str(step, "id")) {
				refs = append(refs, str(p, "ref"))
			}
		}
	}
	refs = uniqueWorkflowStrings(refs)
	passages := []M{}
	for _, ref := range refs {
		if p := byRef[ref]; p != nil {
			passages = append(passages, p)
		}
	}
	findings := []string{}
	for _, step := range steps {
		findings = append(findings, "["+str(step, "id")+"] "+str(step, "question")+"\n"+fallback(str(step, "answer"), "(no answer)"))
	}
	prompt := strings.NewReplacer("__QUESTION__", researchNeutralize(question), "__FINDINGS__", researchNeutralize(strings.Join(findings, "\n\n")), "__PASSAGES__", researchNeutralize(researchPassageLines(passages, 120000))).Replace(str(researchPrompts, "synthesis"))
	data, stats, err := r.s.structuredWithStats(ctx, r.user, legalSystem, prompt)
	if err != nil {
		return nil, nil, bad("The research synthesis could not be completed. Please try again.")
	}
	joined := researchJoinRefs(str(data, "answer"), refs)
	cited := stringsOf(joined["cited"])
	stripped := number(joined, "stripped", 0)
	keyFindings := []M{}
	for _, item := range maps(data["key_findings"]) {
		text := strings.TrimSpace(str(item, "text"))
		wanted := researchRefs(item["refs"])
		kept := []string{}
		for _, ref := range wanted {
			if slices.Contains(refs, ref) {
				kept = append(kept, ref)
			}
		}
		stripped += len(wanted) - len(kept)
		if text != "" {
			keyFindings = append(keyFindings, M{"text": text, "refs": kept})
			cited = append(cited, kept...)
		}
	}
	open := []string{}
	for _, text := range stringsOf(data["open_questions"]) {
		if text = strings.TrimSpace(text); text != "" && len(open) < 10 {
			open = append(open, text)
		}
	}
	sources := []string{question}
	for _, p := range passages {
		sources = append(sources, str(p, "text"))
	}
	answer, removed := redactCaseReferences(str(joined, "text"), sources, nil, false)
	for _, finding := range keyFindings {
		text, gone := redactCaseReferences(str(finding, "text"), sources, nil, false)
		finding["text"] = text
		removed = append(removed, gone...)
	}
	if strings.TrimSpace(answer) == "" {
		return nil, nil, bad("The research synthesis returned no answer. Retry the run.")
	}
	return M{"answer": answer, "key_findings": keyFindings, "open_questions": open, "cited_refs": uniqueWorkflowStrings(cited), "invalid_refs_stripped": stripped, "case_references_removed": removed, "model": stats["model"]}, all, nil
}

func (s *Server) researchCaps() M {
	return M{"max_subquestions": positive(s.cfg.ResearchMaxSubquestions, 8), "max_tool_calls": positive(s.cfg.ResearchMaxToolCalls, 24), "max_passages": positive(s.cfg.ResearchMaxPassages, 60), "concurrency": positive(s.cfg.ResearchConcurrency, 3), "followup_depth": s.cfg.ResearchFollowupDepth}
}
func (s *Server) planDeepResearch(ctx context.Context, user, question string) ([]M, error) {
	plan, _, err := s.planDeepResearchWithStats(ctx, user, question)
	return plan, err
}
func (s *Server) planDeepResearchWithStats(ctx context.Context, user, question string) ([]M, M, error) {
	cap := number(s.researchCaps(), "max_subquestions", 8)
	prompt := strings.Replace(str(researchPrompts, "plan"), "3-8 distinct", fmt.Sprintf("%d-%d distinct", min(3, cap), cap), 1)
	prompt = strings.ReplaceAll(prompt, "__QUESTION__", researchNeutralize(question))
	data, stats, err := s.structuredWithStats(ctx, user, legalSystem, prompt)
	if err != nil {
		return nil, nil, err
	}
	plan := normalizeResearchPlan(data, M{"documents": true, "case_law": true}, cap)
	if len(plan) == 0 {
		return nil, nil, fail(502, "The planner returned no usable sub-questions. Please try again.")
	}
	return plan, stats, nil
}

func (s *Server) runDeepResearch(ctx context.Context, user, question, matter string, plan []M, ids []string) (M, error) {
	var courtContacted atomic.Bool
	ctx = context.WithValue(ctx, courtContactKey{}, &courtContacted)
	caps := s.researchCaps()
	runner := &researchRunner{s: s, user: user, documentIDs: ids, byKey: map[string]M{}, passages: []M{}, maxCalls: number(caps, "max_tool_calls", 24), maxPassages: number(caps, "max_passages", 60), concurrency: number(caps, "concurrency", 3), followups: number(caps, "followup_depth", 1)}
	steps := []M{}
	for _, sub := range plan {
		steps = append(steps, M{"id": sub["id"], "question": sub["question"], "tools": sub["tools"], "status": "pending", "tool_calls": []M{}, "passages_found": 0, "followup_query": nil, "answer": "", "claims": []M{}, "quotes_verified": 0, "quotes_unverified": 0, "invalid_refs_stripped": 0, "error": nil, "duration_ms": 0})
	}
	gate := make(chan struct{}, runner.concurrency)
	var wg sync.WaitGroup
	for _, step := range steps {
		wg.Add(1)
		go func(step M) {
			defer wg.Done()
			select {
			case gate <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-gate }()
			runner.runStep(ctx, step)
		}(step)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	synthesis, passages, err := runner.synthesize(ctx, question, steps)
	if err != nil {
		return nil, err
	}
	byRef := map[string]M{}
	for _, p := range passages {
		byRef[str(p, "ref")] = p
	}
	treatment := M{}
	cites := []string{}
	checks := []M{}
	key, err := s.providerKey(ctx, user, "courtlistener", s.cfg.CourtListenerToken)
	if err != nil {
		return nil, err
	}
	if key != "" {
		for _, ref := range stringsOf(synthesis["cited_refs"]) {
			p := byRef[ref]
			cite := str(p, "citation")
			if str(p, "source_type") == "case_law" && cite != "" && !slices.Contains(cites, cite) && len(checks) < 8 {
				cites = append(cites, cite)
				checks = append(checks, M{"ref": ref, "citation": cite})
			}
		}
	}
	checkGate := make(chan struct{}, 3)
	var checkMu sync.Mutex
	for _, check := range checks {
		wg.Add(1)
		go func(check M) {
			defer wg.Done()
			select {
			case checkGate <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-checkGate }()
			result := s.checkResearchCitation(ctx, user, str(check, "citation"))
			checkMu.Lock()
			treatment[str(check, "ref")] = result
			checkMu.Unlock()
		}(check)
	}
	wg.Wait()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	sources := []M{}
	for _, ref := range stringsOf(synthesis["cited_refs"]) {
		p := byRef[ref]
		if p == nil {
			continue
		}
		entry := researchSourceEntry(p)
		verified := 0
		for _, step := range steps {
			for _, claim := range maps(step["claims"]) {
				if claim["verified"] == true && str(claim, "quote_ref") == ref {
					verified++
				}
			}
		}
		entry["quotes_verified"] = verified
		if scan := object(treatment[ref]); len(scan) > 0 {
			delete(scan, "citation")
			entry["treatment"] = scan
		}
		sources = append(sources, entry)
	}
	verified, unverified, invalid, failed := 0, 0, number(synthesis, "invalid_refs_stripped", 0), 0
	caseLaw := false
	for _, step := range steps {
		verified += number(step, "quotes_verified", 0)
		unverified += number(step, "quotes_unverified", 0)
		invalid += number(step, "invalid_refs_stripped", 0)
		if str(step, "status") == "failed" {
			failed++
		}
		for _, call := range maps(step["tool_calls"]) {
			if str(call, "tool") == "case_law" && call["error"] == nil {
				caseLaw = true
			}
		}
	}
	var inScope any
	if ids != nil {
		inScope = len(ids)
	}
	scope := M{"matter_id": nullable(matter), "document_ids": ids, "documents_in_scope": inScope, "case_law_searched": caseLaw, "courtlistener_contacted": courtContacted.Load(), "tool_calls": runner.used, "tool_call_cap": runner.maxCalls, "passages_collected": len(passages), "passages_dropped_over_cap": runner.dropped, "passages_cited": len(sources), "steps_failed": failed, "coverage_note": fmt.Sprintf("Answered from %s out of %d retrieved across %s and %s. Documents were searched, not read in full; opinions were read in the passage cited, not in full.", workflowPlural(len(sources), "cited passage"), len(passages), workflowPlural(len(plan), "sub-question"), workflowPlural(runner.used, "tool call"))}
	// Access can change while a provider is generating an answer, including
	// when the original request selected all readable documents (nil scope).
	for _, passage := range runner.forStep("") {
		if id := str(passage, "document_id"); id != "" {
			if _, err := s.getObject(ctx, user, "document", id); err != nil {
				return nil, fail(403, "A research source is deleted or no longer accessible")
			}
		}
	}
	return M{"question": question, "answer": synthesis["answer"], "key_findings": synthesis["key_findings"], "open_questions": synthesis["open_questions"], "sub_questions": steps, "sources": sources, "scope": scope, "verification": M{"quotes_verified": verified, "quotes_unverified": unverified, "invalid_refs_stripped": invalid, "case_references_removed": synthesis["case_references_removed"], "citations_checked": len(checks)}, "model": synthesis["model"], "notice": aiReviewNotice}, nil
}

func (s *Server) workflowDeepResearch(ctx context.Context, user string, w, run, step, outputs M, ids []string) (M, error) {
	p := object(step["params"])
	rendered := renderWorkflowTemplate(str(p, "question"), outputs)
	question := strings.Join(strings.Fields(str(rendered, "text")), " ")
	if len([]rune(question)) < 3 {
		return nil, bad("The research question is empty after substitution")
	}
	plan, err := s.planDeepResearch(ctx, user, question)
	if err != nil {
		return nil, err
	}
	allowed := stringsOf(p["tools"])
	if len(allowed) > 0 {
		for _, sub := range plan {
			tools := []string{}
			for _, tool := range stringsOf(sub["tools"]) {
				if slices.Contains(allowed, tool) {
					tools = append(tools, tool)
				}
			}
			if len(tools) == 0 {
				tools = allowed
			}
			sub["tools"] = tools
		}
	}
	var scopeIDs []string
	if len(ids) > 0 || str(run, "matter_id") != "" || str(object(step["inputs"]), "source") != "run" {
		scopeIDs = append([]string{}, ids...)
	}
	result, err := s.runDeepResearch(ctx, user, question, str(run, "matter_id"), plan, scopeIDs)
	if err != nil {
		return nil, err
	}
	id := uuid()
	result["run_id"] = id
	object(result["scope"])["workflow_run_id"] = run["id"]
	data := M{"question": question, "plan": plan, "status": "completed", "result": result, "scope": result["scope"], "error": nil, "completed_at": nowString(), "document_ids": ids}
	if err = s.saveJobObject(ctx, user, "research_run", id, str(run, "matter_id"), data); err != nil {
		return nil, err
	}
	sources := []M{}
	for _, source := range maps(result["sources"]) {
		sources = append(sources, M{"ref": source["ref"], "title": source["title"], "citation": source["citation"], "kind": source["type"], "url": source["url"]})
	}
	verification := object(result["verification"])
	summary := fmt.Sprintf("Researched %s; %s cited, %d quotes verified.", workflowPlural(len(plan), "sub-question"), workflowPlural(len(sources), "source"), number(verification, "quotes_verified", 0))
	return workflowStepOutput(summary, M{"run_id": id, "question": question, "answer": result["answer"], "key_findings": result["key_findings"], "open_questions": result["open_questions"], "sources": sources, "verification": verification, "template_values_used": rendered["used"], "template_values_missing": rendered["missing"]}, ids, M{"research_runs": []string{id}}), nil
}
