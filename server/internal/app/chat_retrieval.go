package app

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

func chatHitResult(hit Hit, doc M) M {
	meta := copyWorkflowMap(object(doc["metadata"]))
	for k, v := range hit.Metadata {
		meta[k] = v
	}
	// Source identity always comes from the authorized application document,
	// including when the searchable GoodMem memory is its OCR derivative.
	meta["document_id"], meta["filename"], meta["chunk_index"] = doc["id"], doc["filename"], hit.Sequence
	meta["source"], meta["created_at"] = doc["source"], doc["created_at"]
	meta["doc_type"] = fallback(str(meta, "doc_type"), "document")
	// GoodMem's retrieve stage is weighted negative inner product. Preserve
	// the raw score and its meaning; this is not a probability of correctness.
	similarity := max(0, min(1, -hit.Score))
	return M{"id": hit.ID, "text": hit.Text, "metadata": meta, "similarity": similarity,
		"raw_score": hit.Score, "score_stage": hit.Stage, "score_kind": "inner_product",
		"memory_id": hit.MemoryID, "token_count": len(strings.Fields(hit.Text))}
}

func spreadChatDocuments(ranked []M, ids []string, count int) []M {
	if len(ids) <= 1 || len(ids) > count {
		return ranked[:min(count, len(ranked))]
	}
	floor := max(1, count/(2*len(ids)))
	perDoc := map[string]int{}
	allowed := map[string]bool{}
	chosen := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	for _, r := range ranked {
		id := str(object(r["metadata"]), "document_id")
		if allowed[id] && perDoc[id] < floor {
			chosen[chatResultKey(r)] = true
			perDoc[id]++
		}
	}
	for _, r := range ranked {
		if len(chosen) >= count {
			break
		}
		chosen[chatResultKey(r)] = true
	}
	out := []M{}
	for _, r := range ranked {
		if chosen[chatResultKey(r)] {
			out = append(out, r)
		}
	}
	return out[:min(count, len(out))]
}

func (s *Server) retrieveChatDocuments(ctx context.Context, user, query string, docs []M, explicit bool, settings M) ([]M, []string, error) {
	warnings := []string{}
	if len(docs) == 0 {
		return []M{}, warnings, nil
	}
	parsed := parseChatSearch(query)
	queries := []string{parsed.Semantic()}
	if settings["query_expansion"] == true {
		raw, _, err := s.llm(withLLMOptions(ctx, llmOptions{Utility: true, MaxTokens: 200}), user,
			"You rewrite a legal search query into alternative phrasings that surface the same underlying documents. Use synonyms and related legal terminology.",
			"Original query: "+parsed.Semantic()+"\n\nWrite up to 2 alternative search queries, one per line, no numbering or commentary.", false)
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if err == nil {
			queries = chatExpandedQueries(parsed.Semantic(), raw)
		} else {
			warnings = append(warnings, "Query expansion was unavailable; searched the original question.")
		}
	}
	count := max(1, min(100, number(settings, "top_k", 10)))
	lists := [][]M{}
	for _, text := range queries {
		results, notes, err := s.searchChatDocuments(ctx, text, parsed, docs, explicit, settings, count)
		if err != nil {
			return nil, nil, err
		}
		lists = append(lists, results)
		warnings = append(warnings, notes...)
	}
	results := lists[0]
	if len(lists) > 1 {
		results = mergeChatResults(lists, count)
	}
	// Expansion must not undo the selected-file coverage guarantee.
	if explicit {
		ids := []string{}
		for _, doc := range docs {
			ids = append(ids, str(doc, "id"))
		}
		if len(lists) > 1 {
			results = spreadChatDocuments(mergeChatResults(lists, 0), ids, count)
		}
	}
	for i, result := range results {
		result["rank"] = i + 1
	}
	return results, uniqueWorkflowStrings(warnings), nil
}

func (s *Server) searchChatDocuments(ctx context.Context, query string, filters chatSearchQuery, docs []M, explicit bool, settings M, count int) ([]M, []string, error) {
	ids := []string{}
	byMemory := map[string]M{}
	for _, doc := range docs {
		mid := documentSearchMemory(doc)
		if mid == "" {
			continue
		}
		ids = append(ids, str(doc, "id"))
		byMemory[mid] = doc
	}
	pool := count
	if settings["hybrid_search"] == true || settings["enable_reranking"] == true {
		pool = max(count, 20)
	}
	hits, err := s.searchDocumentIndexes(ctx, query, docs, pool, false)
	if err != nil {
		return nil, nil, fail(502, "The GoodMem document index could not be queried. Retry or ask the administrator to check GoodMem.")
	}
	results := []M{}
	seen := map[string]bool{}
	perDoc := map[string]int{}
	appendHit := func(hit Hit) {
		doc := byMemory[hit.MemoryID]
		if doc == nil || math.IsNaN(hit.Score) || math.IsInf(hit.Score, 0) {
			return
		}
		row := chatHitResult(hit, doc)
		key := chatResultKey(row)
		if seen[key] || !chatResultMatches(row, filters) {
			return
		}
		seen[key] = true
		results = append(results, row)
		perDoc[str(doc, "id")]++
	}
	for _, hit := range hits {
		appendHit(hit)
	}
	if explicit && len(ids) > 1 && len(ids) <= count {
		// Guarantee the share of the final answer budget, not the inflated
		// reranking pool; otherwise small files are fetched again needlessly.
		floor := max(1, count/(2*len(ids)))
		for _, doc := range docs {
			if perDoc[str(doc, "id")] >= floor {
				continue
			}
			own, err := s.searchDocumentIndexes(ctx, query, []M{doc}, pool, false)
			if err != nil {
				return nil, nil, fail(502, "GoodMem could not search every selected document. Retry the request.")
			}
			for _, hit := range own {
				appendHit(hit)
			}
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		return judgeNumber(results[i], "raw_score") < judgeNumber(results[j], "raw_score")
	})
	threshold := judgeNumber(settings, "similarity_threshold")
	filtered := []M{}
	for _, result := range results {
		if judgeNumber(result, "similarity") < threshold {
			object(result["metadata"])["below_threshold"] = true
			if !explicit {
				continue
			}
		}
		filtered = append(filtered, result)
	}
	if len(filtered) == 0 && len(results) > 0 {
		filtered = results[:min(3, count, len(results))]
	}
	results = filtered
	if settings["hybrid_search"] == true {
		results = chatHybridResults(results, query)
	}
	warnings := []string{}
	if settings["enable_reranking"] == true && len(results) > 0 && s.gm.Reranker != "" {
		// GoodMem's rerank response contains reranker scores, not vector
		// similarities. Keep the first retrieval for thresholding/provenance
		// and use this response only to order those authorized candidates.
		ranked, rerankErr := s.searchDocumentIndexes(ctx, query, docs, pool, true)
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if rerankErr != nil {
			warnings = append(warnings, "GoodMem reranking was unavailable; kept the vector/keyword ranking.")
		} else {
			scores := map[string]float64{}
			for _, hit := range ranked {
				if hit.Stage == "rerank" {
					scores[hit.ID] = hit.Score
				}
			}
			if len(scores) == 0 {
				warnings = append(warnings, "GoodMem returned its vector ranking; no reranker scores were available.")
			}
			for _, row := range results {
				if score, ok := scores[str(row, "id")]; ok {
					row["rerank_score"] = score
				}
			}
			sort.SliceStable(results, func(i, j int) bool {
				a, aOK := results[i]["rerank_score"].(float64)
				b, bOK := results[j]["rerank_score"].(float64)
				if aOK != bOK {
					return aOK
				}
				if aOK {
					return a > b
				}
				return false
			})
		}
	} else if settings["enable_reranking"] == true && s.gm.Reranker == "" {
		warnings = append(warnings, "No GoodMem reranker is configured; used the vector/keyword ranking.")
	}
	if explicit {
		results = spreadChatDocuments(results, ids, count)
	} else {
		results = results[:min(count, len(results))]
	}
	weak := 0
	for i, row := range results {
		row["rank"] = i + 1
		if object(row["metadata"])["below_threshold"] == true {
			weak++
		}
	}
	if weak > 0 {
		warnings = append(warnings, fmt.Sprintf("%d retrieved passage(s) are below the similarity threshold; treat them as weak matches.", weak))
	}
	return results, warnings, nil
}
