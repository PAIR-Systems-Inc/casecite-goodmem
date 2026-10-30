package app

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed judge_methodology.json
var judgeMethodologyJSON []byte

func judgeQuality(n, good, limited int) string {
	if n >= good {
		return "good"
	}
	if n >= limited {
		return "limited"
	}
	return "insufficient"
}
func judgeDissentMetrics(ops []M) (M, M) {
	total, dissent, concur := 0, 0, 0
	for _, op := range ops {
		types := []string{str(op, "opinion_type")}
		// A v4 cluster contains opinions by several judges. Only attributed
		// authorship belongs in this judge's separate-writing statistics.
		if _, present := op["authored_opinion_types"]; present {
			types = stringsOf(op["authored_opinion_types"])
		}
		for _, ot := range types {
			t := strings.ToLower(strings.TrimSpace(ot))
			if t == "" {
				continue
			}
			total++
			if strings.Contains(t, "dissent") {
				dissent++
			}
			if strings.Contains(t, "concur") {
				concur++
			}
		}
	}
	metric := func(count int, key string) M {
		var value any
		if total > 0 {
			value = judgeRound(float64(count)*100/float64(total), 2)
		}
		return M{"value": value, key: count, "total_opinions_analyzed": total, "data_quality": judgeQuality(total, 50, 10)}
	}
	return metric(dissent, "dissent_count"), metric(concur, "concurrence_count")
}
func judgeCitationMetrics(ops []M, year int) M {
	total, highest := 0.0, 0.0
	for _, op := range ops {
		v := judgeNumber(op, "citation_count")
		total += v
		highest = max(highest, v)
	}
	first, _ := judgeDateRange(ops)
	years := 1
	if d, ok := first.(string); ok {
		if y, e := strconv.Atoi(judgeYearRE.FindString(d)); e == nil {
			years = max(1, year-y)
		}
	}
	avg := judgeAverage(total, len(ops))
	quality := "limited"
	if len(ops) >= 50 {
		quality = "good"
	} else if len(ops) == 0 {
		quality = "insufficient"
	}
	return M{"value": judgeRound(min(5, avg*math.Log(float64(years+1))/35), 2), "total_citations": total, "avg_citations_per_opinion": judgeRound(avg, 2), "most_cited_opinion_citations": highest, "years_on_bench": years, "data_quality": quality}
}
func judgeDurations(dockets []M) []int {
	durations := []int{}
	for _, d := range dockets {
		start, e1 := time.Parse("2006-01-02", judgeDateRE.FindString(str(d, "date_filed")))
		end, e2 := time.Parse("2006-01-02", judgeDateRE.FindString(str(d, "date_terminated")))
		if e1 == nil && e2 == nil {
			days := int(end.Sub(start).Hours() / 24)
			if days > 0 && days < 3650 {
				durations = append(durations, days)
			}
		}
	}
	return durations
}
func judgeDurationMetrics(dockets []M) M {
	d := judgeDurations(dockets)
	sort.Ints(d)
	out := M{"value_days": nil, "value_months": nil, "cases_analyzed": len(d), "median_days": nil, "fastest_case_days": nil, "slowest_case_days": nil, "data_quality": judgeQuality(len(d), 30, 10)}
	if len(d) > 0 {
		total := 0
		for _, v := range d {
			total += v
		}
		avg := float64(total) / float64(len(d))
		median := float64(d[len(d)/2])
		if len(d)%2 == 0 {
			median = (float64(d[len(d)/2-1]) + median) / 2
		}
		out["value_days"], out["value_months"], out["median_days"], out["fastest_case_days"], out["slowest_case_days"] = judgeRound(avg, 1), judgeRound(avg/30.44, 1), median, d[0], d[len(d)-1]
	}
	return out
}
func judgeCaseTypeMetrics(dockets []M) M {
	types := judgeCounts(dockets, "nature_of_suit", "type", false)
	total := 0.0
	for _, r := range types {
		total += judgeNumber(r, "count")
	}
	for _, r := range types {
		r["percentage"] = judgeRound(judgeNumber(r, "count")*100/total, 1)
	}
	var primary any
	concentration := 0.0
	if len(types) > 0 {
		primary = types[0]["type"]
		concentration = judgeNumber(types[0], "percentage")
	}
	return M{"distribution": types[:min(15, len(types))], "total_cases_analyzed": total, "primary_expertise": primary, "expertise_concentration": concentration, "data_quality": judgeQuality(int(total), 50, 10)}
}

var judgeSentenceRE = regexp.MustCompile(`[.!?]+`)
var judgeWordRE = regexp.MustCompile(`\b[a-zA-Z]+\b`)

func judgeWritingMetrics(ops []M) M {
	lengths, sentences, diversities := []float64{}, []float64{}, []float64{}
	for _, op := range ops[:min(100, len(ops))] {
		text := str(op, "full_text")
		if _, present := op["authored_text"]; present {
			text = str(op, "authored_text")
		}
		if text == "" {
			continue
		}
		wc := judgeWords(op)
		if wc == 0 {
			wc = float64(len(strings.Fields(text)))
		}
		lengths = append(lengths, wc)
		sum, n := 0, 0
		for _, sentence := range judgeSentenceRE.Split(text, -1) {
			if len([]rune(strings.TrimSpace(sentence))) > 10 {
				sum += len(strings.Fields(sentence))
				n++
			}
		}
		if n > 0 {
			sentences = append(sentences, float64(sum)/float64(n))
		}
		words := judgeWordRE.FindAllString(strings.ToLower(text), -1)
		if len(words) > 100 {
			unique := map[string]bool{}
			for _, w := range words {
				unique[w] = true
			}
			diversities = append(diversities, float64(len(unique))/float64(len(words)))
		}
	}
	mean := func(values []float64) float64 {
		sum := 0.0
		for _, v := range values {
			sum += v
		}
		return judgeAverage(sum, len(values))
	}
	optional := func(values []float64, precision int) any {
		if len(values) == 0 {
			return nil
		}
		return judgeRound(mean(values), precision)
	}
	var score any
	if len(sentences) > 0 && len(diversities) > 0 {
		score = judgeRound(min(3, mean(sentences)/10)+min(3, mean(diversities)*10)+min(4, mean(lengths)/2500), 1)
	}
	return M{"score": score, "avg_opinion_length_words": optional(lengths, 0), "avg_sentence_length_words": optional(sentences, 1), "vocabulary_diversity": optional(diversities, 3), "opinions_analyzed": len(lengths), "data_quality": judgeQuality(len(lengths), 20, 5)}
}
func judgeLengthTrend(ops []M) M {
	years := map[string][]float64{}
	keys := []string{}
	for _, op := range ops {
		if wc := judgeWords(op); wc > 0 {
			if y := judgeYearRE.FindString(str(op, "date_filed")); y != "" {
				if _, ok := years[y]; !ok {
					keys = append(keys, y)
				}
				years[y] = append(years[y], wc)
			}
		}
	}
	sort.Strings(keys)
	rows := []M{}
	x, y, xy, xx := 0.0, 0.0, 0.0, 0.0
	for _, key := range keys {
		total := 0.0
		for _, v := range years[key] {
			total += v
		}
		mean := judgeRound(judgeAverage(total, len(years[key])), 0)
		year, _ := strconv.Atoi(key)
		xf := float64(year)
		x += xf
		y += mean
		xy += xf * mean
		xx += xf * xf
		rows = append(rows, M{"year": key, "avg_length": mean, "opinion_count": len(years[key])})
	}
	direction := "stable"
	n := float64(len(rows))
	if n >= 3 && n*xx != x*x {
		slope := (n*xy - x*y) / (n*xx - x*x)
		if slope > 50 {
			direction = "increasing"
		} else if slope < -50 {
			direction = "decreasing"
		}
	}
	return M{"trend_direction": direction, "yearly_data": rows[max(0, len(rows)-10):], "data_quality": judgeQuality(len(rows), 5, 2)}
}

var judgeAnyYearRE = regexp.MustCompile(`\d{4}`)

func judgeBenchMetrics(positions []M, year int) M {
	rows := []M{}
	intervals := [][2]int{}
	var current any
	currentStart := -1
	for _, pos := range positions {
		kind := strings.ToLower(str(pos, "position_type"))
		if strings.Contains(kind, "attorney") || strings.Contains(kind, "clerk") || strings.Contains(kind, "counsel") || strings.Contains(kind, "prosecutor") {
			continue
		}
		// CourtListener position histories include attorneys and other jobs.
		if str(pos, "court_name") == "" && str(pos, "court_id") == "" && !strings.Contains(kind, "judge") && !strings.Contains(kind, "justice") && !strings.Contains(kind, "magistrate") {
			continue
		}
		row := M{"position": pos["position_type"], "court": pos["court_name"], "start": pos["date_start"], "end": pos["date_termination"]}
		start, e := strconv.Atoi(judgeAnyYearRE.FindString(str(pos, "date_start")))
		end := year
		if y, err := strconv.Atoi(judgeAnyYearRE.FindString(str(pos, "date_termination"))); err == nil {
			end = min(year, y)
		}
		if e == nil && start <= end {
			row["years"] = end - start
			intervals = append(intervals, [2]int{start, end})
		}
		if str(pos, "date_termination") == "" && e == nil && start <= year && start > currentStart {
			current = row
			currentStart = start
		}
		rows = append(rows, row)
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i][0] < intervals[j][0] })
	total, end := 0, -1
	for _, span := range intervals {
		total += max(0, span[1]-max(end, span[0]))
		end = max(end, span[1])
	}
	quality := "insufficient"
	if len(intervals) > 0 {
		quality = "good"
	}
	return M{"total_years": total, "positions": rows, "current_position": current, "data_quality": quality}
}
func judgeCompleteness(data M, ops, dockets []M) M {
	hasText := false
	for _, op := range ops {
		if str(op, "full_text") != "" {
			hasText = true
			break
		}
	}
	types := judgeCaseTypeMetrics(dockets)
	checks := M{"biographical_info": str(data, "name") != "" || str(data, "name_first") != "", "birth_date": str(data, "date_of_birth") != "", "education": len(maps(data["education"])) > 0, "positions": len(maps(data["positions"])) > 0, "opinions": len(ops) > 0, "dockets": judgeNumber(types, "total_cases_analyzed") > 0, "opinion_full_text": hasText}
	count := 0
	for _, v := range checks {
		if v == true {
			count++
		}
	}
	score := float64(count) * 100 / float64(len(checks))
	wording := "Low confidence - significant data gaps"
	if score >= 70 {
		wording = "High confidence in metrics"
	} else if score >= 40 {
		wording = "Moderate confidence - some data missing"
	}
	return M{"score": judgeRound(score, 1), "checks": checks, "recommendation": wording}
}
func judgeMetrics(id string, data, records M, now time.Time) M {
	ops, dockets := maps(records["opinions"]), maps(records["dockets"])
	dissent, concur := judgeDissentMetrics(ops)
	metrics := M{"dissent_rate": dissent, "concurrence_rate": concur, "citation_impact_score": judgeCitationMetrics(ops, now.Year()), "case_disposition_time": judgeDurationMetrics(dockets), "case_type_expertise": judgeCaseTypeMetrics(dockets), "writing_complexity": judgeWritingMetrics(ops), "opinion_length_trend": judgeLengthTrend(ops), "bench_experience": judgeBenchMetrics(maps(data["positions"]), now.Year()), "data_completeness": judgeCompleteness(data, ops, dockets)}
	methodology := M{}
	_ = json.Unmarshal(judgeMethodologyJSON, &methodology)
	delete(methodology, "political_alignment_indicator") // The original describes a metric it never computes.
	object(methodology["bench_experience"])["calculation"] = "Union of non-overlapping calendar-year spans in judicial positions, capped at the current year. Current position is the latest active judicial appointment. Prior non-judicial jobs are excluded."
	for _, key := range []string{"dissent_rate", "concurrence_rate"} {
		object(methodology[key])["limitations"] = "Only opinions explicitly attributed to this judge are counted when author IDs are available. Panel membership and a case's other authors do not establish authorship. Unlinked historical records and missing opinion types limit coverage."
	}
	strengths, cautions := []string{}, []string{}
	impact := object(metrics["citation_impact_score"])
	v := judgeNumber(impact, "value")
	if v > 2 {
		strengths = append(strengths, fmt.Sprintf("Citation impact score %.2f on the fixed 0–5 scale; this is not a peer ranking.", v))
	} else if v > 0 && v < 0.5 {
		cautions = append(cautions, "Lower citation impact may indicate newer opinions or a specialized practice area.")
	}
	if judgeNumber(dissent, "value") > 20 {
		cautions = append(cautions, fmt.Sprintf("Dissents account for %.2f%% of attributed, classified opinions. No peer average is computed.", judgeNumber(dissent, "value")))
	} else if str(dissent, "data_quality") == "insufficient" {
		cautions = append(cautions, "Insufficient attributed opinion data for reliable dissent-rate analysis.")
	}
	duration := object(metrics["case_disposition_time"])
	if months := judgeNumber(duration, "value_months"); months > 0 {
		cautions = append(cautions, fmt.Sprintf("Retrieved terminated cases averaged %.1f months. Case mix and settlement timing affect duration; this does not measure judicial efficiency.", months))
	}
	complete := object(metrics["data_completeness"])
	complete["recommendation"] = "Read each metric's sample size and quality rating; data-category coverage alone does not establish statistical confidence."
	quality := "good"
	if judgeNumber(complete, "score") < 40 {
		quality = "insufficient"
		cautions = append(cautions, "Limited data available - interpret all metrics with caution")
	} else if judgeNumber(complete, "score") < 70 {
		quality = "limited"
		cautions = append(cautions, "Some data gaps exist - some metrics may be less reliable")
	}
	// Seven populated fields do not turn a one-case sample into reliable
	// statistics. Overall quality also reflects the empirical sample sizes.
	good, sufficient := 0, 0
	for _, key := range []string{"dissent_rate", "concurrence_rate", "citation_impact_score", "case_disposition_time", "case_type_expertise", "writing_complexity", "opinion_length_trend"} {
		q := str(object(metrics[key]), "data_quality")
		if q == "good" {
			good++
		}
		if q != "insufficient" {
			sufficient++
		}
	}
	if sufficient < 3 {
		quality = "insufficient"
		cautions = append(cautions, "Too few observations support most metrics; overall statistical quality is insufficient.")
	} else if quality == "good" && good < 4 {
		quality = "limited"
	}
	if primary := str(object(metrics["case_type_expertise"]), "primary_expertise"); primary != "" {
		strengths = append(strengths, "Most frequently retrieved case type: "+primary)
	}
	n, _ := strconv.ParseInt(id, 10, 64)
	return M{"judge_id": n, "judge_name": fallback(str(data, "name"), "Unknown"), "metrics": metrics, "methodology": methodology, "computed_at": now.UTC().Format(time.RFC3339Nano), "summary": M{"strengths": strengths, "cautions": cautions, "data_quality_overall": quality, "recommendation": complete["recommendation"]}}
}
