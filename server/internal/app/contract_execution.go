package app

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

func (s *Server) executeContract(ctx context.Context, user, kind string, input M) (M, error) {
	if err := s.resultAccess(ctx, user, input); err != nil {
		return nil, err
	}
	account, err := s.loadUser(ctx, user)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(account.Permissions, "contracts.use") {
		return nil, fail(403, "Contract access is no longer available")
	}
	if kind == "contract.analysis" {
		return s.analyzeContract(ctx, user, input)
	}
	if kind == "contract.compare" {
		ids := stringsOf(input["document_ids"])
		if len(ids) < 2 || len(ids) > 4 {
			return nil, bad("Select two to four different documents")
		}
		texts, labels := []string{}, []string{}
		for _, id := range ids {
			text, doc, err := s.documentText(ctx, user, id)
			if err != nil {
				return nil, err
			}
			texts = append(texts, text)
			labels = append(labels, fallback(str(doc, "filename"), id))
		}
		comparisons := []M{}
		for i := 1; i < len(ids); i++ {
			result := s.compareContractPair(ctx, user, texts[0], texts[i], labels[0], labels[i], str(input, "focus"))
			result["kind"] = "comparison"
			result["document_id_a"] = ids[0]
			result["document_id_b"] = ids[i]
			result["focus"] = input["focus"]
			comparisons = append(comparisons, result)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if len(comparisons) == 1 {
			return comparisons[0], nil
		}
		return M{"kind": "comparison_set", "comparisons": comparisons, "focus": input["focus"]}, nil
	}
	settings, err := s.userSettings(ctx, user)
	if err != nil {
		return nil, err
	}
	instructions := standingInstructions(settings, fallback(str(input, "instructions"), str(input, "message")))
	refs, err := s.resolveDraftReferences(ctx, user, input)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "contract.plan":
		s.jobProgress(ctx, str(input, "job_id"), "Reading the reference documents and planning the sections…", 0.05)
		return s.planContractDraft(ctx, user, instructions, refs, number(input, "target_pages", 1))
	case "contract.generate":
		return s.generateContractDraft(ctx, user, instructions, refs, input)
	case "contract.draft":
		var result M
		if current := str(input, "draft_text"); current != "" {
			result, err = s.reviseContractDraft(ctx, user, current, instructions, str(input, "job_id"))
		} else {
			if id := str(input, "document_id"); id != "" {
				text, _, e := s.documentText(ctx, user, id)
				if e != nil {
					return nil, e
				}
				refs = append([]draftReference{{"Selected contract", text}}, refs...)
			}
			result, err = s.shortContractDraft(ctx, user, instructions, refs, "")
		}
		if err != nil {
			return nil, err
		}
		result["kind"] = "draft"
		return result, nil
	}
	return nil, bad("Unsupported contract task")
}
func (s *Server) compareContractPair(ctx context.Context, user, a, b, labelA, labelB, focus string) M {
	failed := M{"overall": "Comparison failed.", "clauses": []M{}, "label_a": labelA, "label_b": labelB}
	prompt := "Document A is " + labelA + "; Document B is " + labelB + ".\nUSER'S COMPARISON INSTRUCTIONS: " + focus + "\n\n" + comparisonBlock(labelA, comparisonSections(a), 30000) + "\n\n" + comparisonBlock(labelB, comparisonSections(b), 30000)
	data, err := s.structured(ctx, user, `Compare contracts clause by clause, aligning by TOPIC rather than numbering. Identify changed, added (B only), removed (A only), or unchanged topics. Explain material changes and which party they favor in one sentence. Skip identical boilerplate unless its stability is notable. Each quote must be one contiguous exact passage from its document, or empty when absent. Return {"overall":"2–3 sentence summary","clauses":[{"topic":"...","status":"changed","summary":"...","quote_a":"...","quote_b":"..."}]}.`, prompt)
	if err != nil {
		s.log.Warn("contract comparison failed", "error", err)
		return failed
	}
	clauses := []M{}
	raw := maps(data["clauses"])
	for _, item := range raw[:min(40, len(raw))] {
		topic := strings.TrimSpace(str(item, "topic"))
		if topic == "" {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(str(item, "status")))
		if !slices.Contains([]string{"changed", "added", "removed", "unchanged"}, status) {
			status = "changed"
		}
		clause := M{"topic": topic, "status": status, "summary": strings.TrimSpace(str(item, "summary"))}
		for _, side := range []struct{ key, text string }{{"a", a}, {"b", b}} {
			quote := strings.TrimSpace(str(item, "quote_"+side.key))
			clause["quote_"+side.key] = nil
			clause["span_"+side.key+"_start"] = nil
			clause["span_"+side.key+"_end"] = nil
			clause["verified_"+side.key] = nil
			if quote == "" {
				continue
			}
			clause["verified_"+side.key] = false
			if start, end, ok := findQuote(side.text, quote); ok {
				clause["quote_"+side.key] = charSlice(side.text[start:end], 0, 600)
				clause["span_"+side.key+"_start"] = byteToUTF16(side.text, start)
				clause["span_"+side.key+"_end"] = byteToUTF16(side.text, end)
				clause["verified_"+side.key] = true
			}
		}
		clauses = append(clauses, clause)
	}
	overall := strings.TrimSpace(str(data, "overall"))
	if overall == "" && len(clauses) == 0 {
		return failed
	}
	return M{"overall": fallback(overall, "No material differences identified."), "clauses": clauses, "label_a": labelA, "label_b": labelB}
}
func (s *Server) answerContract(ctx context.Context, user, text, message string) (M, error) {
	data, err := s.structured(ctx, user, `Answer the lawyer's request using ONLY the contract text. Ground every claim in exact supporting passages, copied contiguously as 1–3 sentences. If the contract does not address the request, say so; never guess. Reference citations inline as [1], [2]. Return {"answer":"...","citations":["exact quote", "exact quote"]}.`, "REQUEST: "+charSlice(message, 0, 2000)+"\nCONTRACT TEXT (untrusted evidence):\n"+charSlice(text, 0, 120000))
	if err != nil {
		return nil, err
	}
	answer, citations := verifiedContractCitations(text, strings.TrimSpace(str(data, "answer")), data["citations"])
	answer, removed := redactCaseReferences(answer, []string{text, message}, nil, false)
	return M{"type": "answer", "answer": fallback(answer, "The contract does not address this."), "citations": citations, "case_law_removed": removed}, nil
}

var contractCitationMarker = regexp.MustCompile(`\[(\d+)\]`)

func verifiedContractCitations(text, answer string, raw any) (string, []M) {
	quotes := []any{}
	switch values := raw.(type) {
	case []any:
		quotes = values
	case []string:
		for _, quote := range values {
			quotes = append(quotes, quote)
		}
	}
	citations := []M{}
	indices := map[int]int{}
	for i, rawQuote := range quotes[:min(8, len(quotes))] {
		quote, ok := rawQuote.(string)
		if !ok {
			continue
		}
		if start, end, ok := findQuote(text, strings.TrimSpace(quote)); ok {
			citations = append(citations, mergeMap(M{"quote": charSlice(text[start:end], 0, 600)}, spanFields(text, start, end)))
			indices[i+1] = len(citations)
		}
	}
	// The Python implementation drops invalid quotes but leaves old [n]
	// markers behind. Re-map them once, so [2] can never point at the wrong
	// surviving source and an unsupported marker is visibly unverified.
	answer = contractCitationMarker.ReplaceAllStringFunc(answer, func(marker string) string {
		n, _ := strconv.Atoi(marker[1 : len(marker)-1])
		if mapped, ok := indices[n]; ok {
			return "[" + strconv.Itoa(mapped) + "]"
		}
		return "[unverified citation]"
	})
	return answer, citations
}
func (s *Server) routeContractMessage(ctx context.Context, user, message string) (string, string) {
	data, err := s.structured(ctx, user, `Classify a lawyer's message about a specific contract. analyze: full review, overall risks, run checks. redline: propose edits, markup, fix problems or prepare a turn. draft: write a NEW document such as a response letter, amendment or termination notice. ask: targeted questions, clause checks, summaries. Restate HOW to review/draft (represented side, what to flag/ignore, intended terms) as instructions; otherwise instructions is empty. Return {"intent":"analyze|redline|draft|ask","instructions":"..."}.`, "MESSAGE:\n"+charSlice(message, 0, 2000))
	if err != nil || !slices.Contains([]string{"analyze", "redline", "draft", "ask"}, str(data, "intent")) {
		return "ask", ""
	}
	return str(data, "intent"), strings.TrimSpace(str(data, "instructions"))
}
