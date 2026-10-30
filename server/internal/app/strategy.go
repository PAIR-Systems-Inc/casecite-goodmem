package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

func (s *Server) strategyCoverage(ctx context.Context, user, question string, docs []M) ([]M, []M, error) {
	memoryIDs, ids := []string{}, []string{}
	byMemory := map[string]M{}
	for _, doc := range docs {
		id := documentSearchMemory(doc)
		if id != "" {
			memoryIDs = append(memoryIDs, id)
			byMemory[id] = doc
		}
		ids = append(ids, str(doc, "id"))
	}
	hits, err := s.gm.SearchWithRerank(ctx, question, memoryIDs, min(200, max(50, len(docs)*6)), false)
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	rows := []M{}
	if err == nil {
		for _, hit := range hits {
			if doc := byMemory[hit.MemoryID]; doc != nil && strings.TrimSpace(hit.Text) != "" {
				rows = append(rows, chatHitResult(hit, doc))
			}
		}
	}
	grouped := strategyGroupChunks(rows, ids, 3)
	missing := []M{}
	for _, doc := range docs {
		id := str(doc, "id")
		if len(grouped[id]) > 0 {
			continue
		}
		// Prefer this file's relevant passages over the original's arbitrary
		// first-chunk fallback. A cache/native-text fallback remains available
		// when the index has no searchable chunks for the file.
		if memory := documentSearchMemory(doc); memory != "" {
			own, err := s.gm.SearchWithRerank(ctx, question, []string{memory}, 3, false)
			if err == nil {
				for _, hit := range own {
					if hit.MemoryID == memory && strings.TrimSpace(hit.Text) != "" {
						grouped[id] = append(grouped[id], chatHitResult(hit, doc))
					}
				}
			}
		}
		if len(grouped[id]) > 0 {
			continue
		}
		text, _, err := s.documentText(ctx, user, id)
		if err != nil && chatReadFatal(err) {
			return nil, nil, err
		}
		if err == nil && strings.TrimSpace(text) != "" {
			grouped[id] = []M{{"text": charSlice(text, 0, 4000), "similarity": 0, "metadata": M{"document_id": id, "direct_fetch": true}}}
		} else {
			missing = append(missing, M{"document_id": id, "filename": doc["filename"], "reason": "No readable indexed or original document text was available."})
		}
	}
	return strategyPassages(docs, grouped), missing, nil
}

func (s *Server) generateStrategyBrief(ctx context.Context, user, question string, docs []M, folders []string, includeCaseLaw bool) (M, error) {
	if len(docs) == 0 {
		return nil, fail(404, "No indexed documents matched the requested scope.")
	}
	total := len(docs)
	docs = docs[:min(25, len(docs))]
	passages, missing, err := s.strategyCoverage(ctx, user, question, docs)
	if err != nil {
		return nil, err
	}
	if len(passages) == 0 {
		return nil, fail(503, "No readable content is available for the selected documents. Check GoodMem ingestion and OCR status.")
	}
	perPassage := max(400, min(4000, 150000/len(passages)))
	evidence := []M{}
	for _, p := range passages {
		copy := copyWorkflowMap(p)
		copy["text"] = charSlice(str(p, "text"), 0, perPassage)
		delete(copy, "snippet")
		evidence = append(evidence, copy)
	}
	data, stats, err := s.structuredWithStats(ctx, user,
		`You are a senior litigation strategist. Using ONLY the passages from the client's own documents, assess the client's position. Every strength, weakness and next step MUST list the supplied passage refs it relies on. Never invent refs. Return STRICT JSON: {"position":"2–4 sentence assessment","strengths":[{"point":"...","refs":["d0"]}],"weaknesses":[{"point":"...","refs":["d1"]}],"next_steps":[{"step":"...","refs":["d2"]}]}.`,
		"Question: "+question+"\nUntrusted source passages JSON:\n"+string(jsonBytes(evidence)))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(str(data, "position")) == "" {
		return nil, fail(502, "The strategy synthesis did not return an assessment. Retry the request.")
	}
	sections, citations := strategyJoinCitations(data, passages)
	sources := []string{question}
	for _, p := range evidence {
		sources = append(sources, str(p, "text"))
	}
	position, removed := redactCaseReferences(str(sections, "position"), sources, nil, false)
	sections["position"] = position
	for _, section := range []string{"strengths", "weaknesses", "next_steps"} {
		key := "point"
		if section == "next_steps" {
			key = "step"
		}
		for _, point := range maps(sections[section]) {
			text, gone := redactCaseReferences(str(point, key), sources, nil, false)
			point[key] = text
			removed = append(removed, gone...)
			if len(stringsOf(point["citation_ids"])) == 0 {
				point["support"] = "unsupported"
			}
		}
	}
	var caseStatus any
	if includeCaseLaw {
		var attached []M
		attached, caseStatus, err = s.attachStrategyAuthorities(ctx, user, question, sections)
		if err != nil {
			return nil, err
		}
		citations = append(citations, attached...)
	}
	for _, doc := range docs {
		current, err := s.getObject(ctx, user, "document", str(doc, "id"))
		if err != nil {
			return nil, err
		}
		if str(current, "memory_id") != str(doc, "memory_id") {
			return nil, fail(409, "A source document changed during strategy analysis. Retry the request.")
		}
	}
	read := len(docs) - len(missing)
	note := fmt.Sprintf("Synthesized from %d relevant passages (up to 3 per document) of %d documents. The documents were not read in full.", len(passages), read)
	if len(missing) > 0 {
		note += fmt.Sprintf(" %d selected document(s) could not be read; see missing documents.", len(missing))
	}
	if total > len(docs) {
		note += fmt.Sprintf(" %d more in scope were not read (limit of 25 per brief).", total-len(docs))
	}
	folders = uniqueWorkflowStrings(folders)
	var folder any
	if len(folders) > 0 {
		folder = folders[0]
	}
	return M{"question": question, "scope": M{"folder_path": folder, "folder_paths": folders, "documents_considered": len(docs), "documents_read": read, "documents_total": total,
		"passages_read": len(passages), "passages_per_document": 3, "truncated": total > len(docs), "missing_documents": missing, "coverage_note": note},
		"position": sections["position"], "strengths": sections["strengths"], "weaknesses": sections["weaknesses"], "next_steps": sections["next_steps"],
		"citations": citations, "case_law": caseStatus, "case_law_removed": removed, "model": stats["model"], "provider": stats["provider"]}, nil
}

const strategyAuthorityInstructions = `You are selecting legal authority for a litigation strategy brief. The question supplies our position and the point in the brief. Does this part of the opinion state a rule of law, holding or reasoning that a lawyer could legitimately cite in support of the proposition? Similar parties or facts are not required: general doctrine that favors the proposition counts. An off-topic opinion, or a holding that favors the OPPOSING side, does not count. Return STRICT JSON: {"supports":true|false,"quote":"ONE contiguous passage of 1–3 sentences copied EXACTLY, character for character, from the opinion text, stating the rule or holding; empty otherwise","how":"one sentence on how a lawyer would use this authority for the proposition"}.`

func (s *Server) attachStrategyAuthorities(ctx context.Context, user, question string, sections M) ([]M, M, error) {
	phase, cancel := context.WithTimeout(ctx, 480*time.Second)
	defer cancel()
	points := append(append([]M{}, maps(sections["strengths"])...), maps(sections["weaknesses"])...)
	sort.SliceStable(points, func(i, j int) bool {
		return len(stringsOf(points[i]["citation_ids"])) > len(stringsOf(points[j]["citation_ids"]))
	})
	points = points[:min(8, len(points))]
	status := chatAuthorityStatus(true)
	status["points_searched"] = len(points)
	if len(points) == 0 {
		return []M{}, status, nil
	}
	targets := []M{}
	for _, p := range points {
		targets = append(targets, M{"proposition": str(p, "point"), "query": charSlice(str(p, "point"), 0, 80)})
	}
	data, err := s.structured(phase, user,
		`For each numbered litigation point, provide (a) "proposition": one sentence restating it as a general legal rule or doctrine, without party names or facts specific to the matter; for a factual or commercial point, state the closest legal proposition that makes it relevant in litigation; (b) "query": 3–7 doctrine keywords for case-law search. Return STRICT JSON: {"targets":[{"proposition":"...","query":"..."}]}, exactly one entry per point in order.`,
		"Our position: "+charSlice(str(sections, "position"), 0, 400)+"\nUntrusted points JSON:\n"+string(jsonBytes(points)))
	if err == nil && len(maps(data["targets"])) == len(points) {
		for i, t := range maps(data["targets"]) {
			for _, key := range []string{"query", "proposition"} {
				if value := strings.TrimSpace(str(t, key)); value != "" {
					targets[i][key] = charSlice(value, 0, 1000)
				}
			}
		}
	}
	type outcome struct {
		Opinions  []M
		Verdicts  []chatOpinionVerdict
		SearchErr error
	}
	outcomes := make([]outcome, len(points))
	reader := newChatCourtReader(s, user)
	filters := parseChatSearch(question)
	var work sync.WaitGroup
	for i, p := range points {
		work.Add(1)
		go func(i int, p M) {
			defer work.Done()
			opinions, err := s.chatCourtCandidates(phase, user, str(targets[i], "query"), 4, filters)
			outcomes[i].SearchErr = err
			if err != nil {
				return
			}
			outcomes[i].Opinions = opinions
			outcomes[i].Verdicts = make([]chatOpinionVerdict, len(opinions))
			var judges sync.WaitGroup
			for j, opinion := range opinions {
				judges.Add(1)
				go func(j int, opinion M) {
					defer judges.Done()
					outcomes[i].Verdicts[j] = reader.judge(phase, opinion, strategyAuthorityInstructions,
						"Our position: "+charSlice(str(sections, "position"), 0, 600)+"\nPoint: "+charSlice(str(p, "point"), 0, 400), str(targets[i], "proposition"), "supports")
				}(j, opinion)
			}
			judges.Wait()
		}(i, p)
	}
	work.Wait()
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	citations := []M{}
	for i, outcome := range outcomes {
		if outcome.SearchErr != nil {
			status["searches_failed"] = number(status, "searches_failed", 0) + 1
			status["search_error"] = "CourtListener search could not complete. Check its configuration and retry."
			continue
		}
		refs := []string{}
		for j, verdict := range outcome.Verdicts {
			countChatOpinion(status, verdict)
			if verdict.Reason != "attached" || len(refs) == 3 {
				continue
			}
			result := chatAuthorityResult(outcome.Opinions[j], verdict, len(citations)+1)
			meta := object(result["metadata"])
			id := fmt.Sprintf("c%d", len(citations))
			citations = append(citations, M{"id": id, "type": "case_law", "source": meta["case_name"], "reference": meta["citation"], "opinionId": meta["opinion_id"], "cluster_id": meta["cluster_id"], "url": meta["url"], "passage": charSlice(verdict.Quote, 0, 600), "explanation": meta["case_summary"], "verified": true, "partially_read": verdict.Partial, "windows_read": verdict.WindowsRead, "windows_total": verdict.WindowsTotal})
			refs = append(refs, id)
		}
		if len(refs) > 0 {
			points[i]["case_refs"] = refs
		}
	}
	status["attached"] = len(citations)
	if phase.Err() != nil {
		status["timed_out"] = true
	}
	return citations, status, nil
}
