package install

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestInitEnvPrivateStableSecretsAndComposeEscaping(t *testing.T) {
	t.Setenv("PORT", "18999")
	t.Setenv("OPENAI_API_KEY", `test-$TOKEN-"quote"-\backslash`)
	path := filepath.Join(t.TempDir(), ".env")
	if err := InitEnv(path); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	if !strings.Contains(text, `OPENAI_API_KEY="test-$$TOKEN-\"quote\"-\\backslash"`) || !strings.Contains(text, `PUBLIC_URL="http://localhost:18999"`) {
		t.Fatal("incorrect dotenv escaping or public URL")
	}
	secrets := regexp.MustCompile(`(?m)^(?:DB_ADMIN_PASSWORD|CASECITE_DB_PASSWORD|GOODMEM_DB_PASSWORD|SECRET_KEY|REGISTRATION_BOOTSTRAP_TOKEN)="([a-f0-9]{64})"$`).FindAllStringSubmatch(text, -1)
	seen := map[string]bool{}
	for _, secret := range secrets {
		seen[secret[1]] = true
	}
	if len(seen) != 5 {
		t.Fatal("expected five independent random secrets")
	}
	if err := InitEnv(path); err == nil {
		t.Fatal("overwrote existing secrets")
	}
	after, _ := os.ReadFile(path)
	if string(after) != text {
		t.Fatal("existing configuration changed")
	}
}

func TestInitEnvRejectsInjectedConfiguration(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "key\nDEBUG=false")
	path := filepath.Join(t.TempDir(), ".env")
	if err := InitEnv(path); err == nil {
		t.Fatal("accepted newline injection")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("left partial configuration")
	}
}

func TestWaitRequiresReadyModelAndHonorsCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		status         int
		want           bool
	}{
		{"ready", `{"data":[{"id":"pairsys/pairsys.ocr"}]}`, 200, true},
		{"wrong-model", `{"data":[{"id":"other-model"}]}`, 200, false},
		{"html", `<html>proxy login</html>`, 200, false},
		{"starting", `{"data":[{"id":"pairsys/pairsys.ocr"}]}`, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.response))
			}))
			defer s.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			err := Wait(ctx, s.URL, "pairsys/pairsys.ocr")
			if (err == nil) != tc.want {
				t.Fatalf("readiness = %v", err)
			}
		})
	}
}

func TestWaitRejectsRedirectsAndCredentials(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/login", 302) }))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if Wait(ctx, s.URL, "") == nil {
		t.Fatal("redirect is not readiness")
	}
	if Wait(context.Background(), "http://user:password@localhost/ready", "") == nil {
		t.Fatal("accepted URL credentials")
	}
}
