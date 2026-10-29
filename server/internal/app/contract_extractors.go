package app

// These deterministic rules are ported from upstream's parties.py,
// obligations.py, deadlines.py and defined_terms.py at 6605cb3. Golden tests
// execute those Python files independently; see scripts/parity/extractor_fixtures.py.

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

func charSlice(text string, start, end int) string {
	r := []rune(text)
	return string(r[max(0, min(start, len(r))):max(0, min(end, len(r)))])
}
func byteToUTF16(text string, offset int) int { return len(utf16.Encode([]rune(text[:offset]))) }
func spanFields(text string, start, end int) M {
	return M{"span_start": byteToUTF16(text, start), "span_end": byteToUTF16(text, end)}
}
func firstSpan(text string, start, end int) M {
	return M{"first_span_start": byteToUTF16(text, start), "first_span_end": byteToUTF16(text, end)}
}
func mergeMap(dst, src M) M {
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
func matchGroup(re *regexp.Regexp, text string, indexes []int, name string) string {
	i := re.SubexpIndex(name) * 2
	if i < 0 || i+1 >= len(indexes) || indexes[i] < 0 {
		return ""
	}
	return text[indexes[i]:indexes[i+1]]
}
func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
func wordBoundary(text string, at int) bool {
	left, right := false, false
	if at > 0 {
		r, _ := utf8.DecodeLastRuneInString(text[:at])
		left = wordRune(r)
	}
	if at < len(text) {
		r, _ := utf8.DecodeRuneInString(text[at:])
		right = wordRune(r)
	}
	return left != right
}
func literalWordMatches(text, word string, fold bool) [][2]int {
	flags := ""
	if fold {
		flags = "(?i)"
	}
	re := regexp.MustCompile(flags + regexp.QuoteMeta(word))
	out := [][2]int{}
	for _, p := range re.FindAllStringIndex(text, -1) {
		if wordBoundary(text, p[0]) && wordBoundary(text, p[1]) {
			out = append(out, [2]int{p[0], p[1]})
		}
	}
	return out
}

var definedPartyRE = regexp.MustCompile(`(?P<entity>(?:[A-Z][A-Za-z0-9.\-&/]+\s*){1,6}(?:,\s+(?:a|an)\s+[A-Za-z][A-Za-z &\-]{2,60}\s+(?:corporation|company|LLC|L\.L\.C\.|Inc\.?|partnership|LP|LLP|trust|foundation|association|individual))?)\s*\(\s*['"](?P<alias>[A-Z][A-Za-z &\-]{1,40})['"]\s*\)`)
var partyPrefixRE = regexp.MustCompile(`(?i)^\s*(?:by\s+and\s+between\s+|between\s+|This\s+|the\s+)`)
var partyDateRE = regexp.MustCompile(`^[A-Z][a-z]+\s+\d{1,2},\s+\d{4}`)
var betweenPartyRE = regexp.MustCompile(`(?i)by\s+and\s+between\s+(?P<a>[A-Z][A-Za-z0-9 ,&\-./]{2,120})\s+and\s+(?P<b>[A-Z][A-Za-z0-9 ,&\-./]{2,120})`)
var genericRoles = []string{"Provider", "Customer", "Client", "Licensor", "Licensee", "Buyer", "Seller", "Disclosing Party", "Receiving Party", "Discloser", "Recipient", "Indemnitor", "Indemnitee", "Company", "Employee", "Contractor", "Vendor", "Supplier", "Lessor", "Lessee", "Landlord", "Tenant"}

func partyRole(alias string) any {
	switch strings.ToLower(alias) {
	case "provider", "licensor", "discloser", "disclosing party", "vendor", "supplier", "lessor", "landlord", "indemnitor":
		return "provider"
	case "customer", "client", "licensee", "recipient", "receiving party", "buyer", "lessee", "tenant", "indemnitee":
		return "customer"
	case "company", "employer":
		return "company"
	case "employee", "contractor":
		return "individual"
	}
	return nil
}
func resolveParties(text string) []M {
	head := charSlice(text, 0, 4000)
	out := []M{}
	byAlias := map[string]M{}
	add := func(key string, item M) { byAlias[key] = item; out = append(out, item) }
	for _, m := range definedPartyRE.FindAllStringSubmatchIndex(head, -1) {
		if m[0] > 0 {
			r, _ := utf8.DecodeLastRuneInString(head[:m[0]])
			if wordRune(r) {
				continue
			}
		}
		entity := strings.Trim(matchGroup(definedPartyRE, head, m, "entity"), " ,.-\n\t")
		entity = strings.Trim(partyPrefixRE.ReplaceAllString(entity, ""), " ,.-")
		if utf8.RuneCountInString(entity) < 2 || partyDateRE.MatchString(entity) {
			continue
		}
		alias := strings.TrimSpace(matchGroup(definedPartyRE, head, m, "alias"))
		key := strings.ToLower(alias)
		if item, ok := byAlias[key]; ok {
			aliases := stringsOf(item["aliases"])
			for _, v := range []string{alias, entity} {
				if !slices.Contains(aliases, v) {
					aliases = append(aliases, v)
				}
			}
			item["aliases"] = aliases
			continue
		}
		add(key, mergeMap(M{"canonical_name": entity, "role": partyRole(alias), "aliases": []string{alias, entity}, "detection_method": "defined", "confidence": 1.0}, firstSpan(head, m[0], m[1])))
	}
	if len(out) == 0 {
		if m := betweenPartyRE.FindStringSubmatchIndex(head); m != nil {
			for _, group := range []string{"a", "b"} {
				i := betweenPartyRE.SubexpIndex(group) * 2
				name := strings.Trim(head[m[i]:m[i+1]], " ,.-")
				key := strings.ToLower(name)
				item := mergeMap(M{"canonical_name": name, "role": nil, "aliases": []string{name}, "detection_method": "header", "confidence": 0.85}, firstSpan(head, m[i], m[i+1]))
				if old, ok := byAlias[key]; ok {
					mergeMap(old, item)
				} else {
					add(key, item)
				}
			}
		}
	}
	for _, alias := range genericRoles {
		matches := literalWordMatches(head, alias, false)
		key := strings.ToLower(alias)
		if len(matches) > 0 && byAlias[key] == nil {
			m := matches[0]
			add(key, mergeMap(M{"canonical_name": alias, "role": partyRole(alias), "aliases": []string{alias}, "detection_method": "heuristic", "confidence": 0.6}, firstSpan(head, m[0], m[1])))
		}
	}
	return out
}

var sentenceBoundaryRE = regexp.MustCompile(`[.!?]\s+[A-Z("']`)
var modalRE = regexp.MustCompile(`(?i)\b(shall(?:\s+not)?|must(?:\s+not)?|will(?:\s+not)?|agrees?\s+to|is\s+required\s+to|is\s+obligated\s+to)\b`)
var conditionRE = regexp.MustCompile(`(?i)\b(upon|if|when|whenever|subject to|provided that|provided,? however,?|in the event(?: that)?|on the condition that|to the extent (?:that|of))\b`)
var mutualRE = regexp.MustCompile(`(?i)\b(each|both|either|neither)\b`)
var perpetualRE = regexp.MustCompile(`(?i)\b(perpetual|in\s+perpetuity|forever)\b`)
var continuingRE = regexp.MustCompile(`(?i)\b(at\s+all\s+times|continuing|throughout\s+the\s+term)\b`)
var obligationCategories = []struct {
	name string
	re   *regexp.Regexp
}{
	{"payment", regexp.MustCompile(`(?i)\b(pay|invoice|fee|fees|amount|price|reimburs|refund|royalt)`)},
	{"notice", regexp.MustCompile(`(?i)\b(notice|notif|inform|advise)\b`)},
	{"delivery", regexp.MustCompile(`(?i)\b(deliver|provide|furnish|supply|ship|installat)`)},
	{"confidentiality", regexp.MustCompile(`(?i)\b(confidential|disclos(?:e|ure)|protect)\b`)},
	{"ip", regexp.MustCompile(`(?i)\b(intellectual property|copyright|patent|trademark|license|assign)`)},
	{"termination", regexp.MustCompile(`(?i)\b(terminat|expir|cancel)`)},
	{"warranty", regexp.MustCompile(`(?i)\b(warrant|represent)`)},
	{"indemnity", regexp.MustCompile(`(?i)\b(indemnif|hold harmless|defend)`)},
	{"compliance", regexp.MustCompile(`(?i)\b(complian|complies|comply|laws?|regulations?|FCPA|sanction|export)`)},
	{"reporting", regexp.MustCompile(`(?i)\b(report|certify|certificate|statement|audit)`)},
}

func sentenceSpans(text string) [][2]int {
	out := [][2]int{}
	cursor := 0
	search := 0
	for search < len(text) {
		m := sentenceBoundaryRE.FindStringIndex(text[search:])
		if m == nil {
			break
		}
		start, end := search+m[0], search+m[1]
		out = append(out, [2]int{cursor, start + 1})
		cursor = end - 1
		search = cursor
	}
	if cursor < len(text) {
		out = append(out, [2]int{cursor, len(text)})
	}
	return out
}
func obligationSubject(left string, parties []M) string {
	type pair struct{ alias, name string }
	candidates := []pair{}
	for _, party := range parties {
		for _, alias := range stringsOf(party["aliases"]) {
			candidates = append(candidates, pair{alias, str(party, "canonical_name")})
		}
	}
	slices.SortStableFunc(candidates, func(a, b pair) int { return utf8.RuneCountInString(b.alias) - utf8.RuneCountInString(a.alias) })
	for _, c := range candidates {
		if len(literalWordMatches(left, c.alias, true)) > 0 {
			return c.name
		}
	}
	for _, generic := range []string{"Each party", "Either party", "Neither party", "The parties", "Both parties", "The Company", "The Employee"} {
		if len(literalWordMatches(left, generic, true)) > 0 {
			return generic
		}
	}
	return ""
}
func extractObligations(text string, parties []M) []M {
	out := []M{}
	seen := map[string]bool{}
	skip := strings.Fields("not promptly immediately timely reasonably in no event have be the a an")
	for _, span := range sentenceSpans(text) {
		sentence := text[span[0]:span[1]]
		for _, m := range modalRE.FindAllStringIndex(sentence, -1) {
			matched := charSlice(sentence, 0, 600)
			if seen[matched] {
				continue
			}
			seen[matched] = true
			subject := obligationSubject(sentence[:m[0]], parties)
			modal := strings.Join(strings.Fields(strings.ToLower(sentence[m[0]:m[1]])), " ")
			switch modal {
			case "agree to", "agrees to":
				modal = "agrees_to"
			case "is required to":
				modal = "is_required_to"
			case "is obligated to":
				modal = "is_obligated_to"
			}
			after := strings.TrimSpace(sentence[m[1]:])
			tokens := strings.Fields(after)
			i := 0
			for i < len(tokens) && slices.Contains(skip, strings.ToLower(strings.TrimRight(tokens[i], ",.;:"))) {
				i++
			}
			action, obj := "(unspecified)", after
			if i < len(tokens) {
				action = strings.TrimSpace(charSlice(strings.TrimRight(strings.Join(tokens[i:min(i+9, len(tokens))], " "), ",.;:"), 0, 120))
				obj = strings.Join(tokens[i+1:], " ")
			}
			conditions := conditionRE.FindAllString(sentence, -1)
			if conditions == nil {
				conditions = []string{}
			}
			var category any
			for _, c := range obligationCategories {
				if c.re.MatchString(sentence) {
					category = c.name
					break
				}
			}
			confidence := 0.7
			if subject != "" {
				confidence = 0.9
			}
			out = append(out, mergeMap(M{"subject_party": nullable(subject), "modal": modal, "action": action, "object_text": charSlice(obj, 0, 600), "conditions": conditions, "deadlines": []string{}, "matched_text": matched, "category": category, "is_unilateral": subject != "" && !mutualRE.MatchString(sentence), "is_perpetual": perpetualRE.MatchString(sentence), "is_continuing": continuingRE.MatchString(sentence), "confidence": confidence, "detection_method": "rule"}, spanFields(text, span[0], span[1])))
			if len(out) >= 150 {
				return out
			}
		}
	}
	return out
}
func linkDeadlines(obligations, deadlines []M) {
	for _, d := range deadlines {
		if d["span_start"] == nil {
			continue
		}
		label := fallback(str(d, "description"), str(d, "matched_text"))
		if label == "" {
			continue
		}
		start := number(d, "span_start", -1)
		for _, o := range obligations {
			if number(o, "span_start", 0) <= start && start <= number(o, "span_end", 0) {
				o["deadlines"] = append(stringsOf(o["deadlines"]), label)
				break
			}
		}
	}
}

var numberWords = map[string]int{"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15, "sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19, "twenty": 20, "thirty": 30, "forty": 40, "fifty": 50, "sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90, "hundred": 100}
var quantityPattern = func() string {
	words := []string{}
	for word := range numberWords {
		if word != "hundred" {
			words = append(words, word)
		}
	}
	slices.SortFunc(words, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	alt := strings.Join(words, "|")
	return `(?P<num>\d{1,4}|(?:` + alt + `)(?:[-\s]+(?:hundred|and|` + alt + `))*)\s*(?:\(\s*(?P<paren>\d{1,4})\s*\)\s*)?`
}()
var offsetRE = regexp.MustCompile(`(?i)(?P<adv>within|no\s+later\s+than|not\s+later\s+than|on\s+or\s+before|at\s+least|no\s+more\s+than)?\s*` + quantityPattern + `(?P<unit>days?|business\s+days?|calendar\s+days?|months?|years?|weeks?)(?P<rel>\s+(?:after|prior\s+to|before|preceding|following))?`)
var periodRE = regexp.MustCompile(`(?i)\b(?:for\s+a\s+(?:period\s+of\s+)?|during\s+the\s+|term\s+of\s+|of\s+)?` + quantityPattern + `(?P<unit>days?|months?|years?)\s+(?:term|period|following|after|thereafter|after the)`)
var absoluteDateRE = regexp.MustCompile(`\b(?:Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:tember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\s+\d{1,2},\s+\d{4}\b`)

func wordsToInt(value string) (int, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if n, e := strconv.Atoi(value); e == nil {
		return n, true
	}
	total := 0
	for _, w := range strings.Fields(strings.ReplaceAll(value, "-", " ")) {
		if w == "and" {
			continue
		}
		if w == "hundred" {
			total = max(total, 1) * 100
			continue
		}
		n, ok := numberWords[w]
		if !ok {
			return 0, false
		}
		total += n
	}
	return total, total != 0
}
func deadlineQuantity(re *regexp.Regexp, text string, m []int) (int, bool) {
	if p := matchGroup(re, text, m, "paren"); p != "" {
		return wordsToInt(p)
	}
	return wordsToInt(matchGroup(re, text, m, "num"))
}
func unitDays(unit string, n int) int {
	unit = strings.ToLower(unit)
	switch {
	case strings.Contains(unit, "day"):
		return n
	case strings.Contains(unit, "week"):
		return n * 7
	case strings.Contains(unit, "month"):
		return n * 30
	case strings.Contains(unit, "year"):
		return n * 365
	}
	return n
}
func deadlineAnchor(blob string) any {
	blob = strings.ToLower(blob)
	for _, entry := range []struct {
		key   string
		words []string
	}{{"effective_date", []string{"effective date"}}, {"invoice_date", []string{"invoice"}}, {"termination", []string{"termination", "expir"}}, {"notice", []string{"notice"}}, {"execution_date", []string{"execution", "signing"}}, {"delivery", []string{"delivery"}}} {
		for _, word := range entry.words {
			if strings.Contains(blob, word) {
				return entry.key
			}
		}
	}
	return nil
}
func extractDeadlines(text string, effective *time.Time) []M {
	out := []M{}
	base := func(kind string, start, end int) M {
		return mergeMap(M{"kind": kind, "description": text[start:end], "anchor": nil, "offset_days": nil, "period_days": nil, "resolved_date": nil, "matched_text": text[start:end]}, spanFields(text, start, end))
	}
	for _, m := range offsetRE.FindAllStringSubmatchIndex(text, -1) {
		n, ok := deadlineQuantity(offsetRE, text, m)
		if !ok {
			continue
		}
		days := unitDays(matchGroup(offsetRE, text, m, "unit"), n)
		rel := strings.ToLower(matchGroup(offsetRE, text, m, "rel"))
		if strings.Contains(rel, "before") || strings.Contains(rel, "prior") || strings.Contains(rel, "preceding") {
			days = -days
		}
		left := []rune(text[:m[0]])
		right := []rune(text[m[1]:])
		anchor := deadlineAnchor(string(left[max(0, len(left)-60):]) + " " + string(right[:min(80, len(right))]))
		item := base("relative_offset", m[0], m[1])
		item["anchor"] = anchor
		item["offset_days"] = days
		item["matched_text"] = string(left[max(0, len(left)-30):]) + text[m[0]:m[1]] + string(right[:min(60, len(right))])
		if effective != nil && anchor == "effective_date" {
			item["resolved_date"] = effective.AddDate(0, 0, days).Format("2006-01-02T15:04:05")
		}
		out = append(out, item)
	}
	for _, m := range periodRE.FindAllStringSubmatchIndex(text, -1) {
		n, ok := deadlineQuantity(periodRE, text, m)
		if !ok {
			continue
		}
		item := base("period", m[0], m[1])
		item["period_days"] = unitDays(matchGroup(periodRE, text, m, "unit"), n)
		out = append(out, item)
	}
	for _, m := range absoluteDateRE.FindAllStringIndex(text, -1) {
		item := base("absolute", m[0], m[1])
		normalized := strings.Join(strings.Fields(text[m[0]:m[1]]), " ")
		for _, layout := range []string{"January 2, 2006", "Jan 2, 2006"} {
			if date, err := time.Parse(layout, normalized); err == nil {
				item["resolved_date"] = date.Format("2006-01-02T15:04:05")
				break
			}
		}
		out = append(out, item)
	}
	return out
}

var definedTermRE = regexp.MustCompile(`(?:"(?P<a>[A-Z][A-Za-z0-9 /\-]{1,80})"\s+(?:means|shall\s+mean|refers\s+to|is\s+defined\s+as)\s+(?P<def_a>[^.]{5,500}\.)|\(\s*(?:the\s+)?"(?P<b>[A-Z][A-Za-z0-9 /\-]{1,80})"\s*\))`)
var likelyTermRE = regexp.MustCompile(`\b([A-Z][a-z]+(?:\s+[A-Z][a-z]+){0,2})\b`)
var termStops = []string{"Agreement", "Section", "Article", "Schedule", "Exhibit", "Appendix", "Annex", "Effective Date", "Party", "Parties", "Recipient", "Discloser", "Provider", "Customer", "Licensor", "Licensee", "Company", "Employee", "Confidential Information", "Terms", "Term", "January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}

func checkDefinedTerms(text string) []M {
	out := []M{}
	defined := map[string]bool{}
	for _, m := range definedTermRE.FindAllStringSubmatchIndex(text, -1) {
		term := strings.TrimSpace(fallback(matchGroup(definedTermRE, text, m, "a"), matchGroup(definedTermRE, text, m, "b")))
		if term == "" || defined[term] {
			continue
		}
		defined[term] = true
		body := strings.TrimSpace(matchGroup(definedTermRE, text, m, "def_a"))
		count := len(literalWordMatches(text, term, true))
		out = append(out, mergeMap(M{"term": term, "definition_text": nullable(body), "defined": true, "usage_count": count, "used_but_undefined": false, "defined_but_unused": count <= 1, "circular_reference": body != "" && len(literalWordMatches(body, term, true)) > 0}, spanFields(text, m[0], m[1])))
	}
	counts := map[string]int{}
	ordered := []string{}
	for _, match := range likelyTermRE.FindAllString(text, -1) {
		if counts[match] == 0 {
			ordered = append(ordered, match)
		}
		counts[match]++
	}
	for _, word := range ordered {
		count := counts[word]
		if count < 3 || slices.Contains(termStops, word) || defined[word] || (!strings.Contains(word, " ") && utf8.RuneCountInString(word) < 6) {
			continue
		}
		out = append(out, M{"term": word, "definition_text": nil, "defined": false, "usage_count": count, "used_but_undefined": true, "defined_but_unused": false, "circular_reference": false, "span_start": nil, "span_end": nil})
	}
	return out
}
