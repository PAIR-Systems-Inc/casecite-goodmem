package app

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

func TestMicrosoftIdentityValidationAndLocalRoles(t *testing.T) {
	f := newFixture(t)
	admin := f.admin()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const issuer = "https://login.microsoftonline.com/fixture-tenant/v2.0"
	keys := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, M{"keys": []M{{"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "test-key", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	}))
	defer keys.Close()
	f.s.cfg.MicrosoftClientID = "fixture-client"
	f.s.cfg.MicrosoftTenantID = "fixture-tenant"
	f.s.cfg.AzureAutoProvision = true
	f.s.cfg.AzureDefaultRole = "admin"
	f.s.azureVerifier = oidc.NewVerifier(issuer, oidc.NewRemoteKeySet(context.Background(), keys.URL), &oidc.Config{ClientID: "fixture-client", SupportedSigningAlgs: []string{"RS256"}})
	sign := func(claims M) string {
		header := base64.RawURLEncoding.EncodeToString(jsonBytes(M{"alg": "RS256", "kid": "test-key", "typ": "JWT"}))
		unsigned := header + "." + base64.RawURLEncoding.EncodeToString(jsonBytes(claims))
		hash := sha256.Sum256([]byte(unsigned))
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
		if err != nil {
			t.Fatal(err)
		}
		return unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)
	}
	call := func(method, path string, body M, cookie *http.Cookie, want int) (M, []*http.Cookie) {
		t.Helper()
		req, _ := http.NewRequest(method, f.http.URL+"/api/v1"+path, bytes.NewReader(jsonBytes(body)))
		req.Header.Set("Authorization", "Bearer identity-fixture")
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s: got %d, want %d: %s", path, resp.StatusCode, want, raw)
		}
		var result M
		if err = json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result, resp.Cookies()
	}
	config, cookies := call("GET", "/auth/azure/config", nil, nil, 200)
	var cookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "sso_state" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("missing browser-bound SSO state")
	}
	claims := M{"iss": issuer, "aud": "fixture-client", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": str(config, "nonce"), "tid": "fixture-tenant", "oid": "microsoft-user-1", "sub": "subject-1", "email": "sso@example.test", "name": "SSO User", "roles": []string{"admin"}}
	for _, badClaim := range []struct {
		key   string
		value any
	}{{"aud", "other-client"}, {"tid", "other-tenant"}, {"iss", "https://attacker.example"}, {"nonce", "incorrect"}, {"exp", time.Now().Add(-time.Hour).Unix()}, {"nbf", time.Now().Add(time.Hour).Unix()}} {
		old := claims[badClaim.key]
		claims[badClaim.key] = badClaim.value
		call("POST", "/auth/azure/login", M{"token": sign(claims)}, cookie, 401)
		if old == nil {
			delete(claims, badClaim.key)
		} else {
			claims[badClaim.key] = old
		}
	}
	result, _ := call("POST", "/auth/azure/login", M{"token": sign(claims)}, cookie, 200)
	if got := stringsOf(object(result["user"])["roles"]); len(got) != 1 || got[0] != "viewer" {
		t.Fatalf("provider claims escalated role: %v", result)
	}
	userID := str(object(result["user"]), "id")
	access := str(result, "access_token")
	f.call("GET", "/auth/me", access, nil, 200)
	f.call("GET", "/admin/users", access, nil, 403)
	call("POST", "/auth/azure/login", M{"token": sign(claims)}, cookie, 401)
	f.call("PATCH", "/admin/users/"+userID+"/role", admin, M{"role": "paralegal"}, 200)
	config, cookies = call("GET", "/auth/azure/config", nil, nil, 200)
	for _, c := range cookies {
		if c.Name == "sso_state" {
			cookie = c
		}
	}
	claims["nonce"] = str(config, "nonce")
	claims["email"] = "changed-email@example.test"
	result, _ = call("POST", "/auth/azure/login", M{"token": sign(claims)}, cookie, 200)
	if str(object(result["user"]), "id") != userID || strings.Join(stringsOf(object(result["user"])["roles"]), ",") != "paralegal" {
		t.Fatal("immutable identity or local role lost", result)
	}
	config, cookies = call("GET", "/auth/azure/config", nil, nil, 200)
	for _, c := range cookies {
		if c.Name == "sso_state" {
			cookie = c
		}
	}
	claims["nonce"] = str(config, "nonce")
	claims["oid"] = "another-object"
	claims["email"] = "owner@example.test"
	call("POST", "/auth/azure/login", M{"token": sign(claims)}, cookie, 403)
}
