package app

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

//go:embed contract_taxonomy.json
var contractTaxonomyJSON []byte

//go:embed contract_prompts.json
var contractPromptsJSON []byte
var contractTaxonomy = func() M {
	var m M
	if err := json.Unmarshal(contractTaxonomyJSON, &m); err != nil {
		panic(err)
	}
	return m
}()
var contractPrompts = func() map[string]string {
	var m map[string]string
	if err := json.Unmarshal(contractPromptsJSON, &m); err != nil {
		panic(err)
	}
	return m
}()

type clauseSegment struct {
	Text       string
	Start, End int
}

var clauseHeadingRE = regexp.MustCompile(`(?m)^(?:\s*\d+(?:\.\d+)*\s*\.?\s+[A-Z][A-Za-z0-9 ,\-/&'()]+\s*$|\s*ARTICLE\s+[IVXLCDM\d]+[.\s].*$|\s*SECTION\s+\d+(?:\.\d+)*\s*\.?\s*.*$|\s*[A-Z][A-Z &/\-]{3,40}\s*$|\s*\(?[a-z]\)\s+[A-Z][A-Za-z].*$)`)
var blankLineRE = regexp.MustCompile(`\n\s*\n`)

func clauseSegments(text string) []clauseSegment {
	out := []clauseSegment{}
	if strings.TrimSpace(text) == "" {
		return out
	}
	boundaries := []int{0, len(text)}
	for _, m := range clauseHeadingRE.FindAllStringIndex(text, -1) {
		boundaries = append(boundaries, m[0])
	}
	slices.Sort(boundaries)
	boundaries = slices.Compact(boundaries)
	for i := 0; i+1 < len(boundaries); i++ {
		start, end := boundaries[i], boundaries[i+1]
		body := strings.TrimSpace(text[start:end])
		if body == "" || (len([]rune(body)) < 60 && !strings.Contains(body, "\n")) {
			continue
		}
		out = append(out, clauseSegment{body, start, end})
	}
	if len(out) < 3 {
		out = []clauseSegment{}
		cursor := 0
		for _, chunk := range blankLineRE.Split(text, -1) {
			stripped := strings.TrimSpace(chunk)
			if stripped == "" || len([]rune(stripped)) <= 60 {
				continue
			}
			start := strings.Index(text[cursor:], chunk)
			if start < 0 {
				start = cursor
			} else {
				start += cursor
			}
			out = append(out, clauseSegment{stripped, start, start + len(chunk)})
			cursor = start + len(chunk)
		}
	}
	return out
}

var clauseRegex = func() map[string][]*regexp.Regexp {
	out := map[string][]*regexp.Regexp{}
	for _, canonical := range maps(contractTaxonomy["CANONICAL_CLAUSES"]) {
		for _, p := range stringsOf(canonical["regex_anchors"]) {
			out[str(canonical, "slug")] = append(out[str(canonical, "slug")], regexp.MustCompile("(?i)"+p))
		}
	}
	return out
}()
var subElementRegex = func() map[string][]*regexp.Regexp {
	out := map[string][]*regexp.Regexp{}
	for key, patterns := range object(contractTaxonomy["sub_element_patterns"]) {
		for _, pattern := range stringsOf(patterns) {
			out[key] = append(out[key], regexp.MustCompile(pattern))
		}
	}
	return out
}()

func detectSubElements(text string, canonical M) []string {
	out := []string{}
	lower := strings.ToLower(text)
	for _, sub := range maps(canonical["sub_elements"]) {
		key := str(sub, "key")
		found := false
		for _, pattern := range subElementRegex[key] {
			if pattern.MatchString(text) {
				found = true
				break
			}
		}
		if !found {
			words := []string{}
			for _, w := range strings.Fields(str(sub, "name")) {
				if len([]rune(w)) > 4 {
					words = append(words, strings.ToLower(w))
				}
			}
			found = len(words) > 0
			for _, w := range words {
				found = found && strings.Contains(lower, w)
			}
		}
		if found {
			out = append(out, key)
		}
	}
	return out
}
func cosine(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	dot, aa, bb := 0.0, 0.0, 0.0
	for i, x := range a {
		dot += x * b[i]
		aa += x * x
		bb += b[i] * b[i]
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / (math.Sqrt(aa) * math.Sqrt(bb))
}
func keywordOverlap(text string, words []string) float64 {
	if len(words) == 0 {
		return 0
	}
	hits := 0
	for _, w := range words {
		if strings.Contains(strings.ToLower(text), strings.ToLower(w)) {
			hits++
		}
	}
	return float64(hits) / float64(len(words))
}

func (s *Server) clauseIntelligence(ctx context.Context, user, text, contractType, jurisdiction string) (M, error) {
	segments := clauseSegments(text)
	canonicals := maps(contractTaxonomy["CANONICAL_CLAUSES"])
	canonicalVectors := [][]float64{}
	segmentVectors := [][]float64{}
	warnings := []string{}
	if len(segments) > 0 {
		route, routeErr := s.clauseEmbeddingRoute(ctx, user)
		cacheKey := "clause-embeddings:" + digest(route.Provider+"|"+route.Endpoint+"|"+route.Model+"|"+digest(route.Key)+"|"+string(contractTaxonomyJSON))
		err := routeErr
		if err == nil {
			err = s.kvGet(ctx, cacheKey, &canonicalVectors)
		}
		if err != nil || len(canonicalVectors) != len(canonicals) {
			texts := []string{}
			for _, c := range canonicals {
				texts = append(texts, str(c, "market_standard_text"))
			}
			if routeErr == nil {
				canonicalVectors, err = s.contractEmbeddingsAt(ctx, route, texts)
			}
			if err == nil {
				if e := s.kvSet(ctx, cacheKey, canonicalVectors, 0); e != nil {
					s.log.Warn("clause embedding cache write failed", "error", e)
				}
			}
		}
		if err == nil {
			texts := []string{}
			for _, seg := range segments {
				texts = append(texts, seg.Text)
			}
			segmentVectors, err = s.contractEmbeddingsAt(ctx, route, texts)
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			warnings = append(warnings, "Semantic clause classification was unavailable; regex classification was used.")
			s.log.Warn("clause embeddings unavailable", "error", err)
		}
	}
	tags := []M{}
	deviations := []M{}
	tagged := map[string]bool{}
	for i, seg := range segments {
		for j, c := range canonicals {
			slug := str(c, "slug")
			score, method := 0.0, ""
			for _, pattern := range clauseRegex[slug] {
				if pattern.MatchString(seg.Text) {
					score, method = 0.95, "regex"
					break
				}
			}
			if len(segmentVectors) > i && len(canonicalVectors) > j {
				sim := cosine(segmentVectors[i], canonicalVectors[j])
				hybrid := 0.7*sim + 0.3*keywordOverlap(seg.Text, stringsOf(c["required_keywords"]))
				if sim >= 0.55 && hybrid >= 0.5 && max(hybrid, sim) > score {
					candidate := max(hybrid, sim)
					method = "embedding"
					if score > 0 {
						method = "hybrid"
					}
					score = candidate
				}
			}
			if score == 0 {
				continue
			}
			tagged[slug] = true
			subs := detectSubElements(seg.Text, c)
			tag := mergeMap(M{"canonical_slug": slug, "matched_text": charSlice(seg.Text, 0, 600), "method": method, "confidence": math.RoundToEven(score*1000) / 1000, "detected_sub_elements": subs}, spanFields(text, seg.Start, seg.End))
			tags = append(tags, tag)
			deviations = append(deviations, clauseDeviations(c, seg.Text, subs, tag)...)
		}
	}
	missing := missingClauses(contractType, tagged)
	flags := []M{}
	jurisdiction = strings.ToUpper(strings.TrimSpace(jurisdiction))
	for _, rule := range maps(contractTaxonomy["JURISDICTION_RULES"]) {
		if strings.ToUpper(str(rule, "jurisdiction")) != jurisdiction || !tagged[str(rule, "canonical_slug")] {
			continue
		}
		severity := map[string]string{"void": "critical", "limited": "major", "reformable": "major", "unsettled": "informational", "enforceable": "informational"}[str(rule, "enforceability")]
		flags = append(flags, M{"canonical_slug": rule["canonical_slug"], "jurisdiction": jurisdiction, "enforceability": rule["enforceability"], "severity": fallback(severity, "major"), "note": rule["note"], "constraints": object(rule["constraints"]), "authorities": rule["authorities"], "recommended_text": rule["recommended_text"], "as_of": object(rule["constraints"])["as_of"]})
	}
	counts := M{}
	for _, d := range deviations {
		sev := str(d, "severity")
		counts[sev] = number(counts, sev, 0) + 1
	}
	return M{"contract_type": contractType, "jurisdiction": nullable(jurisdiction), "document_length_chars": len([]rune(text)), "tags": tags, "missing": missing, "deviations": deviations, "jurisdiction_flags": flags, "warnings": warnings, "summary": M{"tags_found": len(tags), "deviations_found": len(deviations), "deviations_by_severity": counts, "missing_required": len(maps(missing["missing_required"])), "missing_recommended": len(maps(missing["missing_recommended"])), "jurisdiction_flags": len(flags)}}, nil
}
func missingClauses(contractType string, tagged map[string]bool) M {
	requirements := object(object(contractTaxonomy["CONTRACT_TYPE_REQUIREMENTS"])[strings.ToLower(strings.TrimSpace(contractType))])
	out := M{"confidence": 1.0}
	if len(requirements) == 0 && contractType != "" {
		out["confidence"] = 0.5
	}
	bySlug := map[string]M{}
	for _, c := range maps(contractTaxonomy["CANONICAL_CLAUSES"]) {
		bySlug[str(c, "slug")] = c
	}
	for _, bucket := range []string{"required", "recommended"} {
		slugs := stringsOf(requirements[bucket])
		missing := []M{}
		for _, slug := range slugs {
			if tagged[slug] {
				continue
			}
			c := bySlug[slug]
			missing = append(missing, M{"canonical_slug": slug, "name": c["name"], "category": c["category"], "rationale": c["description"]})
		}
		coverage := 100.0
		if len(slugs) > 0 {
			coverage = math.RoundToEven(float64(len(slugs)-len(missing))/float64(len(slugs))*1000) / 10
		}
		out["missing_"+bucket] = missing
		out["coverage_"+bucket+"_pct"] = coverage
	}
	return out
}

var clauseMutualRE = regexp.MustCompile(`(?i)\b(?:each|either|both)\s+part(?:y|ies)|mutual`)
var clauseOnePartyRE = regexp.MustCompile(`(?i)\b(?:provider|customer|licensee|licensor|company|recipient|employee)\s+(?:shall|will|agrees)`)
var durationRE = regexp.MustCompile(`(?i)\b(\d{1,3}|one|two|three|four|five|six|seven|eight|nine|ten|twelve|fifteen|eighteen|twenty[\s-]?four|thirty[\s-]?six)\s+(?:\(\d+\)\s+)?(month|months|year|years)\b`)
var dollarCapRE = regexp.MustCompile(`\$\s?[\d,]+(?:\.\d+)?(?:\s*(?:million|thousand|M|K))?`)
var feesCapRE = regexp.MustCompile(`(?i)(?:fees\s+paid|amounts?\s+paid|amounts?\s+payable).{0,80}(?:in\s+the\s+(?:twelve|six)\s*\(?\d*\)?\s*months?|preceding|prior)`)

func clauseMutuality(text string) string {
	if clauseMutualRE.MatchString(text) {
		return "mutual"
	}
	if clauseOnePartyRE.MatchString(text) {
		return "unilateral"
	}
	return ""
}
func clauseDeviations(canonical M, text string, detected []string, tag M) []M {
	out := []M{}
	slug := str(canonical, "slug")
	add := func(kind, sub, sev string, detail M) {
		out = append(out, M{"canonical_slug": slug, "deviation_type": kind, "sub_element": sub, "severity": sev, "matched_text": charSlice(text, 0, 600), "span_start": tag["span_start"], "span_end": tag["span_end"], "classifier_confidence": tag["confidence"], "detail": detail})
	}
	mutual := false
	for _, sub := range maps(canonical["sub_elements"]) {
		key := str(sub, "key")
		if key == "mutual" {
			mutual = true
		}
		if slices.Contains(detected, key) {
			continue
		}
		kind, sev := "weakened", "minor"
		required := sub["required"] == true
		if required {
			kind = "missing_subelement"
			sev = fallback(str(object(contractTaxonomy["sub_element_severity"]), slug+"/"+key), "major")
		}
		add(kind, key, sev, M{"missing": key, "name": sub["name"], "description": sub["description"], "required": required})
	}
	if mutual {
		expected, actual := clauseMutuality(str(canonical, "market_standard_text")), clauseMutuality(text)
		if expected != "" && actual != "" && expected != actual {
			add(expected+"_to_"+actual, "mutual", "major", M{"canonical_mutuality": expected, "observed_mutuality": actual})
		}
	}
	if slug == "limitation_of_liability" && !dollarCapRE.MatchString(text) && !feesCapRE.MatchString(text) {
		add("cap_change", "aggregate_cap", "critical", M{"observation": "no numeric or fees-paid cap detected"})
	}
	if slug == "non_compete" {
		months := -1
		for _, m := range durationRE.FindAllStringSubmatch(text, -1) {
			n, ok := wordsToInt(m[1])
			if !ok {
				continue
			}
			if strings.HasPrefix(strings.ToLower(m[2]), "year") {
				n *= 12
			}
			months = max(months, n)
		}
		if months < 0 {
			add("scope_change", "duration", "major", M{"observation": "no duration detected — likely overbroad"})
		} else if months > 24 {
			add("scope_change", "duration", "major", M{"observed_months": months, "common_outer_limit_months": 24})
		}
	}
	if slug == "termination_for_cause" && !slices.Contains(detected, "cure_period") {
		add("missing_subelement", "cure_period", "major", M{"observation": "cure period not stated — termination may be too aggressive"})
	}
	return out
}

func comparisonSections(text string) []M {
	out := []M{}
	for _, seg := range clauseSegments(text) {
		line := strings.TrimSpace(strings.SplitN(seg.Text, "\n", 2)[0])
		out = append(out, M{"title": fallback(charSlice(line, 0, 80), "(untitled)"), "start": byteToUTF16(text, seg.Start), "end": byteToUTF16(text, seg.End), "preview": charSlice(seg.Text, 0, 600)})
	}
	if len(out) == 0 && strings.TrimSpace(text) != "" {
		out = append(out, M{"title": charSlice(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0], 0, 80), "start": 0, "end": byteToUTF16(text, len(text)), "preview": charSlice(text, 0, 600)})
	}
	return out
}
func comparisonBlock(label string, sections []M, budget int) string {
	lines := []string{"=== " + label + " ==="}
	used := len([]rune(lines[0]))
	for i, sec := range sections {
		entry := fmt.Sprintf("[%s §%d: %s]\n%s", label, i+1, str(sec, "title"), str(sec, "preview"))
		if used+len([]rune(entry)) > budget {
			lines = append(lines, "[... remaining sections omitted for length ...]")
			break
		}
		used += len([]rune(entry))
		lines = append(lines, entry)
	}
	return strings.Join(lines, "\n\n")
}
