package app

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

//go:embed research_prompts.json
var researchPromptsJSON []byte
var researchPrompts = func() M {
	m := M{}
	if err := json.Unmarshal(researchPromptsJSON, &m); err != nil {
		panic(err)
	}
	return m
}()
var researchRefRE = regexp.MustCompile(`^P\d+$`)
var researchBracketRE = regexp.MustCompile(`\[([^\]\n]+)\]`)
var researchSpaceRE = regexp.MustCompile(` {2,}`)
var researchPunctuationRE = regexp.MustCompile(` +([.,;:])`)
var researchDelimiterRE = regexp.MustCompile(`(?i)<<<\s*(?:BEGIN|END)\s+DOCUMENT\b[^<>\n]*(?:>>>)?`)
var researchParagraphRE = regexp.MustCompile(`\n\s*\n`)

func researchNeutralize(text string) string {
	return researchDelimiterRE.ReplaceAllString(text, "[document delimiter removed]")
}
func researchRefs(value any) []string {
	refs := []string{}
	for _, v := range stringsOf(value) {
		ref := strings.TrimSpace(strings.Trim(strings.TrimSpace(v), "[]"))
		if researchRefRE.MatchString(ref) {
			refs = append(refs, ref)
		}
	}
	return uniqueWorkflowStrings(refs)
}
func researchJoinRefs(text string, allowed []string) M {
	cited := []string{}
	stripped := 0
	text = researchBracketRE.ReplaceAllStringFunc(text, func(group string) string {
		parts := strings.Split(group[1:len(group)-1], ",")
		for _, part := range parts {
			if !researchRefRE.MatchString(strings.TrimSpace(part)) {
				return group
			}
		}
		kept := []string{}
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if slices.Contains(allowed, part) {
				kept = append(kept, part)
				cited = append(cited, part)
			} else {
				stripped++
			}
		}
		if len(kept) == 0 {
			return ""
		}
		return "[" + strings.Join(kept, ", ") + "]"
	})
	text = researchPunctuationRE.ReplaceAllString(researchSpaceRE.ReplaceAllString(text, " "), "$1")
	return M{"text": strings.TrimSpace(text), "cited": uniqueWorkflowStrings(cited), "stripped": stripped}
}
func normalizeResearchPlan(raw, available M, cap int) []M {
	out := []M{}
	seen := map[string]bool{}
	for _, item := range maps(raw["sub_questions"]) {
		question := strings.TrimSpace(str(item, "question"))
		if len([]rune(question)) < 3 || seen[strings.ToLower(question)] {
			continue
		}
		allowed := []string{}
		for _, tool := range []string{"documents", "case_law"} {
			if available[tool] != false {
				allowed = append(allowed, tool)
			}
		}
		tools := []string{}
		for _, tool := range stringsOf(item["tools"]) {
			tool = strings.ToLower(strings.TrimSpace(tool))
			if slices.Contains(allowed, tool) {
				tools = append(tools, tool)
			}
		}
		tools = uniqueWorkflowStrings(tools)
		if len(tools) == 0 {
			tools = allowed
		}
		if len(tools) == 0 {
			continue
		}
		seen[strings.ToLower(question)] = true
		out = append(out, M{"id": fmt.Sprintf("q%d", len(out)+1), "question": charSlice(question, 0, 1000), "tools": tools})
		if len(out) >= cap {
			break
		}
	}
	return out
}
func researchExcerpt(text, query string, maxChars int) string {
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	terms := uniqueWorkflowStrings(draftingTerms(query))
	spans := [][2]int{}
	pos := 0
	for _, sep := range researchParagraphRE.FindAllStringIndex(text, -1) {
		if sep[0] > pos {
			spans = append(spans, [2]int{pos, sep[0]})
		}
		pos = sep[1]
	}
	if pos < len(text) {
		spans = append(spans, [2]int{pos, len(text)})
	}
	if len(spans) == 0 {
		return string(runes[:maxChars])
	}
	best, score := spans[0], -1.0
	for _, span := range spans {
		points := 0
		words := draftingTerms(text[span[0]:span[1]])
		for _, term := range words {
			if slices.Contains(terms, term) {
				points++
			}
		}
		rank := float64(points) / math.Sqrt(float64(max(1, len(words))))
		if rank > score {
			score = rank
			best = span
		}
	}
	centre := (len([]rune(text[:best[0]])) + len([]rune(text[:best[1]]))) / 2
	start := max(0, min(centre-maxChars/2, len(runes)-maxChars))
	return string(runes[start : start+maxChars])
}
func researchPassageLines(passages []M, budget int) string {
	if len(passages) == 0 {
		return "(no passages were retrieved)"
	}
	per := max(300, min(4000, budget/len(passages)))
	lines := []string{}
	for _, p := range passages {
		label := str(p, "title")
		if str(p, "source_type") == "case_law" {
			label += " (" + fallback(str(p, "citation"), "no citation") + ")"
		}
		lines = append(lines, fmt.Sprintf("[%s] (%s: %s) %s", str(p, "ref"), str(p, "source_type"), label, charSlice(str(p, "text"), 0, per)))
	}
	return strings.Join(lines, "\n\n")
}

func researchSourceEntry(p M) M {
	out := M{"ref": p["ref"], "type": p["source_type"], "title": p["title"], "snippet": charSlice(str(p, "text"), 0, 400), "excerpt_source": fallback(str(p, "excerpt_source"), "index"), "sub_question_ids": p["sub_question_ids"]}
	for _, key := range []string{"document_id", "chunk_index", "opinion_id", "cluster_id", "citation", "url", "court", "date_filed", "score"} {
		out[key] = p[key]
	}
	return out
}
