package app

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func chatSystemPrompt(mode, intent string, settings M) string {
	defaults := defaultPrompts()
	if intent == "factual" {
		return fallback(str(settings, "custom_factual_prompt"), str(defaults, "factual_prompt")) + "\n" + legalSystem
	}
	system := fallback(str(settings, "custom_system_prompt"), str(defaults, "system_prompt"))
	modePrompt := fallback(str(settings, "custom_"+mode+"_prompt"), str(object(defaults["mode_prompts"]), mode))
	return system + "\n\n" + modePrompt + "\n\n" + legalSystem
}

func (s *Server) pooledChatAnswer(ctx context.Context, user, query, mode, caseQuery string, docs []M, explicit, includeDocuments, includeCases, deepCases bool, caseLimit int, settings M) (M, error) {
	started := time.Now()
	intent := chatQueryIntent(query, mode)
	judge, _ := ctx.Value(judgeChatKey{}).(*judgeChatEvidence)
	warnings := []string{}
	if includeDocuments && explicit && len(docs) == 0 && judge == nil {
		return M{"content": "There are no indexed documents in the selected scope. Check their ingestion and OCR status in the knowledge base.", "citations": []M{}, "stats": chatStats(0, 0, intent), "access_document_ids": []string{}}, nil
	}
	results := []M{}
	var err error
	if includeDocuments {
		results, warnings, err = s.retrieveChatDocuments(ctx, user, query, docs, explicit, settings)
		if err != nil {
			return nil, err
		}
	}
	cases := []M{}
	var authorityStatus M
	if includeCases {
		if deepCases {
			cases, authorityStatus, err = s.researchChatAuthorities(ctx, user, query, caseQuery, caseLimit)
			if err != nil {
				return nil, err
			}
			if notice := chatAuthorityNotice(authorityStatus); notice != "" {
				warnings = append(warnings, notice)
			}
		}
		if len(cases) == 0 {
			var notes []string
			cases, notes, err = s.searchChatCases(ctx, user, query, caseQuery, caseLimit)
			if err != nil {
				return nil, err
			}
			warnings = append(warnings, notes...)
		}
	}
	if len(results)+len(cases) == 0 && (includeDocuments || includeCases) && judge == nil {
		message := "No supporting passages or case-law results were found for this question."
		if includeDocuments && len(docs) == 0 {
			message = "There are no indexed documents available in this scope. Upload documents or check ingestion and OCR status in the knowledge base."
		}
		if explicit && includeDocuments {
			message = "I searched the selected files but found no passages to answer from. Check their ingestion and OCR status in the knowledge base."
		}
		if len(warnings) > 0 {
			message += "\n\n" + strings.Join(uniqueWorkflowStrings(warnings), "\n")
		}
		return M{"content": message, "citations": []M{}, "stats": chatStats(len(docs), 0, intent), "access_document_ids": []string{}}, nil
	}
	promptResults := results
	if settings["context_compression"] == true {
		promptResults = compressChatResults(results, query, 4)
	}
	evidence := []M{}
	sources := []string{}
	ids := []string{}
	for i, result := range promptResults {
		meta := object(result["metadata"])
		evidence = append(evidence, M{"number": i + 1, "source": meta["filename"], "type": "document", "document_id": meta["document_id"], "passage": result["text"]})
	}
	for _, result := range results {
		sources = append(sources, str(result, "text"))
		ids = append(ids, str(object(result["metadata"]), "document_id"))
	}
	for i, result := range cases {
		evidence = append(evidence, M{"number": len(results) + i + 1, "type": "case_law", "source": result["metadata"], "passage": result["text"]})
	}
	defaults := defaultPrompts()
	rules := fallback(str(settings, "custom_grounding_rules"), str(defaults, "grounding_rules")) + "\n" + str(defaults, "document_reading_rules")
	prompt := "Question: " + query
	if judge != nil {
		prompt += judgeChatPrompt(judge)
		if includeDocuments && len(results) == 0 {
			warnings = append(warnings, "No document passages were found in the selected scope; this answer uses the judge profile only.")
		}
	}
	if len(evidence) > 0 {
		prompt += "\n\nUntrusted source evidence JSON:\n" + string(jsonBytes(evidence)) + "\n\n" + rules
		if intent == "factual" {
			prompt += "\nAnswer directly and concisely. Reference the source document by name."
		}
		prompt += "\nOnly cite a case if it directly supports the analysis. Use its full case name. Never cite cases or reporter citations from memory. Case references absent from the supplied sources are removed."
	}
	// The user's output budget applies to factual answers too. The original
	// factual path silently replaced it with the model's much larger ceiling.
	system := chatSystemPrompt(mode, intent, settings)
	options := llmOptions{Model: str(settings, "llm_model"), MaxTokens: number(settings, "max_tokens", 4096)}
	if judge != nil {
		system += "\n\n" + judgeChatInstructions(judge.Task)
	} else if !includeDocuments && !includeCases && chatIsDrafting(query) {
		system = chatDraftingPrompt + "\n" + legalSystem
		temperature := max(0.3, judgeNumber(settings, "temperature"))
		options.Temperature = &temperature
	}
	answer, modelStats, err := s.llm(withLLMOptions(ctx, options), user, system, prompt, false)
	if err != nil {
		return nil, err
	}
	knownCases := cases
	if judge != nil {
		knownCases = append(append([]M{}, cases...), judge.References...)
		// Biographical sections can name cases absent from the sampled opinion
		// list. They are still server-held source references, not client claims.
		sources = append(sources, string(jsonBytes(judge.Profile)))
	}
	answer, removed := redactCaseReferences(answer, sources, knownCases, false)
	if len(removed) > 0 {
		warnings = append(warnings, "Case references absent from the supplied sources were removed.")
	}
	citations := chatDocumentCitations(results, query)
	if settings["citation_verification"] == true {
		verifyChatReferences(citations, answer)
	} else {
		for _, c := range citations {
			c["was_cited_by_ai"] = false
		}
	}
	grounded, err := s.groundChatAnswer(ctx, user, query, answer, results, settings["citation_verification"] == true)
	if err != nil {
		return nil, err
	}
	groundedCitations := false
	if grounded != nil {
		if len(grounded.Citations) > 0 {
			citations = grounded.Citations
			groundedCitations = true
		}
		if notice := grounded.Notice(); notice != "" {
			warnings = append(warnings, notice)
		}
	}
	caseCitations := chatCaseCitations(cases, answer, query, len(results))
	if groundedCitations {
		err = s.enrichChatCitations(ctx, user, query, answer, caseCitations)
	} else {
		citations = append(citations, caseCitations...)
		caseCitations = nil
		err = s.enrichChatCitations(ctx, user, query, answer, citations)
	}
	if err != nil {
		return nil, err
	}
	citations = append(citations, caseCitations...)
	if settings["source_tracking"] == true && !groundedCitations {
		if err = s.trackChatSources(ctx, user, citations); err != nil {
			return nil, err
		}
	}
	ids = uniqueWorkflowStrings(ids)
	if err = s.resultAccess(ctx, user, M{"access_document_ids": ids}); err != nil {
		return nil, err
	}
	if len(warnings) > 0 {
		answer += "\n\n" + strings.Join(uniqueWorkflowStrings(warnings), "\n\n")
	}
	stats := chatStats(0, len(results), intent)
	if includeDocuments {
		stats["docs_searched"] = len(docs)
	}
	stats["case_law_searched"], stats["model"], stats["provider"] = len(cases), modelStats["model"], modelStats["provider"]
	used := 0
	for _, c := range citations {
		if c["type"] == "case_law" && c["was_cited_by_ai"] == true {
			used++
		}
	}
	stats["case_law_included"], stats["processing_time"] = used, fmt.Sprintf("%.2fs", time.Since(started).Seconds())
	return M{"content": answer, "citations": citations, "stats": stats, "case_law_status": authorityStatus, "access_document_ids": ids}, nil
}

const chatDraftingPrompt = `You are an expert legal document drafter. Your role is to write complete, properly formatted legal documents.

IMPORTANT RULES:
- Write the document body only — no preamble, commentary, or explanation before or after it (the product adds its own review notice)
- Use [BRACKETED PLACEHOLDERS] for facts you don't know
- Write professionally formatted legal documents following standard conventions
- Start directly with the document content (caption, title, etc.)`

func chatIsDrafting(query string) bool {
	query = strings.ToLower(query)
	for _, keyword := range []string{"draft", "generate a", "write a", "motion to", "complaint", "document drafter", "begin the document", "ready-to-file"} {
		if strings.Contains(query, keyword) {
			return true
		}
	}
	return false
}
