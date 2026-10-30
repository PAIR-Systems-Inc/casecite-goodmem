package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const chatQuoteEdge = " \t\r\n|–—-:·.,;"

func chatTidySpan(text string, start, end int) (int, int, bool) {
	if start < 0 || end > len(text) || start > end {
		return 0, 0, false
	}
	span := text[start:end]
	trimmed := strings.TrimLeft(span, chatQuoteEdge)
	start += len(span) - len(trimmed)
	trimmed = strings.TrimRight(trimmed, chatQuoteEdge)
	end = start + len(trimmed)
	return start, end, utf8.RuneCountInString(trimmed) >= 12
}
func chatContentRatio(text string) float64 {
	if text == "" {
		return 0
	}
	count := 0
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			count++
		}
	}
	return float64(count) / float64(utf8.RuneCountInString(text))
}

// Ranges are rune offsets, matching Python. External source anchors are always
// converted to UTF-16 before being sent to Word or the browser.
func chatFileExcerpt(text string, ranges [][2]int) string {
	runes := []rune(text)
	if len(runes) <= 30000 {
		return text
	}
	if len(ranges) == 0 {
		return string(runes[:30000])
	}
	windows := [][2]int{{0, 8000}}
	for _, span := range ranges {
		windows = append(windows, [2]int{max(0, span[0]-1500), min(len(runes), span[1]+1500)})
	}
	sort.Slice(windows, func(i, j int) bool {
		if windows[i][0] == windows[j][0] {
			return windows[i][1] < windows[j][1]
		}
		return windows[i][0] < windows[j][0]
	})
	merged := [][2]int{}
	for _, window := range windows {
		if window[0] >= window[1] || window[0] >= len(runes) {
			continue
		}
		if n := len(merged); n > 0 && window[0] <= merged[n-1][1] {
			merged[n-1][1] = max(merged[n-1][1], window[1])
		} else {
			merged = append(merged, window)
		}
	}
	parts := []string{}
	budget := 30000
	for _, window := range merged {
		if budget <= 0 {
			break
		}
		end := min(window[1], window[0]+budget)
		parts = append(parts, string(runes[window[0]:end]))
		budget -= end - window[0]
	}
	return strings.Join(parts, "\n[...]\n")
}

func chatCitation(source, kind, passage string, rank int) M {
	return M{"id": uuid(), "source": source, "type": kind, "confidence": 0, "status": "pending", "similarity": 0,
		"relevance_rank": rank, "chunk_index": 0, "token_count": 0, "passage": passage, "reasoning": []M{},
		"application": nil, "case_summary": nil, "logic": M{"query_intent": "", "matching_criteria": "", "application": ""},
		"notes": nil, "reviewed_at": nil, "document_id": nil, "url": nil, "opinion_id": nil, "cluster_id": nil,
		"was_cited_by_ai": false, "verified": nil, "doc_span_start": nil, "doc_span_end": nil,
		"section": nil, "page_start": nil, "page_end": nil, "support": nil, "support_note": nil}
}

func chatDocumentCitations(results []M, query string) []M {
	out := []M{}
	for i, result := range results {
		meta := object(result["metadata"])
		score := judgeNumber(result, "similarity")
		c := chatCitation(fallback(str(meta, "filename"), "Unknown Source"), fallback(str(meta, "doc_type"), "document"), charSlice(str(result, "text"), 0, 500), i+1)
		c["document_id"], c["url"], c["chunk_index"], c["token_count"] = meta["document_id"], meta["url"], number(meta, "chunk_index", 0), number(result, "token_count", 0)
		c["similarity"], c["confidence"] = score, int(score*100)
		c["raw_score"], c["score_stage"], c["score_kind"] = result["raw_score"], result["score_stage"], result["score_kind"]
		c["logic"] = M{"query_intent": query, "matching_criteria": fmt.Sprintf("GoodMem retrieval score %.3f; result #%d", score, i+1), "application": "This passage matched the question. Retrieval similarity does not establish whether it supports the answer."}
		if meta["below_threshold"] == true {
			c["notes"] = "Weak match: below the selected similarity threshold."
		}
		for _, key := range []string{"section", "page_start", "page_end"} {
			if meta[key] != nil {
				c[key] = meta[key]
			}
		}
		out = append(out, c)
	}
	return out
}

func attachChatLocation(citation M, text string, doc M, start, end int) {
	citation["doc_span_start"], citation["doc_span_end"] = byteToUTF16(text, start), byteToUTF16(text, end)
	for _, page := range maps(object(doc["text_extraction"])["pages"]) {
		if number(page, "span_end", 0) > number(citation, "doc_span_start", 0) && number(page, "span_start", 0) < number(citation, "doc_span_end", 0) {
			if citation["page_start"] == nil {
				citation["page_start"] = number(page, "page_index", 0) + 1
			}
			citation["page_end"] = number(page, "page_index", 0) + 1
		}
	}
	citation["text_extraction"] = doc["text_extraction"]
}

type chatGrounding struct {
	Citations                        []M
	Flagged                          []M
	Claims, FilesChecked, FilesTotal int
	AnswerTruncated                  bool
	SupportChecked                   bool
	Warnings                         []string
}

var chatSupportTitles = map[string]string{"supported": "Supported by the source", "partial": "Only partly supported", "contradicted": "Contradicted by the source", "unsupported": "Not supported by the source"}
var chatSupportDetails = map[string]string{"partial": "the source adds a condition the answer leaves out", "contradicted": "the source says something different", "unsupported": "no supporting passage was found"}

func (g *chatGrounding) Notice() string {
	lines := []string{}
	if len(g.Flagged) > 0 {
		lines = append(lines, fmt.Sprintf("Source check: %d statement(s) in this answer are not fully supported by your documents:", len(g.Flagged)))
		for _, flag := range g.Flagged[:min(8, len(g.Flagged))] {
			said := strings.TrimSpace(fallback(str(flag, "answer_quote"), str(flag, "claim")))
			if utf8.RuneCountInString(said) > 160 {
				said = charSlice(said, 0, 157) + "..."
			}
			lines = append(lines, "- "+strconv.Quote(said)+" — "+fallback(str(flag, "note"), chatSupportDetails[str(flag, "label")]))
		}
		if len(g.Flagged) > 8 {
			lines = append(lines, fmt.Sprintf("- ...and %d more (see the citations).", len(g.Flagged)-8))
		}
	}
	if g.AnswerTruncated {
		lines = append(lines, "Only the first 12,000 characters of the answer were checked.")
	}
	if g.FilesChecked < g.FilesTotal {
		lines = append(lines, fmt.Sprintf("%d of %d source files were checked.", g.FilesChecked, g.FilesTotal))
	}
	lines = append(lines, g.Warnings...)
	return strings.Join(lines, "\n")
}

func chatReadFatal(err error) bool {
	var api *apiError
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &api) && (api.status == 401 || api.status == 403 || api.status == 404 || api.status == 409)
}

func (s *Server) groundChatAnswer(ctx context.Context, user, query, answer string, results []M, checkSupport bool) (*chatGrounding, error) {
	scores := map[string]float64{}
	ids := []string{}
	for _, result := range results {
		id := str(object(result["metadata"]), "document_id")
		if id == "" {
			continue
		}
		if _, exists := scores[id]; !exists {
			ids = append(ids, id)
		}
		scores[id] = max(scores[id], judgeNumber(result, "similarity"))
	}
	if len(ids) == 0 {
		return nil, nil
	}
	sort.SliceStable(ids, func(i, j int) bool { return scores[ids[i]] > scores[ids[j]] })
	g := &chatGrounding{Citations: []M{}, Flagged: []M{}, FilesTotal: len(ids), AnswerTruncated: utf8.RuneCountInString(answer) > 12000}
	type source struct {
		Text string
		Doc  M
	}
	sources := map[string]source{}
	files := []M{}
	for i, id := range ids[:min(6, len(ids))] {
		text, doc, err := s.documentText(ctx, user, id)
		if err != nil {
			if chatReadFatal(err) {
				return nil, err
			}
			continue
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		ranges := [][2]int{}
		for _, r := range results {
			if str(object(r["metadata"]), "document_id") == id {
				if start, end, found := findQuote(text, str(r, "text")); found {
					ranges = append(ranges, [2]int{utf8.RuneCountInString(text[:start]), utf8.RuneCountInString(text[:end])})
				}
			}
		}
		ref := fmt.Sprintf("f%d", i)
		sources[ref] = source{text, doc}
		files = append(files, M{"ref": ref, "id": id, "name": doc["filename"], "text": chatFileExcerpt(text, ranges)})
	}
	g.FilesChecked = len(files)
	if len(files) == 0 {
		g.Warnings = append(g.Warnings, "The original source text could not be read for claim verification; citations show retrieved passages only.")
		return g, nil
	}
	payload, err := s.structured(ctx, user, chatAssets.Instructions["build_grounding_prompt"]+"15 claims, most important first).",
		"Question: "+query+"\nAnswer: "+charSlice(answer, 0, 12000)+"\nUntrusted source files JSON:\n"+string(jsonBytes(files)))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		g.Warnings = append(g.Warnings, "Claim verification was unavailable; citations show retrieved passages only.")
		return g, nil
	}
	claims, valid := payload["claims"].([]any)
	if !valid {
		g.Warnings = append(g.Warnings, "The model did not return usable claim checks; citations show retrieved passages only.")
		return g, nil
	}
	grounded := []M{}
	supportItems := []M{}
	for _, raw := range claims[:min(15, len(claims))] {
		claim := object(raw)
		statement := strings.TrimSpace(str(claim, "claim"))
		if statement == "" {
			continue
		}
		g.Claims++
		answerQuote := strings.TrimSpace(str(claim, "answer_quote"))
		if _, _, found := findQuote(answer, answerQuote); !found {
			answerQuote = ""
		}
		answerQuote = charSlice(answerQuote, 0, 300)
		quote := strings.TrimSpace(str(claim, "quote"))
		src, ok := sources[str(claim, "file_ref")]
		if quote == "" || !ok {
			g.Flagged = append(g.Flagged, M{"claim": statement, "answer_quote": answerQuote, "label": "unsupported"})
			continue
		}
		start, end, verified := findQuote(src.Text, quote)
		if verified {
			start, end, verified = chatTidySpan(src.Text, start, end)
		}
		exact := strings.Trim(quote, chatQuoteEdge)
		if verified {
			exact = src.Text[start:end]
		}
		if chatContentRatio(exact) < .45 || utf8.RuneCountInString(exact) < 12 {
			g.Flagged = append(g.Flagged, M{"claim": statement, "answer_quote": answerQuote, "label": "unsupported", "note": "The proposed supporting quote was empty or mostly markup."})
			continue
		}
		n := len(grounded)
		c := chatCitation(str(src.Doc, "filename"), "document", charSlice(exact, 0, 600), n+1)
		c["id"], c["document_id"], c["application"], c["verified"], c["was_cited_by_ai"] = "claim-"+uuid(), src.Doc["id"], statement, verified, true
		c["similarity"], c["confidence"] = scores[str(src.Doc, "id")], math.Round(scores[str(src.Doc, "id")]*1000)/10
		where := strconv.Quote(str(src.Doc, "filename"))
		matching := "Quote not found verbatim in " + where
		if verified {
			where = fmt.Sprintf("line %d of %s", strings.Count(src.Text[:start], "\n")+1, where)
			matching = "Verified verbatim at " + where
			attachChatLocation(c, src.Text, src.Doc, start, end)
		} else {
			c["support"], c["support_note"] = "unsupported", "The proposed quote could not be found in the source."
			g.Flagged = append(g.Flagged, M{"claim": statement, "answer_quote": answerQuote, "label": "unsupported", "note": c["support_note"]})
		}
		c["logic"] = M{"query_intent": query, "matching_criteria": matching, "application": statement}
		c["reasoning"] = []M{{"type": "Fact in the answer", "description": statement, "evidence": nullable(answerQuote)}, {"type": "Where it comes from", "description": matching, "evidence": charSlice(exact, 0, 600)}}
		grounded = append(grounded, c)
		if verified && checkSupport {
			lo, hi := utf8.RuneCountInString(src.Text[:start]), utf8.RuneCountInString(src.Text[:end])
			supportItems = append(supportItems, M{"n": n, "claim": statement, "answer_quote": answerQuote, "quote": charSlice(exact, 0, 600), "source": src.Doc["filename"], "context": charSlice(src.Text, max(0, lo-700), hi+700)})
		}
	}
	if checkSupport && len(supportItems) > 0 {
		labels, err := s.structured(ctx, user, chatAssets.Instructions["build_support_prompt"], "Question: "+query+"\nUntrusted claims and source context JSON:\n"+string(jsonBytes(supportItems)))
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		seen := map[int]bool{}
		eligible := map[int]M{}
		for _, item := range supportItems {
			eligible[number(item, "n", -1)] = item
		}
		if err == nil {
			for _, label := range maps(labels["items"]) {
				n := number(label, "n", -1)
				kind := strings.ToLower(strings.TrimSpace(str(label, "label")))
				if eligible[n] == nil || seen[n] || chatSupportTitles[kind] == "" {
					continue
				}
				seen[n] = true
				c := grounded[n]
				note := charSlice(strings.TrimSpace(str(label, "note")), 0, 300)
				c["support"], c["support_note"] = kind, nullable(note)
				c["reasoning"] = append(maps(c["reasoning"]), M{"type": "Does the source support it", "description": chatSupportTitles[kind] + ": " + note, "evidence": nil})
				if kind != "supported" {
					g.Flagged = append(g.Flagged, M{"claim": c["application"], "answer_quote": eligible[n]["answer_quote"], "label": kind, "note": note})
				}
			}
		}
		g.SupportChecked = len(seen) == len(supportItems)
		if !g.SupportChecked {
			g.Warnings = append(g.Warnings, fmt.Sprintf("Support could not be checked for %d quoted claim(s). A verified quote alone does not establish that the answer is correct.", len(supportItems)-len(seen)))
		}
	}
	g.Citations = grounded
	for _, src := range sources {
		current, err := s.getObject(ctx, user, "document", str(src.Doc, "id"))
		if err != nil {
			return nil, err
		}
		if str(current, "memory_id") != str(src.Doc, "memory_id") {
			return nil, fail(409, "A source document changed during claim verification. Retry the question.")
		}
	}
	return g, nil
}

func normalizeChatReasoning(raw any) []M {
	out := []M{}
	for _, step := range maps(raw) {
		description := strings.TrimSpace(str(step, "description"))
		if description == "" {
			continue
		}
		kind := charSlice(fallback(strings.TrimSpace(str(step, "type")), "Analysis"), 0, 40)
		out = append(out, M{"type": kind, "description": description, "evidence": nullable(strings.TrimSpace(str(step, "evidence")))})
		if len(out) == 5 {
			break
		}
	}
	return out
}

func (s *Server) enrichChatCitations(ctx context.Context, user, query, answer string, citations []M) error {
	if len(citations) == 0 {
		return nil
	}
	refs := []M{}
	for i, c := range citations {
		refs = append(refs, M{"ref": fmt.Sprintf("c%d", i), "source": c["source"], "type": c["type"], "snippet": charSlice(strings.Join(strings.Fields(str(c, "passage")), " "), 0, 1200)})
	}
	data, err := s.structured(ctx, user, chatAssets.Instructions["build_reasoning_prompt"], "Question: "+query+"\nAnswer: "+charSlice(answer, 0, 4000)+"\nUntrusted source excerpts JSON:\n"+string(jsonBytes(refs)))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return nil
	}
	byRef := map[string]M{}
	for _, entry := range maps(data["citations"]) {
		byRef[strings.TrimSpace(str(entry, "ref"))] = entry
	}
	for i, c := range citations {
		entry := byRef[fmt.Sprintf("c%d", i)]
		if entry == nil {
			continue
		}
		steps := normalizeChatReasoning(entry["reasoning"])
		for _, step := range steps {
			if quote := str(step, "evidence"); quote != "" {
				if start, end, found := findQuote(str(c, "passage"), quote); found {
					step["evidence"] = str(c, "passage")[start:end]
				} else {
					step["evidence"] = nil
					step["description"] = str(step, "description") + " (The proposed evidence quote could not be verified against this source.)"
				}
			}
		}
		c["reasoning"], c["application"] = steps, nullable(strings.TrimSpace(str(entry, "application")))
	}
	return nil
}

// Apply source tracking to the fallback passage citations as well as claim
// citations. Reads use the same native/OCR canonical text and original document.
func (s *Server) trackChatSources(ctx context.Context, user string, citations []M) error {
	type loaded struct {
		text string
		doc  M
	}
	cache := map[string]loaded{}
	failed := map[string]bool{}
	for _, c := range citations {
		id := str(c, "document_id")
		if id == "" || failed[id] {
			continue
		}
		src, present := cache[id]
		if !present {
			text, doc, err := s.documentText(ctx, user, id)
			if err != nil {
				if chatReadFatal(err) {
					return err
				}
				failed[id] = true
				continue
			}
			src = loaded{text, doc}
			cache[id] = src
		}
		if start, end, found := findQuote(src.text, str(c, "passage")); found {
			attachChatLocation(c, src.text, src.doc, start, end)
			location := fmt.Sprintf("Location: chunk #%d, line %d", number(c, "chunk_index", 0)+1, strings.Count(src.text[:start], "\n")+1)
			if c["page_start"] != nil {
				location += fmt.Sprintf(", page %d", number(c, "page_start", 0))
			}
			notes := []string{}
			if str(c, "notes") != "" {
				notes = append(notes, str(c, "notes"))
			}
			notes = append(notes, location)
			c["notes"] = strings.Join(notes, " | ")
		}
	}
	return nil
}

func chatUniqueSourceIDs(citations []M) []string {
	ids := []string{}
	for _, c := range citations {
		if id := str(c, "document_id"); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}
