package app

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestSecurityConfigurationRejectsLegacyCredentialsAndUnsafeSSODefaults(t *testing.T) {
	file := t.TempDir() + "/runtime.json"
	t.Setenv("DATABASE_URL", "postgres://fixture/test")
	t.Setenv("SECRET_KEY", strings.Repeat("s", 40))
	t.Setenv("DEBUG", "true")
	t.Setenv("GOODMEM_API_KEY", "")
	t.Setenv("GOODMEM_SPACE_ID", "")
	t.Setenv("GOODMEM_RERANKER_ID", "")
	t.Setenv("GOODMEM_CONFIG_FILE", file)
	t.Setenv("GOOGLE_SSO_CLIENT_ID", "")
	t.Setenv("GOOGLE_SSO_ALLOWED_DOMAINS", "")
	t.Setenv("GOOGLE_SSO_AUTO_PROVISION", "")
	if err := savePrivateJSON(file, M{"api_key": "legacy-root", "space_id": "legacy-space"}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(); err == nil {
		t.Fatal("legacy root-key runtime configuration accepted")
	}
	scoped := GoodMemSecurity{Version: 1, ControllerID: uuid(), APIKey: "scoped-controller", SpaceID: uuid(), Subjects: []GoodMemSubject{{uuid(), "scoped-reader"}}}
	if err := savePrivateJSON(file, scoped); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOODMEM_API_KEY", "legacy-root")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("environment root key replaced scoped configuration")
	}
	t.Setenv("GOODMEM_API_KEY", "")
	t.Setenv("GOOGLE_SSO_CLIENT_ID", "google-client")
	for _, domain := range []string{"", "*", "*.example.com", "example.com/attacker"} {
		t.Setenv("GOOGLE_SSO_ALLOWED_DOMAINS", domain)
		if _, err := LoadConfig(); err == nil {
			t.Fatalf("unsafe Google domain configuration accepted: %q", domain)
		}
	}
	t.Setenv("GOOGLE_SSO_ALLOWED_DOMAINS", "Example.com,example.org")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GoogleAutoProvision || cfg.GoogleDefaultRole != "viewer" || strings.Join(cfg.GoogleSSODomains, ",") != "example.com,example.org" {
		t.Fatal("unexpected Google enrollment defaults")
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("runtime secrets are not private", err)
	}
}

func TestSetupSerializesProvisioningAndCannotOverwriteItsAdministrativeFile(t *testing.T) {
	file := t.TempDir() + "/admin.json"
	unlock, err := lockGoodMemSetup(file)
	if err != nil {
		t.Fatal(err)
	}
	if second, e := lockGoodMemSetup(file); e == nil {
		second()
		t.Fatal("concurrent provisioning lock succeeded")
	}
	unlock()
	t.Setenv("GOODMEM_PROVISIONING_FILE", file)
	if err := SetupGoodMem(context.Background(), file); err == nil {
		t.Fatal("setup overwrote its administrative file with runtime credentials")
	}
}

func TestSetupRejectsUnknownTemporaryGrantJournal(t *testing.T) {
	for _, journal := range []any{[]string{"pending-grant"}, []M{{"id": "pending-grant"}}, M{"id": "pending-grant"}} {
		state := M{"principal_id": "reader", "temporary_grants": journal}
		before := string(jsonBytes(state))
		if err := finishSetupTemporaryGrants(context.Background(), nil, state); err == nil || !strings.Contains(err.Error(), "invalid temporary grant journal") {
			t.Fatal("unknown pending grant state was accepted", err)
		}
		if string(jsonBytes(state)) != before {
			t.Fatal("unresolved grant journal was discarded")
		}
	}
}
