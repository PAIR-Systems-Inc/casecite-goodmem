package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type connectorHTTPError struct{ Status int }

func (e *connectorHTTPError) Error() string {
	return fmt.Sprintf("provider returned HTTP %d", e.Status)
}

func connectorPublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48"} {
		if netip.MustParsePrefix(prefix).Contains(ip) {
			return false
		}
	}
	return true
}

func newConnectorDownloadClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// No environment proxy: destination validation and the actual connection
	// must use the same resolved address (including after redirects).
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("could not resolve download host")
		}
		for _, ip := range ips {
			if !connectorPublicIP(ip) {
				return nil, errors.New("download destination rejected")
			}
		}
		dialer := net.Dialer{Timeout: 10 * time.Second}
		var last error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		return nil, last
	}
	return &http.Client{Transport: transport, Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func connectorRetryDelay(header string, attempt int, now time.Time) time.Duration {
	delay := time.Duration(2*(attempt+1)) * time.Second
	if seconds, err := strconv.ParseUint(strings.TrimSpace(header), 10, 32); err == nil {
		delay = time.Duration(seconds) * time.Second
	} else if when, err := http.ParseTime(header); err == nil {
		delay = when.Sub(now)
	}
	return max(0, min(delay, 30*time.Second))
}
func connectorWait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func connectorSafeError(err error) string {
	var upstream *connectorHTTPError
	var api *apiError
	switch {
	case errors.As(err, &upstream):
		return upstream.Error()
	case errors.As(err, &api):
		return api.message
	case errors.Is(err, context.Canceled):
		return "operation cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "provider request timed out"
	default:
		return "could not complete the provider request"
	}
}

func (s *Server) connectorJSON(ctx context.Context, method, target string, headers http.Header, payload []byte, retry bool) (M, error) {
	attempts := 1
	if retry {
		attempts = 3
	}
	for attempt := 0; attempt < attempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
		if err != nil {
			return nil, bad("Invalid provider endpoint")
		}
		req.Header = headers.Clone()
		if req.Header == nil {
			req.Header = http.Header{}
		}
		req.Header.Set("Accept", "application/json")
		resp, err := s.connectorHTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fail(502, "Could not reach the connector provider")
		}
		if (resp.StatusCode == 429 || resp.StatusCode == 503) && attempt+1 < attempts {
			resp.Body.Close()
			if err = connectorWait(ctx, connectorRetryDelay(resp.Header.Get("Retry-After"), attempt, time.Now())); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, &connectorHTTPError{resp.StatusCode}
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
		resp.Body.Close()
		if err != nil {
			return nil, fail(502, "Could not read the connector response")
		}
		if len(raw) > 8<<20 {
			return nil, fail(502, "The connector response exceeds the supported page size")
		}
		out := M{}
		if len(raw) == 0 {
			return out, nil
		}
		if raw[0] == '[' {
			var rows []any
			if json.Unmarshal(raw, &rows) != nil {
				return nil, fail(502, "The connector returned invalid JSON")
			}
			return M{"list": rows}, nil
		}
		if json.Unmarshal(raw, &out) != nil || out == nil {
			return nil, fail(502, "The connector returned invalid JSON")
		}
		return out, nil
	}
	return nil, fail(502, "The connector provider is unavailable")
}

func connectorAllowedHost(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, suffix := range allowed {
		suffix = strings.ToLower(suffix)
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return len(allowed) == 0
}

// Browser-supplied downloads require HTTPS, public IPs and a provider host.
// An administrator-configured on-premise endpoint is trusted only on its own
// origin; redirects are validated and never forward bearer/session headers.
func (s *Server) connectorDownload(ctx context.Context, method, target string, headers http.Header, allowed []string, trustedOrigin string) ([]byte, error) {
	for attempt := 0; attempt < 3; attempt++ {
		current, verb, h := target, method, headers.Clone()
		retry := false
		for hop := 0; hop < 6; hop++ {
			u, err := url.Parse(current)
			trusted := trustedOrigin != "" && connectorSameOrigin(current, trustedOrigin)
			if err != nil || u.User != nil || u.Fragment != "" || u.Hostname() == "" || (!trusted && (u.Scheme != "https" || u.Port() != "" && u.Port() != "443" || !connectorAllowedHost(u.Hostname(), allowed))) {
				return nil, bad("Download URL rejected")
			}
			req, err := http.NewRequestWithContext(ctx, verb, current, nil)
			if err != nil {
				return nil, bad("Download URL rejected")
			}
			req.Header = h.Clone()
			client := s.downloadHTTP
			if trusted {
				client = s.connectorHTTP
			}
			resp, err := client.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, fail(502, "Could not download the provider file")
			}
			if resp.StatusCode >= 300 && resp.StatusCode < 400 {
				next, err := resp.Location()
				resp.Body.Close()
				if err != nil {
					return nil, fail(502, "The provider returned an invalid download redirect")
				}
				if !connectorSameOrigin(current, next.String()) {
					h = http.Header{}
				}
				if resp.StatusCode == 303 || resp.StatusCode == 301 || resp.StatusCode == 302 {
					verb = "GET"
				}
				current = next.String()
				continue
			}
			if (resp.StatusCode == 429 || resp.StatusCode == 503) && attempt < 2 {
				resp.Body.Close()
				if err = connectorWait(ctx, connectorRetryDelay(resp.Header.Get("Retry-After"), attempt, time.Now())); err != nil {
					return nil, err
				}
				retry = true
				break
			}
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				resp.Body.Close()
				return nil, &connectorHTTPError{resp.StatusCode}
			}
			if resp.ContentLength > s.cfg.MaxUpload {
				resp.Body.Close()
				return nil, bad("File exceeds the upload limit")
			}
			data, err := io.ReadAll(io.LimitReader(resp.Body, s.cfg.MaxUpload+1))
			resp.Body.Close()
			if err != nil {
				return nil, fail(502, "The provider file download was interrupted")
			}
			if int64(len(data)) > s.cfg.MaxUpload {
				return nil, bad("File exceeds the upload limit")
			}
			return data, nil
		}
		if !retry {
			return nil, fail(502, "Too many download redirects")
		}
	}
	return nil, fail(502, "Could not download the provider file")
}

func connectorPageURL(next, base string) (string, error) {
	u, err := url.Parse(next)
	root, e := url.Parse(base)
	if err != nil || e != nil || u.Fragment != "" || !connectorSameOrigin(next, base) || !strings.HasPrefix(u.EscapedPath(), strings.TrimSuffix(root.EscapedPath(), "/")+"/") {
		return "", fail(502, "The connector returned an invalid pagination URL")
	}
	return next, nil
}

func connectorURL(base, path string, values url.Values) string {
	u := strings.TrimRight(base, "/") + path
	if len(values) > 0 {
		u += "?" + values.Encode()
	}
	return u
}
func connectorBearer(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}}
}
