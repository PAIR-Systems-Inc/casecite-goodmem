package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

type Server struct {
	cfg           Config
	db            *sql.DB
	gm            *GoodMem
	log           *slog.Logger
	mux           *http.ServeMux
	client        *http.Client
	connectorHTTP *http.Client
	downloadHTTP  *http.Client
	court         *courtGate
	ocr           chan struct{}
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	uploads       chan struct{}
	azureVerifier *oidc.IDTokenVerifier
	startedAt     string
}
type request struct {
	w    http.ResponseWriter
	r    *http.Request
	user User
}
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string           { return e.message }
func fail(status int, message string) error { return &apiError{status, message} }
func bad(message string) error              { return fail(400, message) }
func missing() error                        { return fail(404, "Not found") }

func New(cfg Config, db *sql.DB, static fs.FS, log *slog.Logger) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{cfg: cfg, db: db, log: log, mux: http.NewServeMux(), client: &http.Client{Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, ctx: ctx, cancel: cancel, uploads: make(chan struct{}, 2)}
	s.gm = NewGoodMem(cfg)
	s.startedAt = nowString()
	s.connectorHTTP = &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	s.downloadHTTP = newConnectorDownloadClient()
	s.court = newCourtGate()
	s.ocr = make(chan struct{}, 2)
	s.configureAzure()
	s.routes()
	s.mux.HandleFunc("GET /word-addin/manifest.xml", s.addinManifest(static))
	s.mux.HandleFunc("GET /auth/microsoft-callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>CaseCite sign-in</title></head><body><p>Completing Microsoft sign-in. This window will close automatically.</p></body></html>`))
	})
	s.mux.HandleFunc("GET /health", s.health)
	s.mux.HandleFunc("GET /ready", s.health)
	s.api("GET /metrics", true, s.metrics)
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, M{"detail": "This endpoint is not available in the Go edition."})
	})
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", 405)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "word-addin" {
			name = "word-addin/index.html"
		}
		if name == "." || name == "" {
			name = "index.html"
		}
		b, err := fs.ReadFile(static, name)
		if err != nil {
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
			if strings.HasPrefix(r.URL.Path, "/word-addin/") {
				name = "word-addin/index.html"
			}
			b, err = fs.ReadFile(static, name)
		}
		if err != nil {
			http.Error(w, "Frontend not built", 503)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public,max-age=31536000,immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeContent(w, r, name, time.Time{}, strings.NewReader(string(b)))
	})
	return s
}

func (s *Server) api(pattern string, public bool, fn func(request) (any, error)) {
	permissions := routePermissions(pattern)
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		q := request{w: w, r: r}
		if !public {
			u, err := s.authenticate(r)
			if err != nil {
				s.respondError(w, err)
				return
			}
			q.user = u
			if u.MustChangePassword && r.URL.Path != "/api/v1/auth/change-password" && !strings.HasPrefix(r.URL.Path, "/api/v1/auth/logout") && r.URL.Path != "/api/v1/auth/me" {
				writeJSON(w, 403, M{"detail": "Change your temporary password to continue.", "code": "password_change_required"})
				return
			}
			if u.MFAEnrollmentRequired && !accountGateAllowed(r.URL.Path) {
				writeJSON(w, 403, M{"detail": "Set up two-factor authentication to continue.", "code": "mfa_enrollment_required"})
				return
			}
			if len(permissions) > 0 {
				allowed := false
				for _, key := range permissions {
					allowed = allowed || slices.Contains(u.Permissions, key)
				}
				if !allowed {
					s.respondError(w, fail(403, "Your role does not include permission for this action. Ask an administrator to grant it in Settings > Users."))
					return
				}
			}
		}
		if s.cfg.AuditFailClosed && r.Method != "GET" && r.Method != "HEAD" {
			if err := s.appendAudit(r.Context(), q.user.ID, "request.authorized", "endpoint", r.URL.Path, M{"method": r.Method}); err != nil {
				s.log.Error("required audit write failed", "error", err)
				s.respondError(w, fail(503, "The audit trail is unavailable. The request was not performed."))
				return
			}
		}
		v, err := fn(q)
		if err != nil {
			s.respondError(w, err)
			return
		}
		if v != nil {
			writeJSON(w, 200, v)
		}
	})
}
func (s *Server) respondError(w http.ResponseWriter, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		writeJSON(w, ae.status, M{"detail": ae.message})
		return
	}
	if notFound(err) {
		writeJSON(w, 404, M{"detail": "Not found"})
		return
	}
	var ge *GoodMemError
	if errors.As(err, &ge) {
		s.log.Warn("GoodMem operation failed", "status", ge.Status)
		writeJSON(w, 502, M{"detail": "GoodMem could not complete the request. Check its processing status and connection."})
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	s.log.Error("request failed", "error", err)
	writeJSON(w, 500, M{"detail": "The server could not complete this request. Check the server log."})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func body(q request) (M, error) {
	m := M{}
	d := json.NewDecoder(io.LimitReader(q.r.Body, 16<<20))
	if err := d.Decode(&m); err != nil {
		return nil, bad("Invalid JSON body")
	}
	if m == nil {
		return nil, bad("A JSON object is required")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, bad("Unexpected data after JSON body")
	}
	return m, nil
}
func uuid() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
func admin(q request) error {
	return permit(q, "admin.users")
}

func accountGateAllowed(p string) bool {
	return p == "/api/v1/auth/me" || p == "/api/v1/auth/change-password" || strings.HasPrefix(p, "/api/v1/auth/logout") || p == "/api/v1/auth/mfa/status" || p == "/api/v1/auth/mfa/setup" || p == "/api/v1/auth/mfa/enable"
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://accounts.google.com https://apis.google.com https://www.gstatic.com https://www.dropbox.com https://cdn01.boxcdn.net; style-src 'self' 'unsafe-inline' https://cdn01.boxcdn.net; img-src 'self' data: blob: https://*.googleusercontent.com https://*.gstatic.com https://*.boxcloud.com https://*.box.com https://*.dropboxusercontent.com; font-src 'self' data: https://cdn01.boxcdn.net; media-src 'self' https://storage.courtlistener.com https://*.courtlistener.com; connect-src 'self' https://login.microsoftonline.com https://graph.microsoft.com https://accounts.google.com https://*.googleapis.com https://*.box.com https://*.boxcloud.com https://*.sharepoint.com https://*.sharepoint.us https://*.sharepoint.de https://*.sharepoint.cn https://onedrive.live.com; frame-src 'self' blob: https://login.microsoftonline.com https://accounts.google.com https://docs.google.com https://drive.google.com https://www.dropbox.com; object-src 'none'; base-uri 'self'; frame-ancestors 'self'")
	if strings.HasPrefix(r.URL.Path, "/word-addin/") {
		w.Header().Del("X-Frame-Options")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://appsforoffice.microsoft.com; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' https:; frame-src 'self' https://appsforoffice.microsoft.com; object-src 'none'; base-uri 'self'; frame-ancestors 'self' https://*.office.com https://*.officeapps.live.com https://*.microsoft365.com https://*.cloud.microsoft https://*.sharepoint.com")
	}
	if !s.cfg.Debug {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		allowed := origin == s.cfg.PublicURL || (s.cfg.Debug && (origin == "http://localhost:3000" || origin == "http://localhost:5173" || origin == "http://127.0.0.1:5173"))
		addin := !allowed && slices.Contains(s.cfg.AddinOrigins, origin)
		if !allowed && !addin {
			writeJSON(w, 403, M{"detail": "Origin is not allowed"})
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		if addin {
			// A separately hosted Office pane uses bearer/device tokens. Never
			// grant its origin access to a browser's cookie-authenticated session.
			for _, name := range []string{"access_token", "refresh_token"} {
				if _, err := r.Cookie(name); err == nil {
					writeJSON(w, 403, M{"detail": "The external Word add-in must use token authentication without cookies"})
					return
				}
			}
		} else {
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	}
	if r.Method == "OPTIONS" {
		w.WriteHeader(204)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUpload+(2<<20))
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("Authorization") == "" && !cookieLessDeviceRequest(r) {
			cookie, err := r.Cookie("_csrf")
			if err != nil || cookie.Value != r.Header.Get("X-CSRF-Token") || !s.validCSRF(cookie.Value) {
				writeJSON(w, 403, M{"detail": "CSRF token missing or invalid. Refresh the page."})
				return
			}
		}
		limit := 300
		window := time.Minute
		bucket := "request:"
		if r.URL.Path == "/api/v1/auth/login" || r.URL.Path == "/api/v1/auth/register" {
			limit = 10
			window = 15 * time.Minute
			bucket = "signin:"
		}
		n, err := s.countWindow(r.Context(), "rate:"+bucket+remoteIP(r), window)
		if err != nil {
			writeJSON(w, 503, M{"detail": "Session storage is unavailable"})
			return
		}
		if n > limit {
			w.Header().Set("Retry-After", fmt.Sprint(int(window.Seconds())))
			writeJSON(w, 429, M{"detail": "Too many requests. Please try again later."})
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

// Device start/poll and body-token refresh must work in Office's cookie-less
// pane. Cookie-authenticated calls still require CSRF, including these paths.
func cookieLessDeviceRequest(r *http.Request) bool {
	if r.Method != "POST" {
		return false
	}
	if _, err := r.Cookie("access_token"); err == nil {
		return false
	}
	if _, err := r.Cookie("refresh_token"); err == nil {
		return false
	}
	return r.URL.Path == "/api/v1/auth/device/start" || r.URL.Path == "/api/v1/auth/device/token" || r.URL.Path == "/api/v1/auth/refresh"
}

func safeHTTPURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
}

func (s *Server) Close() { s.cancel(); s.wg.Wait() }
