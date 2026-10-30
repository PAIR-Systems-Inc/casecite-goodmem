package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCourtRetryAndSafeErrors(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	f.s.cfg.CourtListenerToken = "fixture"
	f.s.court.interval = 0
	var calls atomic.Int32
	var code atomic.Int32
	code.Store(429)
	var recoverAt atomic.Int32
	recoverAt.Store(3)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if recoverAt.Load() > 0 && call >= recoverAt.Load() {
			writeJSON(w, 200, M{"results": []M{}})
			return
		}
		w.Header().Set("Retry-After", "0")
		if code.Load() == 302 {
			w.Header().Set("Location", "http://127.0.0.1:1/credential-destination")
		}
		if code.Load() == 200 {
			w.Write([]byte("not json"))
			return
		}
		w.WriteHeader(int(code.Load()))
	}))
	t.Cleanup(provider.Close)
	f.s.cfg.CourtListenerURL = provider.URL
	f.call("GET", "/tools/cases/search?q=x", access, nil, 200)
	if calls.Load() != 3 {
		t.Fatal("429 retry budget wrong", calls.Load())
	}
	for _, tc := range []struct{ upstream, want, count int }{{429, 503, 4}, {401, 502, 1}, {403, 502, 1}, {404, 404, 1}, {503, 502, 2}, {302, 502, 1}, {200, 502, 1}} {
		calls.Store(0)
		code.Store(int32(tc.upstream))
		recoverAt.Store(0)
		f.call("GET", "/tools/cases/search?q=x", access, nil, tc.want)
		if calls.Load() != int32(tc.count) {
			t.Fatalf("status %d made %d requests, wanted %d", tc.upstream, calls.Load(), tc.count)
		}
	}
	for _, tc := range []struct {
		header  string
		attempt int
		want    time.Duration
	}{{"0", 0, 0}, {".25", 0, 250 * time.Millisecond}, {"600", 0, 60 * time.Second}, {"NaN", 1, 4 * time.Second}, {"-1", 0, 2 * time.Second}, {"garbage", 2, 8 * time.Second}} {
		if got := courtRetryDelay(tc.header, tc.attempt); got != tc.want {
			t.Error(tc, got)
		}
	}
	date := time.Now().Add(20 * time.Second).UTC().Format(http.TimeFormat)
	if wait := courtRetryDelay(date, 0); wait < 18*time.Second || wait > 20*time.Second {
		t.Fatal("HTTP-date Retry-After ignored", wait)
	}
}

func TestCourtGatePacingConcurrencyPenaltyAndCancellation(t *testing.T) {
	g := newCourtGate()
	g.interval = 10 * time.Millisecond
	var mu sync.Mutex
	starts := []time.Time{}
	active, peak := 0, 0
	var workers sync.WaitGroup
	for range 9 {
		workers.Go(func() {
			release, err := g.acquire(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			starts = append(starts, time.Now())
			active++
			peak = max(peak, active)
			mu.Unlock()
			time.Sleep(40 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			release()
		})
	}
	workers.Wait()
	if peak != 3 {
		t.Fatal("concurrency limit", peak)
	}
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < 8*time.Millisecond {
			t.Fatal("request starts not paced", starts)
		}
	}

	// A penalty imposed after another caller began waiting must move that
	// waiting caller's start too, not only starts reserved after the 429.
	g.interval = 0
	g.next = time.Now().Add(45 * time.Millisecond)
	acquired := make(chan time.Time, 1)
	go func() {
		release, err := g.acquire(context.Background())
		if err == nil {
			acquired <- time.Now()
			release()
		}
	}()
	deadline := time.Now().Add(time.Second)
	for len(g.slots) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("waiter did not enter gate")
		}
		time.Sleep(time.Millisecond)
	}
	g.penalize(100 * time.Millisecond)
	penaltySet := time.Now()
	if start := <-acquired; start.Sub(penaltySet) < 85*time.Millisecond {
		t.Fatal("queued waiter bypassed new retry penalty")
	}

	g.penalize(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := g.acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("rate-limit wait did not cancel", err)
	}
	if len(g.slots) != 0 {
		t.Fatal("canceled waiter leaked a concurrency slot")
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err := g.acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("already canceled caller was admitted", err)
	}
}

func TestLegalCitationResponseCountsAndCases(t *testing.T) {
	rows := func(kind string, count int) []M {
		out := []M{}
		for i := range count {
			out = append(out, M{"id": i, "case_name": kind, "citation": []string{"410 U.S. 113", "93 S. Ct. 705"}, "court": "scotus", "date_filed": "2020-01-01", "treatment": kind, "snippet": "Near citation.", "context_found": true, "url": "https://www.courtlistener.com/opinion/1/"})
		}
		return out
	}
	result := legalCitationResponse(M{"negative_citations": rows("negative", 25), "caution_citations": rows("caution", 25), "positive_citations": rows("positive", 25), "is_good_law": nil, "warning_level": "danger"})
	if result["negative_citations"] != 25 || result["positive_citations"] != 25 || result["caution_citations"] != 25 || result["is_good_law"] != nil {
		t.Fatal(result)
	}
	cases := maps(result["citing_cases"])
	if len(cases) != 60 || str(cases[0], "treatment") != "negative" || str(cases[25], "treatment") != "caution" || str(cases[50], "treatment") != "positive" || str(cases[0], "citation") != "410 U.S. 113" {
		t.Fatal(result)
	}
}
