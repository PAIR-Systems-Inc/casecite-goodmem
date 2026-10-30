package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"
)

func TestDemoLoginDevelopmentIdentityAndBootstrap(t *testing.T) {
	f := newFixture(t)
	f.call("POST", "/auth/demo/login", "test", M{"email": demoEmail, "password": "wrong"}, 401)
	f.call("POST", "/auth/demo/login", "test", M{"email": "unknown@example.test", "password": "demo"}, 401)
	f.call("POST", "/auth/demo/login", "test", M{}, 401)
	for _, input := range []M{{"email": 1}, {"email": nil}, {"email": demoEmail, "password": false}, {"email": demoEmail, "password": nil}} {
		f.call("POST", "/auth/demo/login", "test", input, 422)
	}
	result := f.call("POST", "/auth/demo/login", "test", M{"email": demoEmail}, 200)
	u := object(result["user"])
	if str(u, "id") != demoUserID || str(u, "name") != "Dev User" || !slices.Equal(stringsOf(u["roles"]), []string{"viewer"}) {
		t.Fatal(result)
	}
	access := str(result, "access_token")
	f.call("GET", "/auth/me", access, nil, 200)
	f.call("GET", "/users", access, nil, 403)
	if f.call("GET", "/config", "", nil, 200)["setup_required"] != true {
		t.Fatal("development login blocked first-administrator setup")
	}
	admin := f.admin()
	if f.call("GET", "/config", "", nil, 200)["setup_required"] != false {
		t.Fatal("administrator registration did not complete setup")
	}
	f.upload(admin, "", "private.txt", "Private owner document, unavailable to the demonstration account.")
	if len(maps(f.call("GET", "/documents", access, nil, 200)["documents"])) != 0 {
		t.Fatal("development account could read the administrator's documents")
	}
	// Custom permissions still honor revocation; they cannot elevate this user.
	if err := f.s.kvSet(context.Background(), "permissions:viewer", []string{"documents.view", "admin.users", "documents.upload"}, 0); err != nil {
		t.Fatal(err)
	}
	current := f.call("GET", "/auth/me", access, nil, 200)
	if !slices.Equal(stringsOf(current["permissions"]), []string{"documents.view"}) {
		t.Fatal(current)
	}
}

func TestDemoLoginProductionRejectsExistingAccessAndRefresh(t *testing.T) {
	f := newFixture(t)
	req, _ := http.NewRequest("POST", f.http.URL+"/api/v1/auth/demo/login", bytes.NewReader(jsonBytes(M{"email": demoEmail, "password": "demo"})))
	req.Header.Set("Authorization", "Bearer fixture-csrf-bypass")
	req.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	result := M{}
	err = json.NewDecoder(response.Body).Decode(&result)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 {
		t.Fatal(err, response.StatusCode, result)
	}
	refresh := ""
	for _, cookie := range response.Cookies() {
		if cookie.Name == "refresh_token" {
			refresh = cookie.Value
		}
	}
	if refresh == "" {
		t.Fatal("missing browser refresh cookie")
	}
	f.s.cfg.Debug = false
	f.call("POST", "/auth/demo/login", "test", M{"email": demoEmail}, 403)
	f.call("GET", "/auth/me", str(result, "access_token"), nil, 401)
	f.call("POST", "/auth/refresh", "", M{"refresh_token": refresh}, 401)
}

func TestDemoLoginDoesNotTakeOverOrReactivateAccounts(t *testing.T) {
	for _, tc := range []struct {
		name, id, email, role, hash string
		active                      bool
	}{
		{"email collision", "existing-user", demoEmail, "admin", "existing-password-hash", true},
		{"ID collision", demoUserID, "existing@example.test", "viewer", "", true},
		{"promoted", demoUserID, demoEmail, "admin", "", true},
		{"password enabled", demoUserID, demoEmail, "viewer", "existing-password-hash", true},
		{"deactivated", demoUserID, demoEmail, "viewer", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.s.db.Exec(`INSERT INTO cc_users(id,email,name,role,password_hash,active) VALUES($1,$2,'Existing user',$3,$4,$5)`, tc.id, tc.email, tc.role, tc.hash, tc.active); err != nil {
				t.Fatal(err)
			}
			f.call("POST", "/auth/demo/login", "test", M{"email": demoEmail}, 401)
			var name, role, hash string
			var active bool
			if err := f.s.db.QueryRow(`SELECT name,role,password_hash,active FROM cc_users WHERE id=$1`, tc.id).Scan(&name, &role, &hash, &active); err != nil || name != "Existing user" || role != tc.role || hash != tc.hash || active != tc.active {
				t.Fatal("existing account changed", err)
			}
		})
	}
}

func TestDemoLoginConcurrentCreationKeepsOneViewer(t *testing.T) {
	f := newFixture(t)
	var wg sync.WaitGroup
	statuses := make(chan int, 6)
	for range 6 {
		wg.Go(func() {
			req, _ := http.NewRequest("POST", f.http.URL+"/api/v1/auth/demo/login", bytes.NewReader(jsonBytes(M{"email": demoEmail})))
			req.Header.Set("Authorization", "Bearer fixture-csrf-bypass")
			req.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				statuses <- 0
				return
			}
			response.Body.Close()
			statuses <- response.StatusCode
		})
	}
	wg.Wait()
	close(statuses)
	for status := range statuses {
		if status != 200 {
			t.Fatal("concurrent login failed", status)
		}
	}
	var count int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM cc_users WHERE id=$1 AND email=$2 AND role='viewer' AND password_hash=''`, demoUserID, demoEmail).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
