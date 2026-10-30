package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

func legalTrendResult(topic string, start, end, current int, counts map[int]int) M {
	years, complete := []M{}, []M{}
	byYear := M{}
	total := 0
	for year := start; year <= end; year++ {
		if count, ok := counts[year]; ok {
			row := M{"year": year, "count": count}
			years = append(years, row)
			byYear[strconv.Itoa(year)] = count
			total += count
			// Neither a partial current year nor a future year can establish a
			// decline in decided cases. Both remain visible in the chart.
			if year < current {
				complete = append(complete, row)
			}
		}
	}
	first, last := start, end
	if len(complete) > 0 {
		first = number(complete[0], "year", start)
		last = number(complete[len(complete)-1], "year", end)
	}
	trend := "insufficient"
	if len(complete) >= 2 {
		trend = "stable"
		if float64(counts[last]) > float64(counts[first])*1.05 {
			trend = "increasing"
		} else if float64(counts[last]) < float64(counts[first])*.95 {
			trend = "decreasing"
		}
	}
	_, partial := counts[current]
	return M{"topic": topic, "start_year": start, "end_year": end, "years": years, "cases_by_year": byYear, "years_failed": end - start + 1 - len(years),
		"total_cases": total, "trend": trend, "trend_basis": M{"from_year": first, "to_year": last}, "current_year_partial": end >= current && partial}
}

func (s *Server) legalTrends(ctx context.Context, user, topic string, start, end int) (M, error) {
	key, err := s.providerKey(ctx, user, "courtlistener", s.cfg.CourtListenerToken)
	if err != nil {
		return nil, err
	}
	cacheKey := "legal-trend:" + digest(string(jsonBytes([]any{user, s.cfg.CourtListenerURL, digest(key), strings.ToLower(strings.TrimSpace(topic)), start, end, time.Now().Year()})))
	var cached M
	if s.db != nil && s.kvGet(ctx, cacheKey, &cached) == nil {
		cached["topic"] = topic
		return cached, nil
	}
	sweep, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	years := make(chan int, end-start+1)
	for year := end; year >= start; year-- {
		years <- year
	}
	close(years)
	counts := map[int]int{}
	var mu sync.Mutex
	var fatal error
	var workers sync.WaitGroup
	for range 3 {
		workers.Go(func() {
			for year := range years {
				if sweep.Err() != nil {
					return
				}
				query, cancelQuery := context.WithTimeout(sweep, 30*time.Second)
				data, e := s.courtJSON(query, user, "/search/", url.Values{"q": {topic}, "type": {"o"}, "filed_after": {fmt.Sprintf("%04d-01-01", year)}, "filed_before": {fmt.Sprintf("%04d-12-31", year)}, "page_size": {"1"}})
				cancelQuery()
				mu.Lock()
				if e == nil {
					counts[year] = number(data, "count", 0)
				} else {
					var upstream *courtHTTPError
					if errors.As(e, &upstream) && (upstream.status == 401 || upstream.status == 403) {
						fatal = e
						cancel()
					}
				}
				mu.Unlock()
			}
		})
	}
	workers.Wait()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if fatal != nil {
		return nil, fatal
	}
	result := legalTrendResult(topic, start, end, time.Now().Year(), counts)
	if len(counts) == end-start+1 && len(counts) > 0 && s.db != nil {
		_ = s.kvSet(ctx, cacheKey, result, 15*time.Minute)
	}
	return result, nil
}
