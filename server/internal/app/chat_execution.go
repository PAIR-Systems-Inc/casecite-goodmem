package app

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (s *Server) loadChatScope(ctx context.Context, user string, scope M) ([]M, error) {
	docs := []M{}
	for _, id := range stringsOf(scope["ids"]) {
		doc, err := s.getObject(ctx, user, "document", id)
		if err != nil {
			return nil, err
		}
		if str(doc, "status") != "indexed" || str(doc, "memory_id") != str(object(scope["revisions"]), id) {
			return nil, fail(409, "A selected document changed or is no longer indexed. Submit the question again.")
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

func strategyChatCitations(brief M) []M {
	result := []M{}
	for i, source := range maps(brief["citations"]) {
		citation := chatCitation(fallback(str(source, "source"), fmt.Sprintf("Source %d", i+1)), fallback(str(source, "type"), "document"), str(source, "passage"), i+1)
		citation["id"] = fallback(str(source, "id"), fmt.Sprintf("strat-%d", i))
		confidence := judgeNumber(source, "confidence")
		citation["confidence"], citation["similarity"] = confidence, confidence
		if confidence > 1 {
			citation["similarity"] = confidence / 100
		}
		citation["chunk_index"], citation["case_summary"] = i, source["explanation"]
		citation["logic"] = M{"query_intent": "", "matching_criteria": "Strategy brief coverage retrieval", "application": ""}
		for _, key := range []string{"document_id", "url", "verified"} {
			citation[key] = source[key]
		}
		citation["cluster_id"] = nullable(chatSourceID(source["cluster_id"]))
		citation["opinion_id"] = nullable(fallback(chatSourceID(source["opinion_id"]), chatSourceID(source["opinionId"])))
		citation["was_cited_by_ai"] = true
		result = append(result, citation)
	}
	return result
}

func chatSourceID(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func (s *Server) executeChat(ctx context.Context, user, kind string, in M) (M, error) {
	started := time.Now()
	settings := object(in["_chat_settings"])
	ctx = context.WithValue(ctx, chatSettingsKey{}, settings)
	judge, err := s.loadJudgeChat(ctx, user, in)
	if err != nil {
		return nil, err
	}
	if judge != nil {
		ctx = context.WithValue(ctx, judgeChatKey{}, judge)
	}
	scope := object(in["_chat_scope"])
	query := str(in, "query")
	strategyEndpoint := kind == "strategy.brief"
	if strategyEndpoint {
		query = str(in, "question")
	}
	if !strategyEndpoint && in["_chat_new_session"] != true {
		if _, err := s.getObject(ctx, user, "chat_session", str(in, "session_id")); err != nil {
			return nil, err
		}
	}
	route := chatRoute{Intent: "strategy"}
	if judge != nil {
		route = chatRoute{Intent: "ask"}
	} else if !strategyEndpoint {
		route, err = s.routeChatQuery(ctx, user, query)
		if err != nil {
			return nil, err
		}
	}
	includeCases := route.UseCaseLaw
	if value, ok := in["include_case_law"].(bool); ok {
		includeCases = value
	} else if route.Intent == "research" {
		includeCases = true
	}
	includeDocuments := strategyEndpoint || in["include_documents"] == true && route.Intent != "research"
	docs := []M{}
	if includeDocuments {
		docs, err = s.loadChatScope(ctx, user, scope)
		if err != nil {
			return nil, err
		}
	}
	var result, brief M
	accessIDs := []string{}
	notices := []string{}
	if strategyEndpoint || route.Intent == "strategy" && includeDocuments && len(docs) > 0 {
		brief, err = s.generateStrategyBrief(ctx, user, query, docs, stringsOf(object(in["document_filter"])["folder_paths"]), includeCases)
		if err != nil {
			if strategyEndpoint || chatReadFatal(err) {
				return nil, err
			}
			notices = append(notices, "The strategy brief could not be completed. This answer uses retrieved passages and does not claim to review every selected document.")
		} else {
			for _, doc := range docs[:min(25, len(docs))] {
				accessIDs = append(accessIDs, str(doc, "id"))
			}
			citations := strategyChatCitations(brief)
			stats := chatStats(number(object(brief["scope"]), "documents_read", 0), len(citations), "strategy")
			stats["case_law_searched"] = number(object(brief["case_law"]), "opinions_read", 0)
			stats["case_law_included"] = number(object(brief["case_law"]), "attached", 0)
			stats["model"], stats["provider"] = brief["model"], brief["provider"]
			result = M{"content": fallback(str(brief, "position"), "Strategy brief generated."), "citations": citations, "stats": stats}
		}
	}
	if result == nil && route.Intent == "ask" && route.PerFile && includeDocuments && scope["explicit"] == true && len(docs) >= 2 && len(docs) <= 8 {
		result, err = s.perFileChatAnswer(ctx, user, query, docs)
		if err != nil {
			if chatReadFatal(err) {
				return nil, err
			}
			notices = append(notices, "The per-file comparison could not be completed. This answer uses retrieved passages and may not cover every selected file.")
		} else {
			for _, doc := range docs {
				accessIDs = append(accessIDs, str(doc, "id"))
			}
		}
	}
	if result == nil {
		result, err = s.pooledChatAnswer(ctx, user, query, str(in, "mode"), route.SearchQuery, docs, scope["explicit"] == true, includeDocuments, includeCases, route.Intent == "research", number(in, "case_law_limit", 5), settings)
		if err != nil {
			return nil, err
		}
		accessIDs = stringsOf(result["access_document_ids"])
	}
	accessIDs = uniqueWorkflowStrings(accessIDs)
	in["access_document_ids"] = accessIDs // final worker ACL check uses only sources actually read
	if strategyEndpoint {
		brief["access_document_ids"] = accessIDs
		if err := s.stageChatPublication(ctx, &chatExchange{User: user, Revisions: object(scope["revisions"]), Answer: brief}); err != nil {
			return nil, err
		}
		s.jobAudit(ctx, user, "strategy.brief", "strategy", str(in, "job_id"), M{"documents_read": object(brief["scope"])["documents_read"], "include_case_law": includeCases})
		return brief, nil
	}
	if len(notices) > 0 {
		result["content"] = str(result, "content") + "\n\n" + strings.Join(notices, "\n\n")
	}
	if in["include_citations"] != true {
		result["citations"] = []M{}
	}
	object(result["stats"])["processing_time"] = fmt.Sprintf("%.2fs", time.Since(started).Seconds())
	result["id"], result["mode"], result["timestamp"] = uuid(), in["mode"], time.Now().UTC().Format(time.RFC3339Nano)
	result["strategy"], result["session_id"], result["access_document_ids"] = brief, in["session_id"], accessIDs
	exchange := &chatExchange{User: user, Session: str(in, "session_id"), Matter: str(in, "_chat_session_matter"), Personal: in["_chat_session_personal"] == true, NewSession: in["_chat_new_session"] == true, Query: query, Answer: result, Revisions: object(scope["revisions"])}
	if judge != nil {
		exchange.JudgeID, exchange.JudgeVersion = judge.ID, judge.Version
		result["judge_context"] = M{"id": judge.ID, "version": judge.Version, "task": judge.Task}
	}
	if err := s.stageChatPublication(ctx, exchange); err != nil {
		return nil, err
	}
	s.jobAudit(ctx, user, "chat.query", "chat", str(result, "id"), M{"mode": in["mode"], "query_length": len([]rune(query)), "citations_count": len(maps(result["citations"])), "route": route.Intent})
	return result, nil
}
