package app

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTOTPStandardVectorsAndPasswordPolicy(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for _, v := range []struct {
		at   int64
		code string
	}{{59, "94287082"}, {1111111109, "07081804"}, {1111111111, "14050471"}, {1234567890, "89005924"}, {2000000000, "69279037"}, {20000000000, "65353130"}} {
		got, err := totpCode(secret, v.at/30, 8)
		if err != nil || got != v.code {
			t.Fatalf("TOTP at %d: %s, %v", v.at, got, err)
		}
	}
	for _, p := range []string{"Password123!", "P@ssw0rd1234!", "WelcomeSummer2024!", "test-password-123", "Abc!aaaaaaaa7", "1234567890!@"} {
		if validPassword(p) {
			t.Errorf("weak password accepted: %q", p)
		}
	}
	if !validPassword("Cedar!Trail7826") {
		t.Fatal("strong password rejected")
	}
}

func TestMFAChallengeRecoveryAndEnrollmentGate(t *testing.T) {
	f := newFixture(t)
	admin := f.admin()
	other := str(f.call("POST", "/auth/login", "login", M{"email": "owner@example.test", "password": "Cedar!Trail7826"}, 200), "access_token")
	setup := f.call("POST", "/auth/mfa/setup", admin, nil, 200)
	code, err := totpCode(str(setup, "secret"), time.Now().Unix()/30-1, 6)
	if err != nil {
		t.Fatal(err)
	}
	enabled := f.call("POST", "/auth/mfa/enable", admin, M{"code": code}, 200)
	backups := stringsOf(enabled["recovery_codes"])
	if len(backups) != 10 {
		t.Fatal(enabled)
	}
	f.call("GET", "/auth/me", other, nil, 401)
	challenge := f.call("POST", "/auth/login", "login", M{"email": "owner@example.test", "password": "Cedar!Trail7826"}, 200)
	if challenge["mfa_required"] != true || str(challenge, "access_token") != "" {
		t.Fatal("MFA login issued a session", challenge)
	}
	code, _ = totpCode(str(setup, "secret"), time.Now().Unix()/30, 6)
	verified := f.call("POST", "/auth/mfa/verify", "login", M{"mfa_token": str(challenge, "mfa_token"), "code": code}, 200)
	logged := str(verified, "access_token")
	f.call("GET", "/auth/me", logged, nil, 200)
	f.call("POST", "/auth/mfa/verify", "login", M{"mfa_token": str(challenge, "mfa_token"), "code": backups[0]}, 401)
	challenge = f.call("POST", "/auth/login", "login", M{"email": "owner@example.test", "password": "Cedar!Trail7826"}, 200)
	f.call("POST", "/auth/mfa/verify", "login", M{"mfa_token": str(challenge, "mfa_token"), "code": code}, 401)
	f.call("POST", "/auth/mfa/verify", "login", M{"mfa_token": str(challenge, "mfa_token"), "code": backups[0]}, 200)
	status := f.call("GET", "/auth/mfa/status", logged, nil, 200)
	if number(status, "recovery_codes_remaining", 0) != 9 {
		t.Fatal(status)
	}
	f.call("POST", "/auth/mfa/disable", logged, M{"code": backups[1], "password": "wrong"}, 401)
	f.call("POST", "/auth/mfa/disable", logged, M{"code": backups[0], "password": "Cedar!Trail7826"}, 401)
	f.call("POST", "/auth/mfa/disable", logged, M{"code": backups[1], "password": "Cedar!Trail7826"}, 200)
	f.s.cfg.RequireMFA = true
	me := f.call("GET", "/auth/me", logged, nil, 200)
	if me["mfa_enrollment_required"] != true {
		t.Fatal(me)
	}
	f.call("GET", "/documents", logged, nil, 403)
	f.call("POST", "/auth/device/approve", logged, M{"user_code": "ABCD-EFGH"}, 403)
	f.call("POST", "/auth/mfa/setup", logged, nil, 200)
}

func TestDeviceAuthorizationAndRefreshReplay(t *testing.T) {
	f := newFixture(t)
	admin := f.admin()
	start := f.call("POST", "/auth/device/start", "", M{}, 200)
	code := str(start, "device_code")
	if str(f.call("POST", "/auth/device/token", "", M{"device_code": code}, 200), "status") != "authorization_pending" {
		t.Fatal("device was not pending")
	}
	if str(f.call("POST", "/auth/device/token", "", M{"device_code": code}, 200), "status") != "slow_down" {
		t.Fatal("fast polling was not slowed")
	}
	f.call("POST", "/auth/device/approve", admin, M{"user_code": strings.ToLower(str(start, "user_code"))}, 200)
	f.call("POST", "/auth/device/approve", admin, M{"user_code": str(start, "user_code")}, 404)
	if _, err := f.s.db.Exec(`UPDATE cc_device_codes SET next_poll_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	grant := f.call("POST", "/auth/device/token", "", M{"device_code": code}, 200)
	if str(grant, "status") != "approved" || str(grant, "refresh_token") == "" {
		t.Fatal(grant)
	}
	f.call("GET", "/auth/me", str(grant, "access_token"), nil, 200)
	if str(f.call("POST", "/auth/device/token", "", M{"device_code": code}, 200), "status") != "expired_token" {
		t.Fatal("device grant was replayed")
	}
	rotated := f.call("POST", "/auth/refresh", "", M{"refresh_token": str(grant, "refresh_token")}, 200)
	f.call("GET", "/auth/me", str(grant, "access_token"), nil, 401)
	f.call("GET", "/auth/me", str(rotated, "access_token"), nil, 200)
	f.call("POST", "/auth/refresh", "", M{"refresh_token": str(grant, "refresh_token")}, 401)
	f.call("GET", "/auth/me", str(rotated, "access_token"), nil, 401)
	f.call("GET", "/auth/me", admin, nil, 200)
	start = f.call("POST", "/auth/device/start", "", M{}, 200)
	f.call("POST", "/auth/device/deny", admin, M{"user_code": str(start, "user_code")}, 200)
	if str(f.call("POST", "/auth/device/token", "", M{"device_code": str(start, "device_code")}, 200), "status") != "access_denied" {
		t.Fatal("denial ignored")
	}
}

func TestEditablePermissionsInvitesAndOffboarding(t *testing.T) {
	f := newFixture(t)
	admin := f.admin()
	invite := f.call("POST", "/admin/users/invite", admin, M{"email": "viewer@example.test", "name": "Viewer", "role": "viewer"}, 200)
	id := str(object(invite["user"]), "id")
	login := f.call("POST", "/auth/login", "login", M{"email": "viewer@example.test", "password": str(invite, "temporary_password")}, 200)
	viewer := str(login, "access_token")
	f.call("GET", "/documents", viewer, nil, 403)
	f.call("POST", "/auth/change-password", viewer, M{"current_password": str(invite, "temporary_password"), "new_password": "River!Grove4928"}, 200)
	f.call("GET", "/documents", viewer, nil, 200)
	f.call("POST", "/matters", viewer, M{"name": "Restricted"}, 403)
	f.call("GET", "/admin/users", viewer, nil, 403)
	matrix := f.call("PUT", "/admin/users/roles/viewer", admin, M{"permissions": []string{"documents.view", "matters.manage"}}, 200)
	if len(stringsOf(object(matrix["assigned"])["viewer"])) != 2 {
		t.Fatal(matrix)
	}
	f.call("POST", "/matters", viewer, M{"name": "Now permitted"}, 200)
	f.call("POST", "/admin/users/roles/reset", admin, nil, 200)
	f.call("POST", "/matters", viewer, M{"name": "Restricted again"}, 403)
	owner := f.call("GET", "/auth/me", admin, nil, 200)
	f.call("PATCH", "/admin/users/"+str(owner, "id")+"/role", admin, M{"role": "viewer"}, 400)
	f.call("PATCH", "/admin/users/"+id+"/active", admin, M{"is_active": false}, 200)
	f.call("GET", "/auth/me", viewer, nil, 401)
	f.call("POST", "/auth/login", "login", M{"email": "viewer@example.test", "password": "River!Grove4928"}, 401)
	f.call("GET", "/admin/users/"+id+"/export", admin, nil, 200)
	f.call("DELETE", "/admin/users/"+id, admin, nil, 200)
	f.call("GET", "/admin/users/"+id+"/export", admin, nil, 404)
}

func TestPasswordResetIsSingleUseAndRevokesSessions(t *testing.T) {
	f := newFixture(t)
	admin := f.admin()
	f.call("POST", "/auth/forgot-password", "request", M{"email": "owner@example.test"}, 200)
	var encrypted string
	if err := f.s.db.QueryRow(`SELECT payload FROM cc_outbox ORDER BY created_at DESC LIMIT 1`).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, "reset-password") {
		t.Fatal("reset link stored unencrypted")
	}
	plain, err := f.s.crypt(encrypted, true)
	if err != nil {
		t.Fatal(err)
	}
	var message outboundEmail
	if err = json.Unmarshal([]byte(plain), &message); err != nil {
		t.Fatal(err)
	}
	link := strings.Split(strings.Split(message.Body, "?token=")[1], "\r\n")[0]
	if f.call("POST", "/auth/verify-reset-token", "request", M{"token": link}, 200)["valid"] != true {
		t.Fatal("reset link invalid")
	}
	f.call("POST", "/auth/reset-password", "request", M{"token": link, "new_password": "River!Grove4928"}, 200)
	f.call("GET", "/auth/me", admin, nil, 401)
	f.call("POST", "/auth/reset-password", "request", M{"token": link, "new_password": "Cedar!Trail7826"}, 400)
	f.call("POST", "/auth/login", "login", M{"email": "owner@example.test", "password": "River!Grove4928"}, 200)
	if f.call("POST", "/auth/verify-reset-token", "request", M{"token": link}, 200)["valid"] != false {
		t.Fatal("reset link remained valid")
	}
	var count int
	if err = f.s.db.QueryRowContext(context.Background(), `SELECT count(*) FROM cc_outbox`).Scan(&count); err != nil || count != 1 {
		t.Fatal(err, count)
	}
}

func TestAuditDetectsEditingAndTruncation(t *testing.T) {
	f := newFixture(t)
	admin := f.admin()
	f.call("POST", "/matters", admin, M{"name": "Audit matter"}, 200)
	valid := f.call("GET", "/admin/audit/verify", admin, nil, 200)
	if valid["valid"] != true || number(valid, "entries_checked", 0) == 0 {
		t.Fatal(valid)
	}
	if _, err := f.s.db.Exec(`UPDATE cc_audit SET details='{"modified":true}' WHERE id=(SELECT min(id) FROM cc_audit)`); err != nil {
		t.Fatal(err)
	}
	if f.call("GET", "/admin/audit/verify", admin, nil, 200)["valid"] != false {
		t.Fatal("edited record accepted")
	}
	if _, err := f.s.db.Exec(`UPDATE cc_audit SET details=record->'action'`); err != nil {
		t.Fatal(err)
	}
	if f.call("GET", "/admin/audit/verify", admin, nil, 200)["valid"] != true {
		t.Fatal("restored record not valid")
	}
	if _, err := f.s.db.Exec(`DELETE FROM cc_audit WHERE id=(SELECT max(id) FROM cc_audit)`); err != nil {
		t.Fatal(err)
	}
	if f.call("GET", "/admin/audit/verify", admin, nil, 200)["valid"] != false {
		t.Fatal("truncated log accepted")
	}
	if err := f.s.appendAudit(context.Background(), "", "test", "", "", M{}); err == nil {
		t.Fatal("appended to a truncated signed chain")
	}
	if _, err := f.s.db.Exec(`DELETE FROM cc_audit_head`); err != nil {
		t.Fatal(err)
	}
	if err := f.s.appendAudit(context.Background(), "", "test", "", "", M{}); err == nil {
		t.Fatal("silently reset a deleted audit head")
	}
	if _, err := f.s.db.Exec(`UPDATE cc_audit_head SET signature='tampered'`); err != nil {
		t.Fatal(err)
	}
	f.s.cfg.AuditFailClosed = true
	f.call("POST", "/admin/users/invite", admin, M{"name": "No change", "email": "blocked@example.test", "role": "viewer"}, 503)
	var count int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM cc_users WHERE email='blocked@example.test'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("fail-closed request mutated data", count, err)
	}
}

func TestBrandingSettingsAndPrivateKeys(t *testing.T) {
	f := newFixture(t)
	admin := f.admin()
	user := f.account(admin, "colleague@example.test")
	f.call("PUT", "/branding", user, M{"firm_name": "Unauthorized"}, 403)
	brand := f.call("PUT", "/branding", admin, M{"firm_name": "Example LLP", "custom_css": "a{background:url(https://tracker.example/x)}"}, 200)
	if strings.Contains(str(brand, "custom_css"), "tracker.example") {
		t.Fatal("external CSS retained")
	}
	if str(f.call("GET", "/branding", "", nil, 200), "firm_name") != "Example LLP" {
		t.Fatal("branding not public")
	}
	f.call("PUT", "/settings/rag", user, M{"contract_playbook": "Keep negotiated liability caps", "top_k": 20}, 200)
	settings := f.call("PUT", "/settings/rag", user, M{"temperature": 0.3}, 200)
	if str(settings, "contract_playbook") != "Keep negotiated liability caps" || number(settings, "top_k", 0) != 20 {
		t.Fatal("partial settings update lost existing fields")
	}
	f.call("PUT", "/settings/rag", user, M{"chunk_size": 200, "chunk_overlap": 200}, 400)
	f.call("POST", "/user/keys/save", user, M{"key_type": "anthropic", "api_key": "sk-ant-fixture-sensitive-7826"}, 200)
	status := f.call("GET", "/user/keys/status", user, nil, 200)
	if status["anthropic_configured"] != true {
		t.Fatal(status)
	}
	if f.call("GET", "/user/keys/status", admin, nil, 200)["anthropic_configured"] != false {
		t.Fatal("other user's key visible")
	}
	masked := f.call("GET", "/user/keys/masked", user, nil, 200)
	if strings.Contains(str(masked, "anthropic"), "sensitive") {
		t.Fatal("secret exposed")
	}
	f.call("DELETE", "/user/keys/anthropic", user, nil, 200)
	for _, svg := range []string{`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`, `<svg onload="x()"/>`, `<!DOCTYPE svg><svg/>`, `<svg><image href="https://tracker.example/image"/></svg>`} {
		if validateSVG([]byte(svg)) == nil {
			t.Errorf("unsafe SVG accepted: %s", svg)
		}
	}
	if err := validateSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="20" fill="#123456"/></svg>`)); err != nil {
		t.Fatal(err)
	}
}
