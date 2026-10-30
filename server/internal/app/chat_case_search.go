package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

func (s *Server) searchChatCases(ctx context.Context, user, question, query string, limit int) ([]M, []string, error) {
	phase, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	opinions, err := s.chatCourtCandidates(phase, user, fallback(query, question), limit, parseChatSearch(question))
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if err != nil {
		var api *apiError
		if errors.As(err, &api) && api.status == 400 {
			return nil, nil, err
		}
		return []M{}, []string{"Case-law search could not complete; the answer has no outside authorities from this search."}, nil
	}
	results := []M{}
	for i, op := range opinions {
		text := courtText(str(op, "snippet"))
		if i < 3 {
			if full, err := s.courtOpinionText(phase, user, str(op, "id")); err == nil && strings.TrimSpace(full) != "" {
				// Read around the relevant terms instead of always showing the
				// opinion's opening caption and procedural boilerplate.
				text = chatCaseExcerpt(full, question, 2000)
			}
		}
		citations := stringsOf(op["citation"])
		citation := "No citation"
		if len(citations) > 0 {
			citation = citations[0]
		}
		results = append(results, M{"id": "courtlistener_" + str(op, "id"), "text": text, "similarity": 0, "rank": i + 1, "metadata": M{
			"filename": str(op, "case_name") + " (" + citation + ")", "source": "courtlistener", "doc_type": "case_law", "court": op["court"], "date_filed": op["date_filed"],
			"citation": citation, "url": op["absolute_url"], "opinion_id": op["id"], "cluster_id": op["cluster_id"], "chunk_index": 0, "case_name": op["case_name"]}})
	}
	if len(results) == 0 {
		return results, nil, nil
	}
	candidates := []M{}
	for i, result := range results {
		candidates = append(candidates, M{"i": i, "case": result["metadata"], "text": chatCaseExcerpt(strings.Join(strings.Fields(str(result, "text")), " "), question, 700)})
	}
	data, err := s.structured(phase, user,
		`You are screening case-law search results for a legal research question. Keyword search returns noise. For EACH candidate, use ONLY the supplied text: relevant is true only for the same offense, doctrine or issue; confidence is a 0–1 relevance assessment; summary is 2–3 sentences describing the case and holding without invented facts. Return STRICT JSON: {"cases":[{"i":0,"relevant":true,"confidence":0.9,"summary":"..."}]}.`,
		"Question: "+charSlice(question, 0, 500)+"\nUntrusted case excerpts JSON:\n"+string(jsonBytes(candidates)))
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	rows, valid := data["cases"].([]any)
	if err != nil || !valid {
		return results, []string{"Case-law relevance screening was unavailable; the returned cases are unreviewed research leads."}, nil
	}
	verdicts := map[int]M{}
	for _, row := range rows {
		item := object(row)
		i, err := chatInt(item["i"], 0, len(results)-1)
		if err == nil {
			verdicts[i] = item
		}
	}
	kept := []M{}
	for i, result := range results {
		verdict := verdicts[i]
		relevant, valid := researchBool(verdict["relevant"])
		if !valid || !relevant {
			continue
		}
		if score, ok := chatAuthorityRelevance(verdict["confidence"]); ok {
			result["similarity"] = score
		}
		meta := object(result["metadata"])
		if summary := strings.TrimSpace(str(verdict, "summary")); summary != "" {
			summary, _ = redactCaseReferences(summary, []string{str(result, "text")}, []M{result}, false)
			meta["case_summary"] = summary
		}
		meta["relevance_screened"] = true
		result["rank"] = len(kept) + 1
		kept = append(kept, result)
	}
	return kept, nil, nil
}

var chatCasePartyRE = regexp.MustCompile(`(?i)\s+vs?\.?\s+`)

// Score overlapping bounded windows instead of the centre of a potentially
// enormous OCR paragraph. Apply this again when shortening an excerpt so the
// relevance screen and citation card do not lose the passage that earned it.
func chatCaseExcerpt(text, query string, budget int) string {
	runes := []rune(text)
	if budget <= 0 {
		return ""
	}
	if len(runes) <= budget {
		return text
	}
	terms := map[string]bool{}
	for _, term := range draftingTerms(query) {
		terms[term] = true
	}
	best, bestScore := 0, -1
	for start := 0; ; start += max(1, budget/2) {
		start = min(start, len(runes)-budget)
		matches := map[string]int{}
		for _, term := range draftingTerms(string(runes[start : start+budget])) {
			if terms[term] {
				matches[term]++
			}
		}
		score := 0
		for _, count := range matches {
			score += 3 + min(count, 3)
		}
		if score > bestScore {
			best, bestScore = start, score
		}
		if start+budget == len(runes) {
			break
		}
	}
	return string(runes[best : best+budget])
}

func chatCaseReferenced(answer string, result M) bool {
	meta := object(result["metadata"])
	content := strings.ToLower(answer)
	name := strings.ToLower(str(meta, "case_name"))
	if name != "" && strings.Contains(content, name) {
		return true
	}
	parties := []string{}
	for _, party := range chatCasePartyRE.Split(name, -1) {
		party = strings.TrimSpace(party)
		if len([]rune(party)) > 2 {
			parties = append(parties, party)
		}
	}
	if len(parties) >= 2 {
		pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(parties[0]) + `\b.{0,30}\b` + regexp.QuoteMeta(parties[1]) + `\b`)
		if pattern.MatchString(content) {
			return true
		}
	}
	citation := strings.ToLower(str(meta, "citation"))
	normalize := strings.NewReplacer(".", "", ",", "")
	if citation != "" && citation != "no citation" && strings.Contains(normalize.Replace(content), normalize.Replace(citation)) {
		return true
	}
	filename := str(meta, "filename")
	stem, _, _ := strings.Cut(filename, "(")
	stem = strings.ToLower(strings.TrimSpace(stem))
	return len(filename) > 10 && len(stem) > 5 && strings.Contains(content, stem)
}

func chatCaseCitations(results []M, answer, query string, offset int) []M {
	out := []M{}
	for i, result := range results {
		meta := object(result["metadata"])
		score := judgeNumber(result, "similarity")
		passage := charSlice(str(result, "text"), 0, 500)
		if meta["quote_verified"] != true {
			passage = chatCaseExcerpt(str(result, "text"), query, 500)
		}
		if passage == "" {
			passage = "Full text not available — open the opinion on CourtListener."
		}
		c := chatCitation(fallback(str(meta, "filename"), "Unknown Case"), "case_law", passage, offset+i+1)
		c["similarity"], c["confidence"], c["token_count"] = score, int(score*100), len(strings.Fields(str(result, "text")))
		for _, key := range []string{"url", "opinion_id", "cluster_id", "case_summary"} {
			c[key] = meta[key]
		}
		c["opinion_id"], c["cluster_id"] = nullable(courtID(meta["opinion_id"])), nullable(courtID(meta["cluster_id"]))
		c["was_cited_by_ai"] = chatCaseReferenced(answer, result)
		application := "Keyword-search result not screened for relevance; treat it as a research lead to check."
		if meta["relevance_screened"] == true || score > 0 || str(meta, "case_summary") != "" {
			application = "Kept by the relevance screen. Read the opinion and check its current treatment before relying on it."
		}
		if meta["quote_verified"] == true {
			c["verified"] = true
			application = "The supporting quotation was verified verbatim against the opinion text read. Check the opinion's current treatment before relying on it."
		}
		if meta["partially_read"] == true {
			application += " This opinion was reviewed only in part."
		}
		c["logic"] = M{"query_intent": query, "matching_criteria": fmt.Sprintf("Court: %s | Citation: %s | Date: %s", str(meta, "court"), str(meta, "citation"), str(meta, "date_filed")), "application": application}
		out = append(out, c)
	}
	return out
}
