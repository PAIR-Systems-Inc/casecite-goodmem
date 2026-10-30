package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Shared pacing covers every CourtListener consumer in this server, including
// simultaneous research, trends and judge builds. Waiting callers recheck a
// newly imposed Retry-After penalty before starting their request.
type courtGate struct {
	slots         chan struct{}
	mu            sync.Mutex
	next, penalty time.Time
	interval      time.Duration
}

func newCourtGate() *courtGate {
	return &courtGate{slots: make(chan struct{}, 3), interval: 250 * time.Millisecond}
}

var fallbackCourtGate = newCourtGate()

func (g *courtGate) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case g.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-g.slots }
	for {
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		g.mu.Lock()
		start := g.next
		if g.penalty.After(start) {
			start = g.penalty
		}
		wait := time.Until(start)
		if wait <= 0 {
			g.next = time.Now().Add(g.interval)
			g.mu.Unlock()
			return release, nil
		}
		g.mu.Unlock()
		if err := courtWait(ctx, wait); err != nil {
			release()
			return nil, err
		}
	}
}

func (g *courtGate) penalize(delay time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	until := time.Now().Add(delay)
	if until.After(g.penalty) {
		g.penalty = until
	}
}
func courtWait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func courtRetryDelay(raw string, attempt int) time.Duration {
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil && !math.IsNaN(seconds) && !math.IsInf(seconds, 0) && seconds >= 0 {
		return time.Duration(min(seconds, 60) * float64(time.Second))
	}
	if date, err := http.ParseTime(raw); err == nil {
		return min(max(time.Until(date), 0), 60*time.Second)
	}
	return min(time.Duration(2<<min(attempt, 5))*time.Second, 60*time.Second)
}

type courtHTTPError struct{ status int }

func (e *courtHTTPError) Error() string {
	return fmt.Sprintf("CourtListener returned HTTP %d", e.status)
}
func (e *courtHTTPError) Unwrap() error {
	switch e.status {
	case 401, 403:
		return fail(502, "CourtListener rejected the configured API token. Check the token in Settings or COURTLISTENER_API_TOKEN.")
	case 404:
		return fail(404, "Not found on CourtListener.")
	case 429:
		return fail(503, "CourtListener is rate-limiting requests right now. Wait a minute and try again.")
	default:
		return fail(502, e.Error()+". Please try again.")
	}
}
func courtMissing(err error) bool {
	var ce *courtHTTPError
	return errors.As(err, &ce) && ce.status == 404
}

func (s *Server) courtJSON(ctx context.Context, user, path string, params url.Values) (M, error) {
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") || strings.ContainsAny(path, "?#\\") {
		return nil, bad("Invalid CourtListener resource")
	}
	key, err := s.providerKey(ctx, user, "courtlistener", s.cfg.CourtListenerToken)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(fallback(s.cfg.CourtListenerURL, "https://www.courtlistener.com/api/rest/v4"), "/")
	gate := s.court
	if gate == nil {
		gate = fallbackCourtGate
	}
	for attempt := 0; attempt < 4; attempt++ {
		release, e := gate.acquire(ctx)
		if e != nil {
			return nil, e
		}
		req, e := http.NewRequestWithContext(ctx, "GET", base+path+"?"+params.Encode(), nil)
		if e != nil {
			release()
			return nil, e
		}
		req.Header.Set("Accept", "application/json")
		if key != "" {
			req.Header.Set("Authorization", "Token "+key)
		}
		if contacted, ok := ctx.Value(courtContactKey{}).(*atomic.Bool); ok {
			contacted.Store(true)
		}
		resp, e := s.client.Do(req)
		if e != nil {
			release()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			var network net.Error
			if errors.As(e, &network) && network.Timeout() {
				return nil, fail(504, "CourtListener did not respond in time. Try again.")
			}
			if attempt == 0 {
				if e = courtWait(ctx, 2*time.Second); e != nil {
					return nil, e
				}
				continue
			}
			return nil, fail(502, "CourtListener could not be reached. Please try again.")
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			out := M{}
			e = json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&out)
			resp.Body.Close()
			release()
			if e != nil {
				return nil, fail(502, "CourtListener returned an invalid response. Please try again.")
			}
			return out, nil
		}
		code := resp.StatusCode
		delay := courtRetryDelay(resp.Header.Get("Retry-After"), attempt)
		resp.Body.Close()
		if code == 429 {
			gate.penalize(delay)
		}
		release()
		if code == 429 && attempt < 3 {
			continue
		}
		if code >= 500 && attempt == 0 {
			if e = courtWait(ctx, 2*time.Second); e != nil {
				return nil, e
			}
			continue
		}
		return nil, &courtHTTPError{status: code}
	}
	return nil, &courtHTTPError{status: 429}
}

func (s *Server) requireCourtToken(ctx context.Context, user string) error {
	key, err := s.providerKey(ctx, user, "courtlistener", s.cfg.CourtListenerToken)
	if err != nil {
		return err
	}
	if strings.TrimSpace(key) == "" {
		return fail(503, "A CourtListener API token is required. Add a token in Settings or configure COURTLISTENER_API_TOKEN.")
	}
	return nil
}
