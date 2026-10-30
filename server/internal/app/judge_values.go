package app

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var judgeYearRE = regexp.MustCompile(`\b(1[7-9]\d{2}|20\d{2})\b`)
var judgeDateRE = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

func judgeNumber(m M, key string) float64 {
	switch value := m[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case int64:
		return float64(value)
	}
	return 0
}

func judgeWords(op M) float64 {
	if _, present := op["authored_word_count"]; present {
		return judgeNumber(op, "authored_word_count")
	}
	return judgeNumber(op, "word_count")
}

func judgeRound(n float64, digits int) float64 {
	p := math.Pow10(digits)
	return math.RoundToEven(n*p) / p
}
func judgeAverage(total float64, n int) float64 {
	if n == 0 {
		return 0
	}
	return total / float64(n)
}
func judgePick(m M, keys ...string) M {
	out := M{}
	for _, k := range keys {
		out[k] = m[k]
	}
	return out
}
func judgeString(v any) any {
	if a, ok := v.([]any); ok {
		if len(a) == 0 {
			return nil
		}
		parts := make([]string, 0, len(a))
		for _, item := range a {
			parts = append(parts, workflowValueText(item))
		}
		return strings.Join(parts, ", ")
	}
	return v
}
func judgeName(m M) string {
	parts := []string{}
	for _, k := range []string{"name_first", "name_middle", "name_last", "name_suffix"} {
		if v := strings.TrimSpace(str(m, k)); v != "" {
			parts = append(parts, v)
		}
	}
	return fallback(strings.Join(parts, " "), fallback(str(m, "name_full"), fallback(str(m, "name"), "Unknown")))
}
func judgeSummary(r M) M {
	name := judgeName(r)
	link := ""
	if id := courtID(r["id"]); id != "" {
		link = "https://www.courtlistener.com/person/" + id + "/"
	}
	return M{"id": r["id"], "name": name, "name_full": fallback(str(r, "name_full"), name), "name_first": str(r, "name_first"), "name_last": str(r, "name_last"), "court": r["court"], "position": r["position_type"], "appointed_by": r["appointer"], "date_start": r["date_start"], "dob": r["date_dob"], "political_affiliation": r["political_affiliation"], "url": link}
}
func judgeSortMatches(rows []M, query string) {
	words := strings.Fields(strings.ToLower(query))
	first, last := "", strings.ToLower(query)
	if len(words) > 0 {
		last = words[len(words)-1]
	}
	if len(words) > 1 {
		first = words[0]
	}
	rank := func(m M) int {
		f, l := strings.ToLower(str(m, "name_first")), strings.ToLower(str(m, "name_last"))
		if l == last && (first == "" || f == first) {
			return 0
		}
		if l == last && strings.HasPrefix(f, first) {
			return 1
		}
		if strings.HasPrefix(l, last) && strings.HasPrefix(f, first) {
			return 2
		}
		if strings.Contains(l, last) {
			return 3
		}
		if strings.HasPrefix(f, last) {
			return 4
		}
		return 5
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if a, b := rank(rows[i]), rank(rows[j]); a != b {
			return a < b
		}
		return strings.ToLower(str(rows[i], "name")) < strings.ToLower(str(rows[j], "name"))
	})
}
func judgeBiography(id string, person, wiki M, now time.Time) M {
	n, _ := strconv.ParseInt(id, 10, 64)
	out := M{"id": n, "name": judgeName(person), "courtlistener_url": "https://www.courtlistener.com/person/" + id + "/", "data_pulled_at": now.UTC().Format(time.RFC3339Nano)}
	for dest, src := range map[string]string{"name_first": "name_first", "name_middle": "name_middle", "name_last": "name_last", "name_suffix": "name_suffix", "date_of_birth": "date_dob", "place_of_birth_city": "dob_city", "place_of_birth_state": "dob_state", "date_of_death": "date_dod", "gender": "gender", "race": "race", "religion": "religion", "political_affiliation": "political_affiliation", "aba_rating": "aba_rating"} {
		out[dest] = judgeString(person[src])
	}
	for _, key := range []string{"url", "summary", "bio", "early_life", "education", "career", "judicial_service", "notable_cases", "personal_life", "raw"} {
		out["wikipedia_"+key] = wiki[key]
	}
	return out
}

// Keep insertion order for tied counts, as in the original Python dictionaries.
func judgeCounts(rows []M, field, label string, alphabetical bool) []M {
	out := []M{}
	indices := map[string]int{}
	for _, r := range rows {
		key := str(r, field)
		if key == "" {
			continue
		}
		if i, ok := indices[key]; ok {
			out[i]["count"] = int(judgeNumber(out[i], "count")) + 1
		} else {
			indices[key] = len(out)
			out = append(out, M{label: key, "count": 1})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if alphabetical {
			return str(out[i], label) < str(out[j], label)
		}
		return judgeNumber(out[i], "count") > judgeNumber(out[j], "count")
	})
	return out
}
func judgeSorted(rows []M, key string, numeric bool) []M {
	out := append([]M{}, rows...)
	sort.SliceStable(out, func(i, j int) bool {
		if numeric {
			return judgeNumber(out[i], key) > judgeNumber(out[j], key)
		}
		return str(out[i], key) > str(out[j], key)
	})
	return out
}
func judgeDateRange(rows []M) (any, any) {
	lo, hi := "", ""
	for _, r := range rows {
		v := str(r, "date_filed")
		if v != "" && (lo == "" || v < lo) {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return nullable(lo), nullable(hi)
}
func judgeProfile(data, records M) M {
	profile := judgePick(data)
	for k, v := range data {
		if k != "wikipedia_raw" && k != "_computed_metrics" {
			profile[k] = v
		}
	}
	ops, dockets := maps(records["opinions"]), maps(records["dockets"])
	cites, words := 0.0, 0.0
	years := []M{}
	for _, op := range ops {
		cites += judgeNumber(op, "citation_count")
		words += judgeWords(op)
		if y := judgeYearRE.FindString(str(op, "date_filed")); y != "" {
			years = append(years, M{"year": y})
		}
	}
	lo, hi := judgeDateRange(ops)
	profile["opinion_stats"] = M{"total_opinions": len(ops), "total_citations": cites, "avg_citations": judgeRound(judgeAverage(cites, len(ops)), 2), "first_opinion": lo, "last_opinion": hi, "total_words": words}
	profile["opinions_by_court"] = judgeCounts(ops, "court", "court", false)
	profile["opinions_by_year"] = judgeCounts(years, "year", "year", true)
	top := []M{}
	for _, op := range judgeSorted(ops, "citation_count", true)[:min(20, len(ops))] {
		row := judgePick(op, "id", "case_name", "citation", "date_filed", "court", "url")
		row["citation_count"] = judgeNumber(op, "citation_count")
		top = append(top, row)
	}
	profile["most_cited_opinions"] = top
	lo, hi = judgeDateRange(dockets)
	profile["docket_stats"] = M{"total_dockets": len(dockets), "first_docket": lo, "last_docket": hi}
	recent := []M{}
	for _, d := range judgeSorted(dockets, "date_filed", false)[:min(20, len(dockets))] {
		recent = append(recent, judgePick(d, "id", "case_name", "court", "date_filed", "docket_number", "nature_of_suit", "url"))
	}
	profile["recent_dockets"] = recent
	types := judgeCounts(dockets, "nature_of_suit", "nature_of_suit", false)
	profile["dockets_by_type"] = types[:min(10, len(types))]
	return profile
}

type judgeOpinionFilter struct {
	Query, Court, Order                     string
	Start, End, MinCitations, Limit, Offset int
}

func judgeFilterOpinions(ops []M, f judgeOpinionFilter) M {
	rows := []M{}
	for _, op := range ops {
		if q := strings.ToLower(f.Query); q != "" && !strings.Contains(strings.ToLower(str(op, "case_name")), q) && !strings.Contains(strings.ToLower(str(op, "full_text")), q) {
			continue
		}
		if f.Court != "" && !strings.Contains(strings.ToLower(str(op, "court")), strings.ToLower(f.Court)) {
			continue
		}
		y := charSlice(str(op, "date_filed"), 0, 4)
		if f.Start != 0 && (y == "" || y < strconv.Itoa(f.Start)) {
			continue
		}
		if f.End != 0 && (y == "" || y > strconv.Itoa(f.End)) {
			continue
		}
		if judgeNumber(op, "citation_count") < float64(f.MinCitations) {
			continue
		}
		rows = append(rows, op)
	}
	if f.Order == "citations" {
		rows = judgeSorted(rows, "citation_count", true)
	} else {
		rows = judgeSorted(rows, "date_filed", false)
	}
	page := []M{}
	for _, op := range rows[min(f.Offset, len(rows)):min(f.Offset+f.Limit, len(rows))] {
		r := judgePick(op, "id", "case_name", "case_name_short", "court", "date_filed", "docket_number", "citation", "citation_count", "url")
		r["snippet"] = charSlice(fallback(str(op, "full_text"), str(op, "snippet")), 0, 500)
		page = append(page, r)
	}
	return M{"total": len(rows), "limit": f.Limit, "offset": f.Offset, "opinions": page}
}
func judgeStats(ops []M) M {
	cites, words, maxCites := 0.0, 0.0, 0.0
	years := map[string][]M{}
	yearKeys := []string{}
	courts := map[string][]M{}
	courtKeys := []string{}
	days := make([]int, 7)
	months := map[string]int{}
	ranges := make([]int, 6)
	for _, op := range ops {
		c, w := judgeNumber(op, "citation_count"), judgeWords(op)
		cites += c
		words += w
		maxCites = max(maxCites, c)
		if y := judgeYearRE.FindString(str(op, "date_filed")); y != "" {
			if _, ok := years[y]; !ok {
				yearKeys = append(yearKeys, y)
			}
			years[y] = append(years[y], op)
		}
		if c := str(op, "court"); c != "" {
			if _, ok := courts[c]; !ok {
				courtKeys = append(courtKeys, c)
			}
			courts[c] = append(courts[c], op)
		}
		date := judgeDateRE.FindString(str(op, "date_filed"))
		if parsed, e := time.Parse("2006-01-02", date); e == nil {
			days[(int(parsed.Weekday())+6)%7]++
			months[date[5:7]]++
		}
		switch {
		case c == 0:
			ranges[0]++
		case c <= 5:
			ranges[1]++
		case c <= 20:
			ranges[2]++
		case c <= 50:
			ranges[3]++
		case c <= 100:
			ranges[4]++
		default:
			ranges[5]++
		}
	}
	out := M{"overview": M{"total_opinions": len(ops), "total_citations": cites, "avg_citations_per_opinion": judgeRound(judgeAverage(cites, len(ops)), 2), "max_citations": maxCites, "avg_opinion_length": judgeRound(judgeAverage(words, len(ops)), 1), "total_words_written": words}}
	sort.Strings(yearKeys)
	byYear := []M{}
	for _, y := range yearKeys {
		c, w, n := 0.0, 0.0, 0
		for _, op := range years[y] {
			c += judgeNumber(op, "citation_count")
			if v := judgeWords(op); v > 0 {
				w += v
				n++
			}
		}
		byYear = append(byYear, M{"year": y, "opinions": len(years[y]), "citations": c, "avg_length": judgeAverage(w, n)})
	}
	out["by_year"] = byYear
	byCourt := []M{}
	for _, court := range courtKeys {
		c := 0.0
		for _, op := range courts[court] {
			c += judgeNumber(op, "citation_count")
		}
		byCourt = append(byCourt, M{"court": court, "opinions": len(courts[court]), "citations": c, "avg_citations": judgeRound(judgeAverage(c, len(courts[court])), 2)})
	}
	out["by_court"] = judgeSorted(byCourt, "opinions", true)
	byDay, byMonth, dist := []M{}, []M{}, []M{}
	for i, day := range []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"} {
		byDay = append(byDay, M{"day_of_week": day, "opinions": days[i]})
	}
	monthKeys := []string{}
	for month := range months {
		monthKeys = append(monthKeys, month)
	}
	sort.Strings(monthKeys)
	for _, month := range monthKeys {
		byMonth = append(byMonth, M{"month": month, "opinions": months[month]})
	}
	for i, label := range []string{"0", "1-5", "6-20", "21-50", "51-100", "100+"} {
		dist = append(dist, M{"citation_range": label, "count": ranges[i]})
	}
	out["by_day_of_week"], out["by_month"], out["citation_distribution"] = byDay, byMonth, dist
	return out
}
