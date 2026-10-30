package app

import (
	"fmt"
	"math"
	"strconv"
	"time"
)

func legalQueryInt(q request, key string, def, low, high int) (int, error) {
	if !q.r.URL.Query().Has(key) {
		return def, nil
	}
	n, err := strconv.Atoi(q.r.URL.Query().Get(key))
	if err != nil || n < low || n > high {
		return 0, fail(422, fmt.Sprintf("%s must be between %d and %d", key, low, high))
	}
	return n, nil
}
func legalQueryRequired(q request, key string) (string, error) {
	if !q.r.URL.Query().Has(key) {
		return "", fail(422, key+" is required")
	}
	return q.r.URL.Query().Get(key), nil
}
func legalPathID(q request) (string, error) {
	id, err := strconv.ParseInt(q.r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return "", fail(422, "id must be a positive integer")
	}
	return strconv.FormatInt(id, 10), nil
}
func legalTiming(result M, elapsed time.Duration) M {
	seconds := elapsed.Seconds()
	result["_api_timing"] = M{"elapsed_seconds": math.Round(seconds*100) / 100, "is_slow": seconds > 10}
	if seconds > 10 {
		result["_api_warning"] = fmt.Sprintf("CourtListener API is responding slowly (%.1fs). This is a third-party service issue, not a problem with our platform. Please be patient or try again later.", seconds)
	}
	return result
}
func legalCitationResponse(result M) M {
	rows := []M{}
	for _, group := range []string{"negative_citations", "caution_citations", "positive_citations"} {
		cases := maps(result[group])
		result[group] = len(cases)
		for _, c := range cases {
			var cite any
			if values := stringsOf(c["citation"]); len(values) > 0 {
				cite = values[0]
			}
			rows = append(rows, M{"id": c["id"], "case_name": c["case_name"], "citation": cite, "court": c["court"], "date_filed": c["date_filed"], "treatment": c["treatment"], "snippet": c["snippet"], "context_found": c["context_found"] == true, "url": c["url"]})
		}
	}
	result["citing_cases"] = rows[:min(len(rows), 60)]
	return result
}

func (s *Server) legalToolRoutes() {
	register := func(path string, handler func(request) (M, error)) {
		s.api("GET /api/v1/tools/"+path, false, func(q request) (any, error) {
			start := time.Now()
			result, err := handler(q)
			if err != nil {
				return nil, err
			}
			return legalTiming(result, time.Since(start)), nil
		})
	}
	register("cases/search", func(q request) (M, error) {
		query, err := legalQueryRequired(q, "q")
		if err != nil {
			return nil, err
		}
		size, err := legalQueryInt(q, "page_size", 20, 1, 100)
		if err != nil {
			return nil, err
		}
		cursor := q.r.URL.Query().Get("cursor")
		if cursor != "" {
			if _, err = s.legalCursor(cursor); err != nil {
				return nil, err
			}
		}
		if err = s.requireCourtToken(q.r.Context(), q.user.ID); err != nil {
			return nil, err
		}
		return s.legalSearchCases(q.r.Context(), q.user.ID, query, cursor, size)
	})
	register("cases/{id}", func(q request) (M, error) {
		id, err := legalPathID(q)
		if err != nil {
			return nil, err
		}
		kind := q.r.URL.Query().Get("kind")
		if q.r.URL.Query().Has("kind") && kind != "cluster" && kind != "opinion" {
			return nil, fail(422, "kind must be 'cluster' or 'opinion'")
		}
		if err = s.requireCourtToken(q.r.Context(), q.user.ID); err != nil {
			return nil, err
		}
		return s.legalCaseDetail(q.r.Context(), q.user.ID, id, kind)
	})
	register("validate-citation", func(q request) (M, error) {
		citation, err := legalQueryRequired(q, "citation")
		if err != nil {
			return nil, err
		}
		if err = s.requireCourtToken(q.r.Context(), q.user.ID); err != nil {
			return nil, err
		}
		result, err := s.validateCourtCitation(q.r.Context(), q.user.ID, citation)
		if err != nil {
			return nil, err
		}
		return legalCitationResponse(result), nil
	})
	register("precedents", func(q request) (M, error) {
		topic, err := legalQueryRequired(q, "topic")
		if err != nil {
			return nil, err
		}
		minimum, err := legalQueryInt(q, "min_citations", 5, 0, 1000000)
		if err != nil {
			return nil, err
		}
		limit, err := legalQueryInt(q, "limit", 20, 1, 100)
		if err != nil {
			return nil, err
		}
		if err = s.requireCourtToken(q.r.Context(), q.user.ID); err != nil {
			return nil, err
		}
		rows, err := s.legalPrecedents(q.r.Context(), q.user.ID, topic, q.r.URL.Query().Get("jurisdiction"), minimum, limit)
		return M{"precedents": rows, "count": len(rows)}, err
	})
	register("dockets", func(q request) (M, error) {
		query, err := legalQueryRequired(q, "query")
		if err != nil {
			return nil, err
		}
		limit, err := legalQueryInt(q, "limit", 20, 1, 100)
		if err != nil {
			return nil, err
		}
		if err = s.requireCourtToken(q.r.Context(), q.user.ID); err != nil {
			return nil, err
		}
		rows, err := s.legalDockets(q.r.Context(), q.user.ID, query, q.r.URL.Query().Get("court"), q.r.URL.Query().Get("party"), limit)
		return M{"dockets": rows, "count": len(rows)}, err
	})
	register("dockets/{id}", func(q request) (M, error) {
		id, err := legalPathID(q)
		if err != nil {
			return nil, err
		}
		if err = s.requireCourtToken(q.r.Context(), q.user.ID); err != nil {
			return nil, err
		}
		return s.legalDocketDetail(q.r.Context(), q.user.ID, id)
	})
	register("oral-arguments", func(q request) (M, error) {
		query, err := legalQueryRequired(q, "query")
		if err != nil {
			return nil, err
		}
		limit, err := legalQueryInt(q, "limit", 50, 1, 100)
		if err != nil {
			return nil, err
		}
		if err = s.requireCourtToken(q.r.Context(), q.user.ID); err != nil {
			return nil, err
		}
		rows, err := s.legalOralArguments(q.r.Context(), q.user.ID, query, q.r.URL.Query().Get("court"), limit)
		return M{"arguments": rows, "count": len(rows)}, err
	})
	register("trends", func(q request) (M, error) {
		topic, err := legalQueryRequired(q, "topic")
		if err != nil {
			return nil, err
		}
		start, err := legalQueryInt(q, "start_year", 2000, 1600, 2200)
		if err != nil {
			return nil, err
		}
		end, err := legalQueryInt(q, "end_year", time.Now().Year(), 1600, 2200)
		if err != nil {
			return nil, err
		}
		if end < start {
			return nil, fail(422, "end_year must be at least start_year")
		}
		if err = s.requireCourtToken(q.r.Context(), q.user.ID); err != nil {
			return nil, err
		}
		return s.legalTrends(q.r.Context(), q.user.ID, topic, start, end)
	})
}
