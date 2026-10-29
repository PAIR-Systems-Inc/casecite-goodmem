package app

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

//go:embed case_guard_patterns.json
var caseGuardPatternsJSON []byte
var caseGuardPatterns = func() M {
	var m M
	if err := json.Unmarshal(caseGuardPatternsJSON, &m); err != nil {
		panic(err)
	}
	return m
}()
var caseNameRE = regexp.MustCompile(str(caseGuardPatterns, "_CASE_NAME_RE"))
var singleCaseRE = regexp.MustCompile(str(caseGuardPatterns, "_SINGLE_PARTY_RE"))
var reporterCiteRE = regexp.MustCompile(str(caseGuardPatterns, "_REPORTER_CITE_RE"))
var westlawCiteRE = regexp.MustCompile(str(caseGuardPatterns, "_WL_CITE_RE"))
var versusRE = regexp.MustCompile(`\s+vs?\.?\s+`)
var citeStartRE = regexp.MustCompile(`[,;]?\s*\(?\b\d{1,4}\s+[A-Z]`)
var partyWordsRE = regexp.MustCompile(`[a-z0-9]+`)

const caseRedaction = "[unverified case-law reference removed]"

func normalizeCase(text string) string {
	text = strings.Map(func(r rune) rune {
		if wordRune(r) || unicode.IsSpace(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, text)
	words := strings.Fields(text)
	for i, w := range words {
		if w == "vs" || w == "versus" {
			words[i] = "v"
		}
	}
	return strings.Join(words, " ")
}
func unverifiedCases(content string, sources []string, authorities []M) [][2]int {
	texts := []string{}
	type namePair struct{ left, right []string }
	names := []namePair{}
	add := func(text string) {
		if norm := normalizeCase(text); norm != "" {
			texts = append(texts, " "+norm+" ")
		}
	}
	for _, source := range sources {
		add(source)
	}
	for _, result := range authorities {
		metadata := object(result["metadata"])
		for _, key := range []string{"case_name", "citation", "filename"} {
			add(str(metadata, key))
		}
		add(str(result, "text"))
		for _, key := range []string{"case_name", "filename"} {
			name := normalizeCase(strings.SplitN(str(metadata, key), "(", 2)[0])
			a, b, ok := strings.Cut(name, " v ")
			if ok && a != "" && b != "" {
				names = append(names, namePair{strings.Fields(a), strings.Fields(b)})
			}
		}
	}
	hasPhrase := func(phrase string) bool {
		needle := " " + phrase + " "
		for _, text := range texts {
			if strings.Contains(text, needle) {
				return true
			}
		}
		return false
	}
	allowedName := func(left, right string) bool {
		a, b := strings.Fields(normalizeCase(left)), strings.Fields(normalizeCase(right))
		for len(a) > 1 && slices.Contains(stringsOf(caseGuardPatterns["lead_in_words"]), a[0]) {
			a = a[1:]
		}
		if len(a) == 0 || len(b) == 0 {
			return false
		}
		for _, name := range names {
			match := true
			for _, w := range a {
				if !slices.Contains(name.left, w) {
					match = false
					break
				}
			}
			if match && slices.Contains(name.right, b[0]) {
				return true
			}
		}
		return hasPhrase(strings.Join(a, " ") + " v " + b[0])
	}
	spans := [][2]int{}
	trimEnd := func(start, end int) int {
		for end > start && strings.ContainsRune(".,;:", rune(content[end-1])) {
			end--
		}
		return end
	}
	for _, m := range caseNameRE.FindAllStringSubmatchIndex(content, -1) {
		if !allowedName(content[m[2]:m[3]], content[m[4]:m[5]]) {
			spans = append(spans, [2]int{m[0], trimEnd(m[0], m[1])})
		}
	}
	for _, m := range singleCaseRE.FindAllStringSubmatchIndex(content, -1) {
		prefix := normalizeCase(content[m[2]:m[3]])
		party := strings.Fields(normalizeCase(content[m[4]:m[5]]))
		if len(party) == 0 {
			continue
		}
		variants := []string{prefix}
		if prefix == "in the matter of" {
			variants = append(variants, "matter of")
		} else if prefix == "matter of" {
			variants = append(variants, "in the matter of")
		}
		allowed := false
		for _, v := range variants {
			allowed = allowed || hasPhrase(v+" "+party[0])
		}
		if !allowed {
			spans = append(spans, [2]int{m[0], trimEnd(m[0], m[1])})
		}
	}
	for _, re := range []*regexp.Regexp{reporterCiteRE, westlawCiteRE} {
		for _, m := range re.FindAllStringIndex(content, -1) {
			if !hasPhrase(normalizeCase(content[m[0]:m[1]])) {
				spans = append(spans, [2]int{m[0], m[1]})
			}
		}
	}
	slices.SortFunc(spans, func(a, b [2]int) int {
		if a[0] != b[0] {
			return a[0] - b[0]
		}
		return a[1] - b[1]
	})
	merged := [][2]int{}
	for _, p := range spans {
		if len(merged) == 0 {
			merged = append(merged, p)
			continue
		}
		prev := &merged[len(merged)-1]
		adjacent := p[0] <= prev[1]
		if !adjacent {
			gap := content[prev[1]:p[0]]
			adjacent = len([]rune(gap)) <= 3 && strings.Trim(gap, " ,;:(") == ""
		}
		if adjacent {
			prev[1] = max(prev[1], p[1])
		} else {
			merged = append(merged, p)
		}
	}
	return merged
}
func redactCaseReferences(text string, sources []string, authorities []M, knownParties bool) (string, []string) {
	probe := strings.ReplaceAll(text, "\n", "|")
	spans := unverifiedCases(probe, sources, authorities)
	removed := []string{}
	if knownParties && len(spans) > 0 {
		words := partyWordsRE.FindAllString(strings.ToLower(strings.Join(sources, " ")), -1)
		kept := [][2]int{}
		excluded := append([]string{"v", "vs"}, stringsOf(caseGuardPatterns["lead_in_words"])...)
		for _, span := range spans {
			reference := text[span[0]:span[1]]
			captionEnd := 0
			if versusRE.MatchString(reference) {
				name := reference
				if cite := citeStartRE.FindStringIndex(reference); cite != nil {
					name = reference[:cite[0]]
				}
				partyWords := []string{}
				for _, w := range partyWordsRE.FindAllString(strings.ToLower(name), -1) {
					if !slices.Contains(excluded, w) {
						partyWords = append(partyWords, w)
					}
				}
				all := len(partyWords) > 0
				for _, w := range partyWords {
					all = all && slices.Contains(words, w)
				}
				if all {
					captionEnd = len(name)
				}
			}
			start := span[0] + captionEnd
			for start < span[1] && strings.ContainsRune(", ;(", rune(text[start])) {
				start++
			}
			if start < span[1] {
				kept = append(kept, [2]int{start, span[1]})
			}
		}
		spans = kept
	}
	for _, span := range spans {
		removed = append(removed, text[span[0]:span[1]])
	}
	for i := len(spans) - 1; i >= 0; i-- {
		p := spans[i]
		text = text[:p[0]] + caseRedaction + text[p[1]:]
	}
	return text, removed
}
