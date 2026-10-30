package app

import (
	"crypto/sha256"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type chatQueryToken struct {
	Value    string  `json:"value"`
	Operator string  `json:"operator"`
	Field    *string `json:"field"`
	IsPhrase bool    `json:"is_phrase"`
	Negated  bool    `json:"negated"`
}
type chatSearchQuery struct {
	Original      string            `json:"original"`
	SemanticText  string            `json:"semantic_text"`
	Tokens        []chatQueryToken  `json:"tokens"`
	Phrases       []string          `json:"phrases"`
	Required      []string          `json:"required_terms"`
	Optional      []string          `json:"optional_terms"`
	Excluded      []string          `json:"excluded_terms"`
	Fields        map[string]string `json:"field_queries"`
	DateFrom      *string           `json:"date_from"`
	DateTo        *string           `json:"date_to"`
	Courts        []string          `json:"courts"`
	DocTypes      []string          `json:"doc_types"`
	Jurisdictions []string          `json:"jurisdictions"`
}

var chatQueryFieldRE = regexp.MustCompile(`([\p{L}\p{N}_]+):("[^"]+"|[^\s":/][^\s"]*)`)
var chatPhraseRE = regexp.MustCompile(`"([^"]+)"`)

func chatQueryDate(value string) *string {
	for _, layout := range []string{"2006-1-2", "2006/1/2", "1/2/2006", "2006"} {
		if date, err := time.Parse(layout, value); err == nil {
			out := date.Format("2006-01-02")
			return &out
		}
	}
	return nil
}
func (q chatSearchQuery) Semantic() string { return fallback(q.SemanticText, q.Original) }
func chatOperatorsEnabled(query string) bool {
	for _, r := range query {
		if unicode.IsLower(r) {
			return true
		}
	}
	return false
}
func chatExclusion(word string) (string, bool) {
	if strings.HasPrefix(word, "-") {
		r, _ := utf8.DecodeRuneInString(word[1:])
		if unicode.IsLetter(r) {
			return word[1:], true
		}
	}
	return "", false
}

func parseChatSearch(query string) chatSearchQuery {
	p := chatSearchQuery{Original: query, Tokens: []chatQueryToken{}, Phrases: []string{}, Required: []string{}, Optional: []string{}, Excluded: []string{}, Fields: map[string]string{}, Courts: []string{}, DocTypes: []string{}, Jurisdictions: []string{}}
	// Normalize Unicode whitespace for RE2's ASCII \s; no original text is lost
	// in citations or persisted questions.
	query = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, query)
	var b strings.Builder
	start := 0
	for _, match := range chatQueryFieldRE.FindAllStringSubmatchIndex(query, -1) {
		before, _ := utf8.DecodeLastRuneInString(query[:match[0]])
		if chatWord(before) || before == ':' || before == '/' {
			continue
		}
		name := strings.ToLower(query[match[2]:match[3]])
		mapped, known := chatAssets.FieldMappings[name]
		if !known && !slices.Contains([]string{"jurisdiction", "after", "from", "date_from", "before", "to", "date_to"}, name) {
			continue
		}
		value := strings.Trim(query[match[4]:match[5]], `"`)
		if known {
			p.Fields[mapped] = value
		}
		switch name {
		case "court":
			p.Courts = append(p.Courts, value)
		case "type":
			p.DocTypes = append(p.DocTypes, value)
		case "jurisdiction":
			p.Jurisdictions = append(p.Jurisdictions, value)
		case "after", "from", "date_from":
			p.DateFrom = chatQueryDate(value)
		case "before", "to", "date_to":
			p.DateTo = chatQueryDate(value)
		}
		b.WriteString(query[start:match[0]])
		b.WriteByte(' ')
		start = match[1]
	}
	b.WriteString(query[start:])
	query = b.String()
	for _, match := range chatPhraseRE.FindAllStringSubmatch(query, -1) {
		p.Phrases = append(p.Phrases, match[1])
	}
	tokenText := chatPhraseRE.ReplaceAllString(query, " ")
	operators, operator, negated := chatOperatorsEnabled(tokenText), "AND", false
	for _, word := range strings.Fields(tokenText) {
		if operators {
			switch word {
			case "AND":
				operator = "AND"
				continue
			case "OR":
				operator = "OR"
				if n := len(p.Tokens); n > 0 && !p.Tokens[n-1].Negated {
					p.Tokens[n-1].Operator = "OR"
				}
				continue
			case "NOT":
				negated = true
				continue
			}
		}
		if excluded, ok := chatExclusion(word); ok {
			word, negated = excluded, true
		}
		value := strings.Trim(word, ".,;:!?()[]")
		valid := false
		for _, r := range value {
			valid = valid || unicode.IsLetter(r) || unicode.IsNumber(r)
		}
		if !valid {
			negated = false
			continue
		}
		p.Tokens = append(p.Tokens, chatQueryToken{Value: value, Operator: operator, Negated: negated})
		operator, negated = "AND", false
	}
	for _, token := range p.Tokens {
		if slices.Contains(chatAssets.QueryStopwords, strings.ToLower(token.Value)) {
			continue
		}
		if token.Negated {
			p.Excluded = append(p.Excluded, token.Value)
		} else if token.Operator == "OR" {
			p.Optional = append(p.Optional, token.Value)
		} else {
			p.Required = append(p.Required, token.Value)
		}
	}
	kept := []string{}
	skip := false
	operators = chatOperatorsEnabled(query)
	for _, word := range strings.Fields(strings.ReplaceAll(query, `"`, " ")) {
		if skip {
			skip = false
			continue
		}
		if operators && (word == "AND" || word == "OR") {
			continue
		}
		if operators && word == "NOT" {
			skip = true
			continue
		}
		if _, ok := chatExclusion(word); ok {
			continue
		}
		kept = append(kept, word)
	}
	p.SemanticText = strings.Join(kept, " ")
	return p
}

func chatBM25Terms(text string) []string {
	text = strings.Map(func(r rune) rune {
		if chatWord(r) || unicode.IsSpace(r) {
			return r
		}
		return ' '
	}, strings.ToLower(text))
	out := []string{}
	for _, word := range strings.Fields(text) {
		if utf8.RuneCountInString(word) > 2 {
			out = append(out, word)
		}
	}
	return out
}

// Request-local keyword scoring: no extra service, index, model or mutable
// process-wide state. GoodMem supplies the vector candidate pool.
func chatBM25(documents []M, query string) []M {
	frequencies := make([]map[string]int, len(documents))
	lengths := make([]int, len(documents))
	df := map[string]int{}
	total := 0
	for i, doc := range documents {
		terms := chatBM25Terms(str(doc, "text"))
		lengths[i] = len(terms)
		total += len(terms)
		frequencies[i] = map[string]int{}
		for _, term := range terms {
			frequencies[i][term]++
		}
		for term := range frequencies[i] {
			df[term]++
		}
	}
	out := []M{}
	if total == 0 {
		return out
	}
	average := float64(total) / float64(len(documents))
	queryTerms := chatBM25Terms(query)
	for i, doc := range documents {
		score := 0.0
		for _, term := range queryTerms {
			count := float64(frequencies[i][term])
			if count == 0 {
				continue
			}
			idf := math.Log((float64(len(documents)-df[term])+.5)/(float64(df[term])+.5) + 1)
			score += idf * count * 2.5 / (count + 1.5*(.25+.75*float64(lengths[i])/average))
		}
		if score > 0 {
			out = append(out, M{"id": doc["id"], "score": score})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return judgeNumber(out[i], "score") > judgeNumber(out[j], "score") })
	return out
}

func chatHybridResults(results []M, query string) []M {
	keyword := map[string]float64{}
	maximum := 0.0
	for _, r := range chatBM25(results, query) {
		score := judgeNumber(r, "score")
		keyword[str(r, "id")] = score
		maximum = max(maximum, score)
	}
	out := make([]M, 0, len(results))
	for _, r := range results {
		copy := copyWorkflowMap(r)
		score := 0.0
		if maximum > 0 {
			score = keyword[str(r, "id")] / maximum
		}
		copy["keyword_score"] = score
		copy["hybrid_score"] = .7*judgeNumber(r, "similarity") + .3*score
		out = append(out, copy)
	}
	sort.SliceStable(out, func(i, j int) bool { return judgeNumber(out[i], "hybrid_score") > judgeNumber(out[j], "hybrid_score") })
	return out
}

func chatResultKey(result M) string {
	meta := object(result["metadata"])
	if meta["document_id"] != nil && meta["chunk_index"] != nil {
		return fmt.Sprintf("dc:%s:%d", str(meta, "document_id"), number(meta, "chunk_index", 0))
	}
	if str(result, "id") != "" {
		return "id:" + str(result, "id")
	}
	// The original compares only 80 characters, collapsing distinct passages
	// with the same boilerplate. Hash the whole passage instead.
	return fmt.Sprintf("text:%x", sha256.Sum256([]byte(str(result, "text"))))
}

func mergeChatResults(lists [][]M, count int) []M {
	scores := map[string]float64{}
	best := map[string]M{}
	order := []string{}
	for _, list := range lists {
		for i, r := range list {
			key := chatResultKey(r)
			scores[key] += 1 / (60 + float64(i+1))
			if best[key] == nil {
				order = append(order, key)
			}
			if best[key] == nil || judgeNumber(r, "similarity") > judgeNumber(best[key], "similarity") {
				best[key] = r
			}
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return scores[order[i]] > scores[order[j]] })
	if count > 0 && len(order) > count {
		order = order[:count]
	}
	out := []M{}
	for i, key := range order {
		r := copyWorkflowMap(best[key])
		r["merge_score"], r["rank"] = scores[key], i+1
		out = append(out, r)
	}
	return out
}

var chatKeywordRE = regexp.MustCompile(`[a-z0-9]{4,}`)

func chatKeywords(text string) map[string]bool {
	out := map[string]bool{}
	for _, word := range chatKeywordRE.FindAllString(strings.ToLower(text), -1) {
		if !slices.Contains(chatAssets.KeywordStopwords, word) {
			out[word] = true
		}
	}
	return out
}
func chatOverlap(a, b map[string]bool) int {
	n := 0
	for word := range a {
		if b[word] {
			n++
		}
	}
	return n
}

func chatSentences(text string) []string {
	out := []string{}
	start := 0
	previous := rune(0)
	splitting := false
	for i, r := range text {
		if unicode.IsSpace(r) && (strings.ContainsRune(".!?", previous) || splitting) {
			if !splitting && strings.TrimSpace(text[start:i]) != "" {
				out = append(out, text[start:i])
			}
			splitting = true
			start = i + utf8.RuneLen(r)
		} else {
			splitting = false
		}
		previous = r
	}
	if strings.TrimSpace(text[start:]) != "" {
		out = append(out, text[start:])
	}
	return out
}

func compressChatResults(results []M, query string, count int) []M {
	keywords := chatKeywords(query)
	if len(keywords) == 0 {
		return results
	}
	out := []M{}
	for _, r := range results {
		sentences := chatSentences(str(r, "text"))
		if len(sentences) <= count {
			out = append(out, r)
			continue
		}
		type scored struct{ index, score int }
		kept := []scored{}
		for i, sentence := range sentences {
			score := chatOverlap(keywords, chatKeywords(sentence))
			if score >= 1 {
				kept = append(kept, scored{i, score})
			}
		}
		sort.SliceStable(kept, func(i, j int) bool { return kept[i].score > kept[j].score })
		if len(kept) > count {
			kept = kept[:count]
		}
		if len(kept) == 0 {
			for i := 0; i < min(count, len(sentences)); i++ {
				kept = append(kept, scored{i, 0})
			}
		}
		sort.Slice(kept, func(i, j int) bool { return kept[i].index < kept[j].index })
		parts := []string{}
		for _, item := range kept {
			parts = append(parts, strings.TrimSpace(sentences[item.index]))
		}
		copy := copyWorkflowMap(r)
		copy["text"], copy["compressed"] = strings.Join(parts, " "), true
		out = append(out, copy)
	}
	return out
}

func verifyChatReferences(citations []M, answer string) {
	answerLower, answerKeywords := strings.ToLower(answer), chatKeywords(answer)
	for _, citation := range citations {
		filename := strings.ToLower(strings.TrimSpace(str(citation, "source")))
		source := filename
		if i := strings.LastIndex(source, "."); i >= 0 {
			source = source[:i]
		}
		source = strings.ToLower(strings.TrimSpace(source))
		words := chatKeywords(str(citation, "passage"))
		overlap := chatOverlap(words, answerKeywords)
		ratio := 0.0
		if len(words) > 0 {
			ratio = float64(overlap) / float64(len(words))
		}
		sourceHit := chatContainsSource(answerLower, filename) || utf8.RuneCountInString(source) >= 3 && chatContainsSource(answerLower, source)
		referenced := sourceHit || overlap >= 6 && ratio >= .25 || overlap >= 3 && ratio >= .6
		citation["was_cited_by_ai"] = referenced
		note := "Not referenced in answer"
		if referenced {
			note = "Referenced in the answer"
		}
		if str(citation, "notes") != "" {
			note = str(citation, "notes") + " | " + note
		}
		citation["notes"] = note
	}
}

func chatContainsSource(text, source string) bool {
	if source == "" {
		return false
	}
	for offset := 0; offset < len(text); {
		i := strings.Index(text[offset:], source)
		if i < 0 {
			return false
		}
		i += offset
		before, _ := utf8.DecodeLastRuneInString(text[:i])
		after, _ := utf8.DecodeRuneInString(text[i+len(source):])
		if !chatWord(before) && !chatWord(after) {
			return true
		}
		offset = i + len(source)
	}
	return false
}

func chatExpandedQueries(query, raw string) []string {
	out := []string{query}
	seen := map[string]bool{strings.ToLower(strings.TrimSpace(query)): true}
	for _, line := range strings.Split(raw, "\n") {
		cleaned := strings.TrimSpace(strings.Trim(line, " \t-•*0123456789."))
		key := strings.ToLower(cleaned)
		if cleaned != "" && !seen[key] {
			seen[key] = true
			out = append(out, cleaned)
		}
		if len(out) == 3 {
			break
		}
	}
	return out
}

func chatResultMatches(result M, query chatSearchQuery) bool {
	meta := object(result["metadata"])
	date := str(meta, "date_filed")
	if date == "" {
		date = str(meta, "created_at")
	}
	if len(date) >= 10 {
		date = date[:10]
	}
	parsedDate := chatQueryDate(date)
	if query.DateFrom != nil && (parsedDate == nil || *parsedDate < *query.DateFrom) {
		return false
	}
	if query.DateTo != nil && (parsedDate == nil || *parsedDate > *query.DateTo) {
		return false
	}
	matches := func(value string, patterns []string) bool {
		for _, p := range patterns {
			if strings.Contains(strings.ToLower(value), strings.ToLower(p)) {
				return true
			}
		}
		return false
	}
	if len(query.Courts) > 0 && !matches(str(meta, "court"), query.Courts) {
		return false
	}
	if len(query.DocTypes) > 0 && !matches(str(meta, "doc_type"), query.DocTypes) {
		return false
	}
	for _, term := range query.Excluded {
		if strings.Contains(strings.ToLower(str(result, "text")), strings.ToLower(term)) {
			return false
		}
	}
	// Unlike the original filter stage, honor the other field filters which
	// the parser already advertises (filename, source, judge, citation, etc.).
	for field, value := range query.Fields {
		if field == "court" || field == "doc_type" {
			continue
		}
		actual := str(meta, field)
		if field == "year" && actual == "" && parsedDate != nil {
			actual = (*parsedDate)[:4]
		}
		if !matches(actual, []string{value}) {
			return false
		}
	}
	if len(query.Jurisdictions) > 0 && !matches(str(meta, "jurisdiction"), query.Jurisdictions) {
		return false
	}
	return true
}
