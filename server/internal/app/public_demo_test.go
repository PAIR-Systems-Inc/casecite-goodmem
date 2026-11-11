package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

func demoTestServer(t *testing.T, live bool) *Server {
	t.Helper()
	f := newFixture(t)
	cfg := f.s.cfg
	cfg.PublicDemo = true
	if live {
		file := os.Getenv("TEST_GOODMEM_SECURITY_CONFIG")
		if file == "" {
			t.Skip("set TEST_GOODMEM_SECURITY_CONFIG for real GoodMem demo isolation")
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var security GoodMemSecurity
		if err = json.Unmarshal(b, &security); err != nil {
			t.Fatal(err)
		}
		cfg.GoodMemSecurity = &security
		cfg.GoodMemURL = os.Getenv("TEST_GOODMEM_BASE_URL")
		cfg.GoodMemKey = security.APIKey
		cfg.GoodMemSpace = security.SpaceID
	}
	s := New(cfg, f.s.db, fstest.MapFS{"index.html": {Data: []byte("demo shell")}}, f.s.log)
	t.Cleanup(s.Close)
	if err := s.InitializeGoodMemSecurity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializePublicDemo(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func demoRequest(t *testing.T, s *Server, method, path, cookie string, payload any, want int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://example.test/api/v1"+path, bytes.NewReader(jsonBytes(payload)))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Content-Type", "application/json")
	csrf := s.csrfValue()
	r.AddCookie(&http.Cookie{Name: "_csrf", Value: csrf})
	r.Header.Set("X-CSRF-Token", csrf)
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: demoCookie, Value: cookie})
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
	}
	return w
}

func demoInviteCookie(t *testing.T, s *Server, name string) string {
	t.Helper()
	link, err := s.DemoInvite(context.Background(), name, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	w := demoRequest(t, s, "POST", "/demo/redeem", "", M{"token": strings.Split(link, "#invite=")[1]}, 200)
	for _, c := range w.Result().Cookies() {
		if c.Name == demoCookie {
			if !c.HttpOnly || c.Value == "" {
				t.Fatal("invitation session must be HttpOnly")
			}
			return c.Value
		}
	}
	t.Fatal("missing demo session")
	return ""
}

func demoUploadFile(t *testing.T, s *Server, cookie, name, content string, want int) M {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	f, _ := mw.CreateFormFile("file", name)
	io.WriteString(f, content)
	mw.Close()
	r := httptest.NewRequest("POST", "http://example.test/api/v1/demo/private/documents", &b)
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Content-Type", mw.FormDataContentType())
	csrf := s.csrfValue()
	r.AddCookie(&http.Cookie{Name: "_csrf", Value: csrf})
	r.Header.Set("X-CSRF-Token", csrf)
	r.AddCookie(&http.Cookie{Name: demoCookie, Value: cookie})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("upload got %d want %d: %s", w.Code, want, w.Body.String())
	}
	out := M{}
	json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

func TestPublicDemoOnlyRegistersRestrictedRoutes(t *testing.T) {
	s := demoTestServer(t, false)
	demoRequest(t, s, "GET", "/demo/public/documents", "", nil, 200)
	demoRequest(t, s, "POST", "/demo/public/chat", "", M{"question": "What is the notice period?"}, 200)
	demoRequest(t, s, "POST", "/demo/private/documents", "", nil, 401)
	for _, path := range []string{"/auth/login", "/auth/register", "/auth/demo/login", "/users", "/documents", "/jobs", "/settings/clear", "/connectors/google/import", "/auth/device/token"} {
		demoRequest(t, s, "POST", path, "", M{}, 404)
	}
	r := httptest.NewRequest("POST", "http://example.test/api/v1/demo/redeem", strings.NewReader(`{"token":"fake"}`))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("demo mutations must enforce CSRF", w.Code)
	}
	demoRequest(t, s, "POST", "/demo/public/chat", "", M{"question": strings.Repeat("x", 2001)}, 400)
	for i := 0; i < 11; i++ {
		demoRequest(t, s, "POST", "/demo/public/chat", "", M{"question": "A question"}, 200)
	}
	demoRequest(t, s, "POST", "/demo/public/chat", "", M{"question": "Over limit"}, 429)
}

func TestPublicDemoInvitationLifecycle(t *testing.T) {
	s := demoTestServer(t, false)
	s.cfg.Debug = false
	ctx := context.Background()
	link, err := s.DemoInvite(ctx, "friend", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.Split(link, "#invite=")[1]
	var stored string
	if err = s.db.QueryRow(`SELECT invite_hash FROM cc_demo_guests`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == raw || stored != digest(raw) {
		t.Fatal("raw token persisted")
	}
	redeemed := demoRequest(t, s, "POST", "/demo/redeem", "", M{"token": raw}, 200)
	for _, cookie := range redeemed.Result().Cookies() {
		if cookie.Name == demoCookie && (!cookie.Secure || !cookie.HttpOnly) {
			t.Fatal("production invitation cookies must be Secure and HttpOnly")
		}
	}
	demoRequest(t, s, "POST", "/demo/redeem", "", M{"token": raw}, 401)
	cookie := demoInviteCookie(t, s, "friend")
	demoRequest(t, s, "GET", "/demo/session", cookie, nil, 200)
	if err = s.DemoRevoke(ctx, "friend"); err != nil {
		t.Fatal(err)
	}
	demoRequest(t, s, "GET", "/demo/private/documents", cookie, nil, 401)
	cookie = demoInviteCookie(t, s, "friend")
	if _, err = s.db.Exec(`UPDATE cc_demo_guests SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	demoRequest(t, s, "POST", "/demo/private/chat", cookie, M{"question": "secret"}, 401)
	cookie = demoInviteCookie(t, s, "friend")
	demoRequest(t, s, "POST", "/demo/logout", cookie, M{}, 200)
	demoRequest(t, s, "GET", "/demo/session", cookie, nil, 401)
	cookie = demoInviteCookie(t, s, "friend")
	demoInviteCookie(t, s, "friend")
	demoRequest(t, s, "GET", "/demo/session", cookie, nil, 401)
	var passwords int
	if err = s.db.QueryRow(`SELECT count(*) FROM cc_users WHERE password_hash<>''`).Scan(&passwords); err != nil || passwords != 0 {
		t.Fatal("demo stored a password", err)
	}
	if _, err = s.db.Exec(`UPDATE cc_users SET password_hash='existing-password' WHERE id=$1`, publicDemoUser); err != nil {
		t.Fatal(err)
	}
	if err = s.InitializePublicDemo(ctx); err == nil {
		t.Fatal("must reject an existing password database")
	}
}

func TestPublicDemoConcurrentUploadQuota(t *testing.T) {
	s := demoTestServer(t, false)
	cookie := demoInviteCookie(t, s, "friend")
	// An invalid upload must not consume a slot.
	demoUploadFile(t, s, cookie, "empty.txt", "", 400)
	var count atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.db.Exec(`INSERT INTO cc_objects(kind,id,owner_id,data) VALUES('document',$1,'microsite-guest-friend','{}')`, uuid())
			if err == nil {
				count.Add(1)
			}
		}()
	}
	wg.Wait()
	if count.Load() != 10 {
		t.Fatalf("reserved %d documents, want exactly 10", count.Load())
	}
	demoUploadFile(t, s, cookie, "eleventh.txt", "Valid text", 403)
	if _, err := s.db.Exec(`DELETE FROM cc_objects WHERE owner_id='microsite-guest-friend'`); err != nil {
		t.Fatal(err)
	}
	cookie = demoInviteCookie(t, s, "friend")
	demoUploadFile(t, s, cookie, "quota-bypass.txt", "Valid text", 403)
	// The durable quota survives server recreation (same database).
	other := New(s.cfg, s.db, fstest.MapFS{}, s.log)
	defer other.Close()
	demoUploadFile(t, other, cookie, "restart-bypass.txt", "Valid text", 403)
}

func testDemoIsolation(t *testing.T, live bool) {
	s := demoTestServer(t, live)
	ctx := context.Background()
	public, _, err := s.beginDocumentIngest(goodMemUserContext(ctx, publicDemoUser), publicDemoUser, "", uuid(), "public.txt", "local", "", []byte("Public agreement: notice is 30 days."), M{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.waitDocumentIndexed(ctx, publicDemoUser, str(public, "id")); err != nil {
		t.Fatal(err)
	}
	a := demoInviteCookie(t, s, "alice")
	b := demoInviteCookie(t, s, "bob")
	private := demoUploadFile(t, s, a, "alice-private.txt", "Alice confidential agreement: notice is 79 days.", 200)
	if _, err = s.waitDocumentIndexed(ctx, "microsite-guest-alice", str(private, "id")); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []struct{ cookie, path, want, denied string }{{"", "public", "public.txt", "alice-private.txt"}, {a, "private", "alice-private.txt", "public.txt"}, {b, "private", "", "alice-private.txt"}} {
		w := demoRequest(t, s, "GET", "/demo/"+scope.path+"/documents", scope.cookie, nil, 200)
		if !strings.Contains(w.Body.String(), scope.want) || strings.Contains(w.Body.String(), scope.denied) {
			t.Fatal("document isolation failed", w.Body.String())
		}
		w = demoRequest(t, s, "POST", "/demo/"+scope.path+"/chat", scope.cookie, M{"question": "What is the notice period?", "document_ids": []string{str(private, "id")}}, 200)
		if strings.Contains(w.Body.String(), scope.denied) {
			t.Fatal("source isolation failed", w.Body.String())
		}
		if scope.want != "" && !strings.Contains(w.Body.String(), scope.want) {
			t.Fatal("missing expected evidence", w.Body.String())
		}
	}
	var history int
	if err = s.db.QueryRow(`SELECT count(*) FROM cc_objects WHERE kind<>'document'`).Scan(&history); err != nil || history != 0 {
		t.Fatal("demo query persisted shared conversation state", err, history)
	}
}

func TestPublicDemoCollectionIsolation(t *testing.T) { testDemoIsolation(t, false) }
func TestPublicDemoGoodMemLive(t *testing.T)         { testDemoIsolation(t, true) }
