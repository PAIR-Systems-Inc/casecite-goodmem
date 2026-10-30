package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

func workflowStepOutput(summary string, data M, ids []string, produced M) M {
	if ids == nil {
		ids = []string{}
	}
	if produced == nil {
		produced = M{}
	}
	return M{"summary": summary, "data": data, "document_ids": ids, "produced": produced}
}
func workflowPlural(n int, word string) string {
	if n != 1 {
		word += "s"
	}
	return fmt.Sprintf("%d %s", n, word)
}
func workflowSafeError(err error) string {
	var ae *apiError
	if errors.As(err, &ae) {
		return charSlice(ae.message, 0, 300)
	}
	if notFound(err) {
		return "Document not found or no longer accessible"
	}
	return "The operation failed; check the server log and retry."
}

func (s *Server) executeWorkflowStep(ctx context.Context, user string, w, run, step, outputs M, products *workflowProducts) (M, error) {
	p := object(step["params"])
	ids, err := resolveWorkflowInputs(step, run, outputs)
	if err != nil {
		return nil, err
	}
	switch str(step, "kind") {
	case "extract_fields":
		if len(ids) == 0 {
			return nil, bad("No documents to extract from")
		}
		fields := maps(p["fields"])
		table := uuid()
		name := charSlice(strings.TrimSpace(fallback(str(p, "table_name"), str(w, "name")+" — "+str(step, "name"))), 0, 300)
		rows, values := M{}, M{}
		docs, fieldRefs := []M{}, []M{}
		counts := M{"extracted": 0, "unverified": 0, "not_found": 0, "errors": 0}
		for _, field := range fields {
			fieldRefs = append(fieldRefs, M{"key": field["key"], "name": field["name"]})
		}
		// Use the same bounded document concurrency as tabular review, then
		// assemble in input order. Products remain private until the step commits.
		type extracted struct {
			cells  map[string]M
			length int
		}
		extractions := make([]extracted, len(ids))
		queue := make(chan int, len(ids))
		for i := range ids {
			queue <- i
		}
		close(queue)
		var workers sync.WaitGroup
		concurrency := s.cfg.ReviewConcurrency
		if concurrency <= 0 {
			concurrency = 4
		}
		for range min(concurrency, len(ids)) {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for i := range queue {
					if ctx.Err() != nil {
						return
					}
					cells, length := s.extractReviewDocument(ctx, user, ids[i], fields)
					extractions[i] = extracted{cells, length}
				}
			}()
		}
		workers.Wait()
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		for i, id := range ids {
			cells, length := extractions[i].cells, extractions[i].length
			row, value := M{}, M{}
			for _, field := range fields {
				key := str(field, "key")
				result := cells[key]
				cell := newReviewCell(id, key)
				applyReviewModel(cell, result, s.cfg.LLMUtilityModel, length, user)
				products.cells[table] = append(products.cells[table], cell)
				row[key] = M{"value": result["value"], "status": result["status"], "verified": result["verified"], "quote": result["quote"]}
				value[key] = result["value"]
				status := str(result, "status")
				if status == "error" {
					status = "errors"
				}
				counts[status] = number(counts, status, 0) + 1
			}
			rows[id] = row
			values[id] = value
			doc, _ := s.getObject(ctx, user, "document", id)
			docs = append(docs, M{"id": id, "filename": fallback(str(doc, "filename"), id)})
		}
		data := M{"name": name, "description": "Created by workflow run " + str(run, "id"), "fields": fields, "document_ids": ids, "last_run_at": nowString()}
		if err = s.saveJobObject(ctx, user, "review_table", table, str(run, "matter_id"), data); err != nil {
			return nil, err
		}
		summary := fmt.Sprintf("Extracted %s from %s: %d verified, %d unverified, %d not found, %d failed.", workflowPlural(len(fields), "field"), workflowPlural(len(ids), "document"), number(counts, "extracted", 0), number(counts, "unverified", 0), number(counts, "not_found", 0), number(counts, "errors", 0))
		return workflowStepOutput(summary, M{"table_id": table, "fields": fieldRefs, "documents": docs, "rows": rows, "values": values, "counts": counts}, ids, M{"review_tables": []string{table}}), nil
	case "analyze_contract":
		if len(ids) == 0 {
			return nil, bad("No documents to analyse")
		}
		cap := min(len(ids), number(s.workflowCaps(), "max_documents", 25))
		skipped := append([]string{}, ids[cap:]...)
		ids = ids[:cap]
		analyses, failures := []M{}, []M{}
		analysisIDs := []string{}
		issueCount := 0
		for _, id := range ids {
			input := M{}
			mergeMap(input, p)
			input["document_id"] = id
			input["matter_id"] = run["matter_id"]
			result, e := s.analyzeContract(ctx, user, input)
			if e != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				failures = append(failures, M{"document_id": id, "error": workflowSafeError(e)})
				continue
			}
			issues := maps(result["issues"])
			severities := M{}
			briefs := []M{}
			for i, issue := range issues {
				severity := fallback(str(issue, "severity"), "minor")
				severities[severity] = number(severities, severity, 0) + 1
				if i < 25 {
					briefs = append(briefs, M{"ref": issue["ref"], "title": charSlice(str(issue, "title"), 0, 200), "severity": severity, "why": charSlice(str(issue, "why"), 0, 600), "grounding": issue["grounding"], "matched_text": nullable(charSlice(str(issue, "matched_text"), 0, 300)), "suggested_language": nullable(charSlice(str(issue, "suggested_language"), 0, 400))})
				}
			}
			analyses = append(analyses, M{"document_id": id, "filename": result["filename"], "analysis_id": result["analysis_id"], "contract_type": result["contract_type"], "issues": len(issues), "issues_by_severity": severities, "issue_list": briefs, "executive_summary": result["executive_summary"], "parties": result["parties"], "key_terms": result["key_terms"]})
			issueCount += len(issues)
			if aid := str(result, "analysis_id"); aid != "" {
				analysisIDs = append(analysisIDs, aid)
			}
		}
		if len(analyses) == 0 {
			detail := "no documents could be read"
			if len(failures) > 0 {
				detail = str(failures[0], "error")
			}
			return nil, bad("Contract analysis failed for every document: " + detail)
		}
		summary := fmt.Sprintf("Analysed %s, %s found.", workflowPlural(len(analyses), "contract"), workflowPlural(issueCount, "issue"))
		if len(failures) > 0 {
			summary += " " + workflowPlural(len(failures), "document") + " could not be analysed."
		}
		return workflowStepOutput(summary, M{"analyses": analyses, "failures": failures, "skipped_document_ids": skipped}, ids, M{"analyses": analysisIDs}), nil
	case "compare_contracts":
		baseline := str(p, "baseline_document_id")
		if baseline == "" && len(ids) > 0 {
			baseline = ids[0]
		}
		if baseline == "" {
			return nil, bad("No baseline document to compare against")
		}
		others := []string{}
		for _, id := range ids {
			if id != baseline && len(others) < 3 {
				others = append(others, id)
			}
		}
		if len(others) == 0 {
			return nil, bad("Comparing needs the baseline and at least one other document")
		}
		textA, docA, e := s.documentText(ctx, user, baseline)
		if e != nil {
			return nil, bad("Baseline document could not be read: " + workflowSafeError(e))
		}
		labelA := fallback(str(docA, "filename"), baseline)
		comparisons, failures := []M{}, []M{}
		differences := 0
		for _, id := range others {
			textB, docB, e := s.documentText(ctx, user, id)
			if e != nil {
				failures = append(failures, M{"document_id": id, "error": workflowSafeError(e)})
				continue
			}
			result := s.compareContractPair(ctx, user, textA, textB, labelA, fallback(str(docB, "filename"), id), str(p, "focus"))
			count := 0
			for _, clause := range maps(result["clauses"]) {
				if str(clause, "status") != "unchanged" {
					count++
				}
			}
			mergeMap(result, M{"document_id_a": baseline, "document_id_b": id, "differences": count})
			comparisons = append(comparisons, result)
			differences += count
		}
		if len(comparisons) == 0 {
			return nil, bad("No document could be compared against the baseline")
		}
		return workflowStepOutput(fmt.Sprintf("Compared %s against %s: %s flagged.", workflowPlural(len(comparisons), "document"), labelA, workflowPlural(differences, "clause difference")), M{"baseline_document_id": baseline, "focus": p["focus"], "comparisons": comparisons, "failures": failures}, append([]string{baseline}, others...), nil), nil
	case "draft_document":
		rendered := renderWorkflowTemplate(str(p, "instructions"), outputs)
		instructions := strings.TrimSpace(str(rendered, "text"))
		if len([]rune(instructions)) < 3 {
			return nil, bad("The drafting instructions are empty after substitution")
		}
		settings, e := s.userSettings(ctx, user)
		if e != nil {
			return nil, e
		}
		instructions = standingInstructions(settings, instructions)
		refs := []draftReference{}
		references := []M{}
		docIDs := []string{}
		for _, name := range stringsOf(p["include_outputs_from"]) {
			prior := workflowOutput(outputs, name)
			if prior == nil {
				return nil, bad(fmt.Sprintf("Step '%s' has no output to attach", name))
			}
			refs = append(refs, draftReference{Label: "Output of step " + name, Text: workflowOutputText(prior, 20000)})
			references = append(references, M{"kind": "step", "name": name})
		}
		for _, id := range uniqueWorkflowStrings(stringsOf(p["reference_document_ids"])) {
			text, doc, e := s.documentText(ctx, user, id)
			if e != nil {
				return nil, bad("Reference document could not be read: " + workflowSafeError(e))
			}
			refs = append(refs, draftReference{Label: str(doc, "filename"), Text: charSlice(text, 0, 20000)})
			references = append(references, M{"kind": "document", "id": id, "filename": doc["filename"]})
			docIDs = append(docIDs, id)
		}
		result, e := s.shortContractDraft(ctx, user, instructions, refs, "")
		if e != nil {
			return nil, e
		}
		data := M{"title": result["title"], "text": result["text"], "case_law_removed": result["case_law_removed"], "references": references, "template_values_used": rendered["used"], "template_values_missing": rendered["missing"]}
		return workflowStepOutput(fmt.Sprintf("Drafted \"%s\" (%s).", fallback(str(result, "title"), "Draft"), workflowPlural(len(strings.Fields(str(result, "text"))), "word")), data, docIDs, nil), nil
	case "deep_research":
		return s.workflowDeepResearch(ctx, user, w, run, step, outputs, ids)
	case "citation_check":
		if p["from_steps"] == nil {
			p = copyWorkflowMap(p)
			names := []string{}
			for _, prior := range maps(run["step_definitions"]) {
				name := str(prior, "name")
				if workflowOutput(outputs, name) != nil {
					names = append(names, name)
				}
			}
			p["from_steps"] = names
		}
		return s.workflowCitationCheck(ctx, user, p, outputs)
	}
	return nil, bad("Unknown workflow step kind")
}
