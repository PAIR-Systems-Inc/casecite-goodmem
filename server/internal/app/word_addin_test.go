package app

import (
	"net/http"
	"testing"
)

func TestExternalWordOriginUsesTokensWithoutCookies(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	f.s.cfg.AddinOrigins = []string{"https://word.example.test"}
	for _, tc := range []struct {
		method, origin, bearer string
		cookie                 bool
		status                 int
	}{
		{"OPTIONS", "https://word.example.test", "", false, 204},
		{"GET", "https://word.example.test", access, false, 200},
		{"GET", "https://word.example.test", "", false, 401},
		{"GET", "https://word.example.test", access, true, 403},
		{"GET", "https://unconfigured.example.test", access, false, 403},
	} {
		r, err := http.NewRequest(tc.method, f.http.URL+"/api/v1/auth/me", nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Origin", tc.origin)
		if tc.bearer != "" {
			r.Header.Set("Authorization", "Bearer "+tc.bearer)
		}
		if tc.cookie {
			r.AddCookie(&http.Cookie{Name: "access_token", Value: access})
		}
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("%s %s cookie=%v: %d, want %d", tc.method, tc.origin, tc.cookie, response.StatusCode, tc.status)
		}
		if response.Header.Get("Access-Control-Allow-Credentials") != "" {
			t.Fatal("external pane gained cookie credentials")
		}
		if tc.status == 200 && response.Header.Get("Access-Control-Allow-Origin") != tc.origin {
			t.Fatal("authorized pane origin missing")
		}
	}
}
