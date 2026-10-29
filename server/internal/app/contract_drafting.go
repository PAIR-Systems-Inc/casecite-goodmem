package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type draftReference struct{ Label, Text string }

func modelContextWindow(model string, override int) int {
	if override > 0 {
		return override
	}
	for _, entry := range []struct {
		name string
		size int
	}{{"gpt-5", 400000}, {"gpt-4.1", 1000000}, {"gpt-4o", 128000}, {"gpt-4", 128000}, {"o1", 200000}, {"o3", 200000}, {"o4", 200000}, {"claude", 200000}, {"gemini", 1000000}, {"llama", 128000}, {"mistral", 128000}, {"qwen", 128000}} {
		if strings.Contains(strings.ToLower(model), entry.name) {
			return entry.size
		}
	}
	return 128000
}
func estimateTokens(text string) int { return max(1, len([]rune(text))/4) }

var draftingWordRE = regexp.MustCompile(`[a-z][a-z0-9'-]{2,}`)
var draftingStop = strings.Fields("the and for that with this from shall have will any all are not such other than into under upon each been being their there which where when what these those")

func draftingTerms(text string) []string {
	out := []string{}
	for _, w := range draftingWordRE.FindAllString(strings.ToLower(text), -1) {
		if slices.Contains(draftingStop, w) {
			continue
		}
		if len(w) > 6 {
			w = w[:6]
		} else if len(w) > 3 {
			w = strings.TrimSuffix(w, "s")
		}
		out = append(out, w)
	}
	return out
}
func draftingChunks(text string) []string {
	out := []string{}
	current := []string{}
	length := 0
	for _, paragraph := range blankLineRE.Split(text, -1) {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		size := len([]rune(paragraph))
		if len(current) > 0 && length+size > 1600 {
			out = append(out, strings.Join(current, "\n\n"))
			current = []string{}
			length = 0
		}
		current = append(current, paragraph)
		length += size + 2
	}
	if len(current) > 0 {
		out = append(out, strings.Join(current, "\n\n"))
	}
	return out
}
func buildReferenceBlock(references []draftReference, budget int, focus string) (string, string) {
	refs := []draftReference{}
	full := []string{}
	for _, r := range references {
		if strings.TrimSpace(r.Text) == "" {
			continue
		}
		refs = append(refs, r)
		full = append(full, "=== "+r.Label+" ===\n"+strings.TrimSpace(r.Text))
	}
	if len(refs) == 0 {
		return "", "none"
	}
	joined := strings.Join(full, "\n\n")
	if estimateTokens(joined) <= budget {
		return joined, "full"
	}
	type scored struct {
		score       float64
		doc, chunk  int
		label, text string
	}
	scores := []scored{}
	terms := draftingTerms(focus)
	for d, r := range refs {
		for c, chunk := range draftingChunks(r.Text) {
			words := draftingTerms(chunk)
			hits := 0
			for _, w := range words {
				if slices.Contains(terms, w) {
					hits++
				}
			}
			score := float64(hits)/math.Sqrt(float64(max(1, len(words)))) - float64(c)*0.001
			scores = append(scores, scored{score, d, c, r.Label, chunk})
		}
	}
	slices.SortStableFunc(scores, func(a, b scored) int {
		if a.score > b.score {
			return -1
		}
		if a.score < b.score {
			return 1
		}
		return 0
	})
	used := 0
	kept := []scored{}
	for _, candidate := range scores {
		cost := estimateTokens(candidate.text) + 8
		if used+cost > budget {
			continue
		}
		used += cost
		kept = append(kept, candidate)
	}
	slices.SortFunc(kept, func(a, b scored) int {
		if a.doc != b.doc {
			return a.doc - b.doc
		}
		return a.chunk - b.chunk
	})
	parts := []string{}
	label := ""
	for _, c := range kept {
		if c.label != label {
			parts = append(parts, "=== "+c.label+" (excerpts) ===")
			label = c.label
		}
		parts = append(parts, c.text)
	}
	return strings.Join(parts, "\n\n"), "excerpts"
}
func normalizeDraftPlan(data M, targetWords int) (M, error) {
	sections := []M{}
	raw := maps(data["sections"])
	for i, item := range raw[:min(60, len(raw))] {
		words := number(item, "target_words", 0)
		if value, ok := item["target_words"].(string); ok {
			words, _ = strconv.Atoi(value)
		}
		sections = append(sections, M{"number": i + 1, "title": fallback(strings.TrimSpace(str(item, "title")), fmt.Sprintf("Section %d", i+1)), "brief": strings.TrimSpace(str(item, "brief")), "exclude": strings.TrimSpace(str(item, "exclude")), "target_words": words})
	}
	if len(sections) == 0 {
		return nil, bad("Draft planning failed: the plan has no usable sections")
	}
	total := 0
	for _, sec := range sections {
		total += number(sec, "target_words", 0)
	}
	if targetWords > 0 {
		if total <= 0 {
			for _, sec := range sections {
				sec["target_words"] = targetWords / len(sections)
			}
		} else if math.Abs(float64(total-targetWords)) > float64(targetWords)*0.15 {
			scale := float64(targetWords) / float64(total)
			for _, sec := range sections {
				sec["target_words"] = int(float64(number(sec, "target_words", 0)) * scale)
			}
		}
	}
	total = 0
	for _, sec := range sections {
		words := max(150, min(2500, number(sec, "target_words", 0)))
		sec["target_words"] = words
		total += words
	}
	return M{"title": fallback(strings.TrimSpace(str(data, "title")), "Draft"), "document_type": strings.TrimSpace(str(data, "document_type")), "sections": sections, "definitions": strings.TrimSpace(str(data, "definitions")), "style_guide": strings.TrimSpace(str(data, "style_guide")), "target_words": total}, nil
}
func sharedDraftBlock(plan M, instructions string) string {
	lines := []string{"DOCUMENT: " + fallback(str(plan, "title"), "Draft")}
	if kind := str(plan, "document_type"); kind != "" {
		lines = append(lines, "TYPE: "+kind)
	}
	lines = append(lines, "SECTION PLAN (every section, so cross-references resolve):")
	for _, sec := range maps(plan["sections"]) {
		lines = append(lines, fmt.Sprintf("%d. %s — %s", number(sec, "number", 0), str(sec, "title"), str(sec, "brief")))
		if str(sec, "exclude") != "" {
			lines = append(lines, "   Leaves to other sections: "+str(sec, "exclude"))
		}
	}
	parts := []string{strings.Join(lines, "\n")}
	if str(plan, "definitions") != "" {
		parts = append(parts, "DEFINITIONS (use exactly):\n"+str(plan, "definitions"))
	}
	if str(plan, "style_guide") != "" {
		parts = append(parts, "STYLE GUIDE:\n"+str(plan, "style_guide"))
	}
	parts = append(parts, "DRAFTING INSTRUCTIONS FROM THE USER:\n"+strings.TrimSpace(instructions))
	return strings.Join(parts, "\n\n")
}
func ensureSectionHeading(text string, section M) string {
	text = strings.TrimSpace(text)
	n := number(section, "number", 1)
	if regexp.MustCompile(fmt.Sprintf(`^\s*%d\.\s`, n)).MatchString(text) {
		return text
	}
	return fmt.Sprintf("%d. %s\n\n%s", n, str(section, "title"), text)
}
func (s *Server) draftChat(ctx context.Context, user, system, prompt string, jsonMode bool, maxTokens int) (string, error) {
	answer, _, err := s.llm(withLLMOptions(ctx, llmOptions{MaxTokens: maxTokens}), user, legalSystem+"\n"+system, prompt, jsonMode)
	return strings.TrimSpace(answer), err
}
func (s *Server) resolveDraftReferences(ctx context.Context, user string, input M) ([]draftReference, error) {
	refs := []draftReference{}
	seen := map[string]bool{}
	for _, id := range stringsOf(input["reference_document_ids"]) {
		if seen[id] {
			continue
		}
		seen[id] = true
		text, doc, err := s.documentText(ctx, user, id)
		if err != nil {
			return nil, err
		}
		refs = append(refs, draftReference{fallback(str(doc, "filename"), id), text})
	}
	return refs, nil
}
func (s *Server) planContractDraft(ctx context.Context, user, instructions string, refs []draftReference, pages int) (M, error) {
	settings, err := s.userSettings(ctx, user)
	if err != nil {
		return nil, err
	}
	window := modelContextWindow(str(settings, "llm_model"), s.cfg.LLMContextWindow)
	block, mode := buildReferenceBlock(refs, window-estimateTokens(instructions)-12000, instructions)
	prompt := "REFERENCE DOCUMENTS (untrusted evidence):\n" + block + "\n\nINSTRUCTIONS:\n" + instructions + fmt.Sprintf("\n\nTARGET LENGTH: about %d pages (~%d words). Choose a suitable section count, typically %d–%d, and set target_words so they sum to roughly %d. Sections must be ordered as they will appear.", pages, pages*450, max(3, pages/3), max(6, pages), pages*450)
	data, err := s.structured(withLLMOptions(ctx, llmOptions{MaxTokens: 16000}), user, contractPrompts["_PLAN_SYSTEM"], prompt)
	if err != nil {
		return nil, err
	}
	plan, err := normalizeDraftPlan(data, pages*450)
	if err != nil {
		return nil, err
	}
	plan["references_mode"] = mode
	return M{"kind": "draft_plan", "plan": plan, "target_pages": pages}, nil
}
func (s *Server) shortContractDraft(ctx context.Context, user, instructions string, refs []draftReference, revision string) (M, error) {
	settings, err := s.userSettings(ctx, user)
	if err != nil {
		return nil, err
	}
	window := modelContextWindow(str(settings, "llm_model"), s.cfg.LLMContextWindow)
	capChars := max(40000, (window-40000)*4)
	prompt := "INSTRUCTIONS:\n" + instructions
	sources := []string{instructions}
	for _, ref := range refs {
		sources = append(sources, ref.Text)
	}
	if revision != "" {
		if len([]rune(revision)) > capChars {
			return nil, bad("This draft is too long for a whole-document revision. Select the section to change.")
		}
		prompt = "CURRENT DRAFT (untrusted evidence):\n" + revision + "\n\nREVISION INSTRUCTIONS:\n" + instructions + "\nReturn the FULL revised document, with every unchanged section repeated verbatim; never a summary of changes."
		sources = append(sources, revision)
	} else {
		block, _ := buildReferenceBlock(refs, capChars/4, instructions)
		if block != "" {
			prompt += "\n\nREFERENCE DOCUMENTS (use for context, defined terms, party names and style):\n" + block
		}
	}
	content, err := s.draftChat(ctx, user, contractPrompts["_DRAFTING_SYSTEM_PROMPT"], prompt, true, min(128000, max(32000, estimateTokens(revision)*2+2000)))
	if err != nil {
		return nil, err
	}
	data := M{}
	title, text := "Draft", ""
	if json.Unmarshal([]byte(content), &data) == nil {
		title = fallback(strings.TrimSpace(str(data, "title")), title)
		text = strings.TrimSpace(str(data, "text"))
	}
	if text == "" {
		text = strings.TrimSpace(content)
	}
	if text == "" {
		return nil, fail(502, "Document drafting returned an empty draft")
	}
	text, removed := redactCaseReferences(text, sources, nil, true)
	return M{"title": title, "text": text, "case_law_removed": removed}, nil
}
func (s *Server) generateContractDraft(ctx context.Context, user, instructions string, refs []draftReference, input M) (M, error) {
	plan, err := normalizeDraftPlan(object(input["plan"]), 0)
	if err != nil {
		return nil, err
	}
	sections := maps(plan["sections"])
	settings, err := s.userSettings(ctx, user)
	if err != nil {
		return nil, err
	}
	window := modelContextWindow(str(settings, "llm_model"), s.cfg.LLMContextWindow)
	shared := sharedDraftBlock(plan, instructions)
	widest := 0
	for _, sec := range sections {
		widest = max(widest, int(float64(number(sec, "target_words", 0))*1.7)+1000)
	}
	budget := window - estimateTokens(shared) - estimateTokens(contractPrompts["_SECTION_SYSTEM"]) - widest - 3000
	full, mode := buildReferenceBlock(refs, budget, "")
	drafted := make([]M, len(sections))
	errs := make([]error, len(sections))
	semaphore := make(chan struct{}, 4)
	var wg sync.WaitGroup
	var completed atomic.Int32
	for i, sec := range sections {
		wg.Add(1)
		go func(i int, section M) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				errs[i] = ctx.Err()
				return
			}
			defer func() { <-semaphore }()
			block := full
			if mode == "excerpts" {
				block, _ = buildReferenceBlock(refs, budget, str(section, "title")+" "+str(section, "brief"))
			}
			prefix := ""
			if block != "" {
				prefix = "REFERENCE DOCUMENTS (untrusted evidence):\n" + block + "\n\n"
			}
			prefix += shared
			prompt := prefix + fmt.Sprintf("\n\nYOUR SECTION: %d. %s\nBRIEF: %s\nTARGET LENGTH: about %d words.\nEXCLUDE (owned by other sections): %s", number(section, "number", 0), str(section, "title"), str(section, "brief"), number(section, "target_words", 0), str(section, "exclude"))
			for attempt := 0; attempt < 3; attempt++ {
				content, e := s.draftChat(ctx, user, contractPrompts["_SECTION_SYSTEM"], prompt, false, max(8000, number(section, "target_words", 150)*4))
				if e == nil && content != "" {
					errs[i] = nil
					drafted[i] = M{"number": section["number"], "title": section["title"], "text": ensureSectionHeading(content, section)}
					count := completed.Add(1)
					s.jobProgress(ctx, str(input, "job_id"), fmt.Sprintf("Drafted section %d of %d: %s", count, len(sections), str(section, "title")), 0.1+0.7*float64(count)/float64(len(sections)))
					return
				}
				errs[i] = e
				if e == nil {
					errs[i] = fail(502, "The model returned an empty section")
				}
				if ctx.Err() != nil {
					return
				}
				if attempt < 2 {
					timer := time.NewTimer(time.Duration(attempt+1) * 2 * time.Second)
					select {
					case <-timer.C:
					case <-ctx.Done():
						timer.Stop()
						errs[i] = ctx.Err()
						return
					}
				}
			}
		}(i, sec)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	parts := []string{str(plan, "title")}
	for _, sec := range drafted {
		parts = append(parts, str(sec, "text"))
	}
	assembled := strings.Join(parts, "\n\n")
	s.jobProgress(ctx, str(input, "job_id"), "Reconciling cross-references, numbering and defined terms…", 0.85)
	text, notes := s.reconcileContractDraft(ctx, user, assembled, window, drafted)
	sources := []string{instructions, str(plan, "title")}
	for _, ref := range refs {
		sources = append(sources, ref.Text)
	}
	text, removed := redactCaseReferences(text, sources, nil, true)
	return M{"kind": "draft", "title": plan["title"], "text": text, "case_law_removed": removed, "sections": drafted, "reconcile_notes": notes, "references_mode": mode, "plan": plan}, nil
}
func normHeading(line string) string {
	return strings.TrimRight(strings.Join(strings.Fields(strings.ToLower(strings.NewReplacer("*", "", "#", "").Replace(line))), " "), " .:")
}
func checkReconciledSections(doc string, sections []M) string {
	positions := []int{}
	cursor := 0
	for _, sec := range sections {
		wanted := []string{normHeading(fmt.Sprintf("%d. %s", number(sec, "number", 0), str(sec, "title")))}
		if first := strings.SplitN(strings.TrimSpace(str(sec, "text")), "\n", 2)[0]; first != "" {
			wanted = append(wanted, normHeading(first))
		}
		found := -1
		at := cursor
		for _, line := range strings.Split(doc[cursor:], "\n") {
			if slices.Contains(wanted, normHeading(line)) {
				found = at
				cursor = at + len(line)
				break
			}
			at += len(line) + 1
		}
		if found < 0 {
			return fmt.Sprintf("section %d (%s) heading is missing", number(sec, "number", 0), str(sec, "title"))
		}
		positions = append(positions, found)
	}
	for i, sec := range sections {
		start, end := positions[i], len(doc)
		if i+1 < len(positions) {
			end = positions[i+1]
		}
		drafted := len([]rune(strings.TrimSpace(str(sec, "text"))))
		if drafted == 0 {
			continue
		}
		ratio := float64(len([]rune(strings.TrimSpace(doc[start:end])))) / float64(drafted)
		direction := ""
		if ratio < 0.85 {
			direction = "shorter"
		} else if ratio > 1.15 {
			direction = "longer"
		}
		if direction != "" {
			return fmt.Sprintf("section %d (%s) came back %.0f%% %s than drafted", number(sec, "number", 0), str(sec, "title"), math.RoundToEven(math.Abs(1-ratio)*100), direction)
		}
	}
	return ""
}
func (s *Server) reconcileContractDraft(ctx context.Context, user, assembled string, window int, sections []M) (string, []string) {
	if estimateTokens(assembled)*2+estimateTokens(contractPrompts["_RECONCILE_SYSTEM"])+2000 > window {
		return assembled, []string{"Reconcile pass skipped: the document exceeds the model's context window."}
	}
	content, err := s.draftChat(ctx, user, contractPrompts["_RECONCILE_SYSTEM"], "DOCUMENT:\n"+assembled, false, min(128000, max(8000, estimateTokens(assembled)*2+2000)))
	if err != nil {
		s.log.Warn("draft reconciliation failed", "error", err)
		return assembled, []string{"Reconcile pass failed or was truncated; the assembled sections are shown as drafted."}
	}
	doc, tail, _ := strings.Cut(content, "=== RECONCILE NOTES ===")
	doc = strings.TrimSpace(doc)
	if len([]rune(doc)) < int(float64(len([]rune(assembled)))*0.8) {
		return assembled, []string{"Reconcile pass discarded: it returned a shortened document. The assembled sections are shown as drafted."}
	}
	if problem := checkReconciledSections(doc, sections); problem != "" {
		return assembled, []string{"Reconcile pass discarded: " + problem + ". The assembled sections are shown as drafted."}
	}
	notes := []string{}
	for _, line := range strings.Split(tail, "\n") {
		if note := strings.TrimSpace(strings.TrimLeft(line, "-•* ")); note != "" {
			notes = append(notes, note)
		}
	}
	if len(notes) == 0 {
		notes = append(notes, "No changes.")
	}
	return doc, notes
}

var draftHeadingRE = regexp.MustCompile(`(?m)^[ \t]*(\d{1,2})\.[ \t]+(\S[^\n]{0,140}?)[ \t]*$`)
var draftTrailerRE = regexp.MustCompile(`(?m)^[ \t]*(?:(?i:in\s+witness\s+whereof|\[?\s*signature\s+pages?\s+(?:follows?|to\s+follow)\s*\]?)|SIGNATURES?\b|SCHEDULE\b|EXHIBIT\b|ANNEX\b|APPENDIX\b|ATTACHMENT\b)`)
var draftQuotedRE = regexp.MustCompile(`["“]([^"”]{20,})["”]`)

func splitDraftSections(text string) []M {
	sections := []M{}
	expected := 1
	for _, m := range draftHeadingRE.FindAllStringSubmatchIndex(text, -1) {
		n, _ := strconv.Atoi(text[m[2]:m[3]])
		if n != expected {
			continue
		}
		sections = append(sections, M{"number": n, "title": strings.TrimSpace(text[m[4]:m[5]]), "start": m[0], "end": len(text)})
		expected++
	}
	for i := 0; i+1 < len(sections); i++ {
		sections[i]["end"] = sections[i+1]["start"]
	}
	if len(sections) < 3 {
		return []M{}
	}
	last := sections[len(sections)-1]
	start := number(last, "start", 0)
	if at := strings.IndexByte(text[start:], '\n'); at >= 0 {
		start += at
	}
	if trailer := draftTrailerRE.FindStringIndex(text[start:]); trailer != nil {
		last["end"] = start + trailer[0]
	}
	return sections
}
func (s *Server) reviseContractDraft(ctx context.Context, user, text, instructions, jobID string) (M, error) {
	sections := []M{}
	if len([]rune(text)) >= 30000 {
		sections = splitDraftSections(text)
	}
	targets := []int{}
	all := len(sections) == 0
	for _, m := range draftQuotedRE.FindAllStringSubmatch(instructions, -1) {
		fragment := charSlice(strings.TrimSpace(strings.TrimRight(m[1], "…")), 0, 200)
		at := strings.Index(text, fragment)
		if at < 0 {
			continue
		}
		for _, sec := range sections {
			n := number(sec, "number", 0)
			if number(sec, "start", 0) <= at && at < number(sec, "end", 0) && !slices.Contains(targets, n) {
				targets = append(targets, n)
			}
		}
	}
	if !all && len(targets) == 0 {
		listing := []string{}
		for _, sec := range sections {
			preview := charSlice(text[number(sec, "start", 0):number(sec, "end", 0)], 0, 220)
			listing = append(listing, fmt.Sprintf("%d. %s: %s…", number(sec, "number", 0), str(sec, "title"), strings.ReplaceAll(preview, "\n", " ")))
		}
		data, err := s.structured(ctx, user, contractPrompts["_TARGET_SYSTEM"], "SECTIONS:\n"+strings.Join(listing, "\n")+"\nREVISION INSTRUCTIONS:\n"+instructions)
		if err != nil || data["sections"] == "all" {
			all = true
		} else {
			if values, ok := data["sections"].([]any); ok {
				for _, value := range values {
					n, ok := value.(float64)
					if ok && n == float64(int(n)) && n >= 1 && n <= float64(len(sections)) {
						targets = append(targets, int(n))
					}
				}
			}
			all = len(targets) == 0
		}
	}
	if all || len(targets) > len(sections)/2 {
		settings, err := s.userSettings(ctx, user)
		if err != nil {
			return nil, err
		}
		if len(sections) > 0 && estimateTokens(text)*2+3000 > modelContextWindow(str(settings, "llm_model"), s.cfg.LLMContextWindow) {
			return nil, bad("This change touches most of a draft that is too long to revise in one pass. Select the clause or section to change.")
		}
		result, err := s.shortContractDraft(ctx, user, instructions, nil, text)
		if err != nil {
			return nil, err
		}
		result["revised_sections"] = "all"
		return result, nil
	}
	original := text
	removed := []string{}
	revised := []int{}
	for i := len(sections) - 1; i >= 0; i-- {
		sec := sections[i]
		n := number(sec, "number", 0)
		if !slices.Contains(targets, n) {
			continue
		}
		s.jobProgress(ctx, jobID, fmt.Sprintf("Revising section %d: %s", n, str(sec, "title")), 0.2+0.7*float64(len(revised)+1)/float64(len(targets)))
		content, err := s.draftChat(ctx, user, contractPrompts["_REVISION_SYSTEM"], "FULL DOCUMENT (context — do not return it):\n"+original+fmt.Sprintf("\n\nSECTION TO REVISE: %d. %s\n\nREVISION INSTRUCTIONS:\n%s", n, str(sec, "title"), instructions), false, 32000)
		if err != nil {
			return nil, err
		}
		if content == "" {
			return nil, fail(502, "Revising a section returned nothing")
		}
		content = ensureSectionHeading(content, sec)
		content, gone := redactCaseReferences(content, []string{instructions, original}, nil, true)
		removed = append(removed, gone...)
		start, end := number(sec, "start", 0), number(sec, "end", 0)
		text = text[:start] + strings.TrimRight(content, "\r\n \t") + "\n\n" + strings.TrimLeft(text[end:], "\n")
		revised = append(revised, n)
	}
	slices.Reverse(revised)
	return M{"title": "", "text": text, "revised_sections": revised, "case_law_removed": removed}, nil
}
