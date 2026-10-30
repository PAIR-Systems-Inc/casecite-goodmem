package app

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func connectorFixture(t *testing.T) (*fixture, string, string) {
	t.Helper()
	f := newFixture(t)
	f.s.cfg.PublicURL = f.http.URL
	f.s.cfg.ConnectorSettings = map[string]string{"clio_base_url": "https://app.clio.com", "filevine_base_url": "https://api.filevine.io", "netdocuments_vault_host": "https://vault.netvoyage.com", "netdocuments_api_host": "https://api.vault.netvoyage.com", "imanage_base_url": "https://cloudimanage.com", "microsoft_tenant_id": "common"}
	for _, d := range connectorDefinitions {
		f.s.cfg.ConnectorSettings[d.IDField] = "fixture-client"
		f.s.cfg.ConnectorSettings[d.SecretField] = "fixture-secret"
		if d.RedirectField != "" {
			f.s.cfg.ConnectorSettings[d.RedirectField] = f.http.URL + "/api/v1/connectors/" + d.ID + "/callback"
		}
	}
	f.s.cfg.ExperimentalConnectors, f.s.cfg.FilevineEnabled = true, true
	admin := f.admin()
	user := str(f.call("GET", "/auth/me", admin, nil, 200), "id")
	return f, admin, user
}
func seedConnector(t *testing.T, f *fixture, user, provider string, expired bool) string {
	t.Helper()
	expires := time.Now().Add(time.Hour).Unix()
	if expired {
		expires = 1
	}
	creds := M{"access_token": "fixture-access", "refresh_token": "fixture-refresh", "client_id": "fixture-client", "expires_at": expires, "customer_id": "customer", "user_id": "fv-user", "org_id": "fv-org"}
	sealed, err := f.s.crypt(string(jsonBytes(creds)), false)
	if err != nil {
		t.Fatal(err)
	}
	generation := uuid()
	_, err = f.s.db.Exec(`INSERT INTO cc_connector_accounts(user_id,provider,generation,credentials) VALUES($1,$2,$3,$4) ON CONFLICT(user_id,provider) DO UPDATE SET generation=$3,credentials=$4`, user, provider, generation, sealed)
	if err != nil {
		t.Fatal(err)
	}
	return generation
}
func connectorResponse(status int, body any, headers http.Header) *http.Response {
	if headers == nil {
		headers = http.Header{}
	}
	content, ok := body.(string)
	if !ok {
		content = string(jsonBytes(body))
	}
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(content)), ContentLength: int64(len(content))}
}
func connectorRequest(t *testing.T, f *fixture, method, path, bearer string, cookie *http.Cookie, accept string) (*http.Response, M) {
	t.Helper()
	req, _ := http.NewRequest(method, f.http.URL+"/api/v1"+path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	req.Header.Set("Accept", accept)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := M{}
	_ = json.Unmarshal(raw, &out)
	return resp, out
}

func TestConnectorOAuthBrowserBindingEncryptionAndReplay(t *testing.T) {
	f, admin, user := connectorFixture(t)
	var tokens atomic.Int32
	f.s.connectorHTTP.Transport = modelTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/token":
			tokens.Add(1)
			_ = r.ParseForm()
			if r.Form.Get("client_secret") != "fixture-secret" || r.Form.Get("grant_type") != "authorization_code" {
				t.Error("incorrect token exchange")
			}
			return connectorResponse(200, M{"access_token": "private-access", "refresh_token": "private-refresh", "expires_in": 3600}, nil), nil
		case "/oauth2/v2/userinfo":
			return connectorResponse(200, M{"name": "Fixture", "email": "fixture@example.test"}, nil), nil
		default:
			t.Errorf("unexpected provider request %s", r.URL)
			return connectorResponse(404, M{}, nil), nil
		}
	})
	begin := func() (M, *http.Cookie) {
		r, out := connectorRequest(t, f, "GET", "/connectors/google_drive/auth", admin, nil, "")
		if r.StatusCode != 200 || len(r.Cookies()) == 0 {
			t.Fatalf("start: %d %v", r.StatusCode, out)
		}
		return out, r.Cookies()[0]
	}
	badFlow, _ := begin()
	path := "/connectors/google_drive/callback?code=code&state=" + str(badFlow, "state")
	r, _ := connectorRequest(t, f, "GET", path, "", nil, "")
	if r.StatusCode != 403 || tokens.Load() != 0 {
		t.Fatal("unbound browser reached provider", r.StatusCode)
	}
	flow, cookie := begin()
	launch, _ := url.Parse(str(flow, "auth_url"))
	r, _ = connectorRequest(t, f, "GET", strings.TrimPrefix(launch.RequestURI(), "/api/v1"), "", cookie, "")
	if r.StatusCode != 302 || !strings.HasPrefix(r.Header.Get("Location"), "https://accounts.google.com/o/oauth2/v2/auth?") {
		t.Fatal("incorrect launch", r.StatusCode, r.Header)
	}
	path = "/connectors/google_drive/callback?code=code&state=" + str(flow, "state")
	r, _ = connectorRequest(t, f, "GET", path, "", cookie, "text/html")
	if r.StatusCode != 200 || !strings.Contains(r.Header.Get("Content-Security-Policy"), "nonce-") {
		t.Fatal("callback did not complete", r.StatusCode)
	}
	r, _ = connectorRequest(t, f, "GET", path, "", cookie, "")
	if r.StatusCode != 400 || tokens.Load() != 1 {
		t.Fatal("OAuth state replayed", r.StatusCode)
	}
	var raw string
	if err := f.s.db.QueryRow(`SELECT credentials FROM cc_connector_accounts WHERE user_id=$1 AND provider='google_drive'`, user).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "private-") {
		t.Fatal("credentials stored in plaintext")
	}
	account, err := f.s.loadConnectorAccount(context.Background(), user, "google_drive")
	if err != nil || account.Credentials["refresh_token"] != "private-refresh" {
		t.Fatal(account, err)
	}
	flow, cookie = begin()
	f.call("POST", "/admin/integrations/connectors/google", admin, M{"values": M{"google_client_secret": "changed-secret"}}, 200)
	r, _ = connectorRequest(t, f, "GET", "/connectors/google_drive/callback?code=code&state="+str(flow, "state"), "", cookie, "")
	if r.StatusCode != 400 || tokens.Load() != 1 {
		t.Fatal("changed OAuth config was accepted")
	}
}

func TestConnectorRefreshSerializesAndDisconnectWinsLateCallback(t *testing.T) {
	f, admin, user := connectorFixture(t)
	generation := seedConnector(t, f, user, "box", true)
	var refreshes atomic.Int32
	f.s.connectorHTTP.Transport = modelTransport(func(r *http.Request) (*http.Response, error) {
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") != "fixture-refresh" {
			t.Error("rotated refresh token replayed")
		}
		refreshes.Add(1)
		return connectorResponse(200, M{"access_token": "rotated-access", "refresh_token": "rotated-refresh", "expires_in": 3600}, nil), nil
	})
	d, _ := f.s.connectorDefinition("box")
	v, _ := f.s.connectorConfig(context.Background(), "box")
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			a, err := f.s.connectorCredentials(context.Background(), user, d, v, generation)
			if err != nil || a.Credentials["refresh_token"] != "rotated-refresh" {
				t.Errorf("refresh: %v %v", a, err)
			}
		})
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatalf("refresh replayed %d times", refreshes.Load())
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var revoked atomic.Int32
	f.s.connectorHTTP.Transport = modelTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/oauth2/token":
			close(entered)
			<-release
			return connectorResponse(200, M{"access_token": "late-access", "refresh_token": "late-refresh"}, nil), nil
		case "/2.0/users/me":
			return connectorResponse(200, M{"name": "Fixture", "login": "fixture@example.test"}, nil), nil
		case "/oauth2/revoke":
			revoked.Add(1)
			return connectorResponse(200, M{}, nil), nil
		default:
			return connectorResponse(404, M{}, nil), nil
		}
	})
	r, flow := connectorRequest(t, f, "GET", "/connectors/box/auth", admin, nil, "")
	cookie := r.Cookies()[0]
	done := make(chan int, 1)
	go func() {
		response, _ := connectorRequest(t, f, "GET", "/connectors/box/callback?code=code&state="+str(flow, "state"), "", cookie, "")
		done <- response.StatusCode
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("callback not started")
	}
	f.call("POST", "/connectors/box/disconnect", admin, nil, 200)
	close(release)
	if status := <-done; status != 409 {
		t.Fatalf("late callback status %d", status)
	}
	a, err := f.s.loadConnectorAccount(context.Background(), user, "box")
	if err != nil || len(a.Credentials) != 0 || revoked.Load() != 2 {
		t.Fatal("grant resurrected", a, revoked.Load(), err)
	}
}

func TestConnectorAdminConfigScopeAndDownscopedPicker(t *testing.T) {
	f, admin, user := connectorFixture(t)
	member := f.account(admin, "member@example.test")
	f.call("POST", "/admin/integrations/connectors/google", member, M{"values": M{"google_client_id": "no"}}, 403)
	f.call("POST", "/admin/integrations/connectors/google", admin, M{"values": M{"google_api_key": "browser-key", "google_app_id": "app", "ignored": "no"}}, 200)
	f.call("POST", "/admin/integrations/connectors/google", admin, M{"values": M{"google_client_id": "changed-client"}}, 200)
	config := f.call("GET", "/pickers/config", member, nil, 200)
	if config["google_api_key"] != "browser-key" || config["google_client_id"] != "changed-client" || strings.Contains(string(jsonBytes(config)), "fixture-secret") {
		t.Fatal(config)
	}
	f.call("DELETE", "/admin/integrations/connectors/google", admin, nil, 200)
	config = f.call("GET", "/pickers/config", member, nil, 200)
	if config["google_client_id"] != "fixture-client" || config["google_enabled"] != false {
		t.Fatal(config)
	}
	f.call("GET", "/connectors/filevine/auth", member, nil, 403)
	f.call("POST", "/connectors/filevine/disconnect", member, nil, 403)
	seedConnector(t, f, user, "box", false)
	f.call("POST", "/connectors/box/sync", admin, M{}, 400)
	f.call("GET", "/pickers/box/token", member, nil, 409)
	f.s.connectorHTTP.Transport = modelTransport(func(r *http.Request) (*http.Response, error) {
		_ = r.ParseForm()
		if r.Form.Get("subject_token") != "fixture-access" || r.Form.Get("scope") != "base_picker item_download root_readonly" {
			t.Errorf("invalid downscope %v", r.Form)
		}
		return connectorResponse(200, M{"access_token": "picker-only", "expires_in": 600}, nil), nil
	})
	response, out := connectorRequest(t, f, "GET", "/pickers/box/token", admin, nil, "")
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" || out["access_token"] != "picker-only" {
		t.Fatal(out)
	}
}

func TestConnectorAllProvidersDownloadIntoGoodMem(t *testing.T) {
	f, admin, user := connectorFixture(t)
	for _, definition := range connectorDefinitions {
		t.Run(definition.ID, func(t *testing.T) {
			generation := seedConnector(t, f, user, definition.ID, false)
			file := connectorFile{ID: "file", Name: definition.ID + ".txt", MIME: "text/plain", Metadata: M{}}
			if definition.ID == "onedrive" {
				file.ID = "drv:shared-drive:file"
			}
			if definition.ID == "dropbox" {
				file.ID = "/notice 😀.txt"
			}
			var seen bool
			transport := modelTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer fixture-access" {
					t.Error("missing grant")
				}
				if definition.ID == "google_drive" && r.URL.Query().Get("fields") == "mimeType" {
					return connectorResponse(200, M{"mimeType": "text/plain"}, nil), nil
				}
				if definition.ID == "onedrive" && r.URL.Path != "/v1.0/drives/shared-drive/items/file/content" {
					t.Error(r.URL.Path)
				}
				if definition.ID == "filevine" && (r.Header.Get("x-fv-userid") != "fv-user" || r.Header.Get("x-fv-orgid") != "fv-org") {
					t.Error("missing firm session context")
				}
				if definition.ID == "dropbox" {
					arg := M{}
					_ = json.Unmarshal([]byte(r.Header.Get("Dropbox-API-Arg")), &arg)
					if r.Method != "POST" || arg["path"] != file.ID {
						t.Error("invalid Dropbox path")
					}
				}
				seen = true
				return connectorResponse(200, "Notice is 30 days.", nil), nil
			})
			f.s.connectorHTTP.Transport, f.s.downloadHTTP.Transport = transport, transport
			v, _ := f.s.connectorConfig(context.Background(), definition.Settings)
			same, err := f.s.importConnectorFile(context.Background(), user, definition, v, generation, file)
			if err != nil || same || !seen {
				t.Fatal(same, seen, err)
			}
			id := connectorDocumentID(user, definition.ID, file.ID, []byte("Notice is 30 days."))
			doc := f.call("GET", "/documents/"+id, admin, nil, 200)
			if doc["status"] != "indexed" || doc["source"] != definition.ID {
				t.Fatal(doc)
			}
			f.mu.Lock()
			stored := string(f.memories[str(doc, "memory_id")])
			f.mu.Unlock()
			if stored != "Notice is 30 days." {
				t.Fatal("original bytes not stored in GoodMem")
			}
		})
	}
}

func TestConnectorSyncPaginationCyclesFailuresAndIdempotence(t *testing.T) {
	f, admin, user := connectorFixture(t)
	seedConnector(t, f, user, "box", false)
	var downloads atomic.Int32
	f.s.connectorHTTP.Transport = modelTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/2.0/folders/root/items":
			if r.URL.Query().Get("usemarker") != "true" {
				t.Error("Box marker paging absent")
			}
			if r.URL.Query().Get("marker") == "next" {
				return connectorResponse(200, M{"entries": []M{{"id": "bad", "name": "Denied", "type": "folder"}}, "next_marker": "next"}, nil), nil
			}
			return connectorResponse(200, M{"entries": []M{{"id": "root", "name": "Cycle", "type": "folder"}, {"id": "file", "name": "notice.txt", "type": "file", "size": 18, "modified_at": "2026-10-29T12:00:00Z"}}, "next_marker": "next"}, nil), nil
		case "/2.0/folders/bad/items":
			return connectorResponse(403, M{}, nil), nil
		case "/2.0/files/file/content":
			downloads.Add(1)
			return connectorResponse(200, "Notice is 30 days.", nil), nil
		case "/2.0/users/me":
			return connectorResponse(200, M{"name": "Fixture", "login": "fixture@example.test"}, nil), nil
		default:
			t.Errorf("unexpected URL %s", r.URL)
			return connectorResponse(404, M{}, nil), nil
		}
	})
	for run := range 2 {
		started := f.call("POST", "/connectors/box/sync", admin, M{"folder_id": "root"}, 200)
		id := str(started, "sync_id")
		awaitChatFixture(t, runChatFixtureJob(t, f, id))
		job := f.call("GET", "/connectors/box/sync/"+id, admin, nil, 200)
		out := object(job["result"])
		if job["status"] != "completed" || number(out, "docs_processed", -1) != 1-run || number(out, "docs_skipped", -1) != run || number(out, "errors_count", -1) != 2 {
			t.Fatal(job)
		}
		f.call("GET", "/connectors/google_drive/sync/"+id, admin, nil, 404)
	}
	if downloads.Load() != 1 {
		t.Fatal("unchanged content downloaded again")
	}
	status := f.call("GET", "/connectors/box/status", admin, nil, 200)
	if status["last_sync"] == nil || number(status, "docs_indexed", 0) != 1 {
		t.Fatal(status)
	}
}

func TestPickerDurableImportsIDsSecretsAndCancellation(t *testing.T) {
	f, admin, user := connectorFixture(t)
	var requests atomic.Int32
	f.s.downloadHTTP.Transport = modelTransport(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		if strings.Contains(r.URL.Host, "sharepoint") && r.Header.Get("Authorization") != "" {
			t.Error("bearer leaked to signed download")
		}
		if strings.Contains(r.URL.Path, "bad") {
			return connectorResponse(200, "<html>Not a PDF</html>", nil), nil
		}
		return connectorResponse(200, "Notice is 30 days.", nil), nil
	})
	files := []struct {
		provider string
		file     M
	}{
		{"google", M{"id": "g", "name": "g.txt", "mimeType": "text/plain", "oauthToken": "private-token", "url": "https://drive.google.com/file/d/g/view"}},
		{"microsoft", M{"id": "ms", "name": "ms.txt", "accessToken": "private-token", "driveId": "drive", "downloadUrl": "https://tenant.sharepoint.com/signed?token=secret"}},
		{"box", M{"id": "b", "name": "b.txt", "accessToken": "private-token"}},
		{"dropbox", M{"name": "db.txt", "link": "https://dl.dropboxusercontent.com/file?token=secret"}},
	}
	for _, test := range files {
		t.Run(test.provider, func(t *testing.T) {
			started := f.call("POST", "/pickers/"+test.provider+"/import?async=true", admin, []M{test.file}, 202)
			id := str(started, "job_id")
			var raw []byte
			if err := f.s.db.QueryRow(`SELECT input FROM cc_jobs WHERE id=$1`, id).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "private-token") || strings.Contains(string(raw), "token=secret") {
				t.Fatal("picker credentials were not encrypted")
			}
			awaitChatFixture(t, runChatFixtureJob(t, f, id))
			job := f.call("GET", "/jobs/"+id, admin, nil, 200)
			out := object(job["result"])
			ids := stringsOf(out["document_ids"])
			if job["status"] != "completed" || number(out, "imported", 0) != 1 || len(ids) != 1 {
				t.Fatal(job)
			}
			doc, err := f.s.getObject(context.Background(), user, "document", ids[0])
			if err != nil || doc["status"] != "indexed" || strings.Contains(string(jsonBytes(doc)), "token=secret") {
				t.Fatal(doc, err)
			}
			if err := f.s.db.QueryRow(`SELECT input FROM cc_jobs WHERE id=$1`, id).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "encrypted_files") {
				t.Fatal("finished job retained picker tokens")
			}
		})
	}
	started := f.call("POST", "/pickers/box/import?async=true", admin, []M{files[2].file}, 202)
	id := str(started, "job_id")
	f.call("DELETE", "/jobs/"+id, admin, nil, 200)
	var encrypted bool
	if err := f.s.db.QueryRow(`SELECT input ? 'encrypted_files' FROM cc_jobs WHERE id=$1`, id).Scan(&encrypted); err != nil || encrypted {
		t.Fatal("cancelled job kept tokens", err)
	}
	out := f.call("POST", "/pickers/box/import", admin, []M{{"id": "bad", "name": "bad.pdf", "accessToken": "private-token"}}, 200)
	if number(out, "failed", 0) != 1 || number(out, "imported", 1) != 0 {
		t.Fatal(out)
	}
}

func TestConnectorDownloadRedirectsRetriesAndSize(t *testing.T) {
	s := &Server{cfg: Config{MaxUpload: 20}}
	var calls int
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	s.connectorHTTP, s.downloadHTTP = client, client
	client.Transport = modelTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return connectorResponse(302, "", http.Header{"Location": {"https://cdn.boxcloud.com/file"}}), nil
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Fv-Orgid") != "" {
			t.Error("provider headers crossed origins")
		}
		return connectorResponse(200, "document", nil), nil
	})
	b, err := s.connectorDownload(context.Background(), "GET", "https://api.box.com/file", http.Header{"Authorization": {"Bearer secret"}, "X-Fv-Orgid": {"org"}}, []string{"box.com", "boxcloud.com"}, "https://api.box.com")
	if err != nil || string(b) != "document" {
		t.Fatal(string(b), err)
	}
	calls = 0
	client.Transport = modelTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return connectorResponse(302, "", http.Header{"Location": {fmt.Sprintf("https://api.box.com/loop/%d", calls)}}), nil
	})
	_, err = s.connectorDownload(context.Background(), "GET", "https://api.box.com/file", nil, []string{"box.com"}, "")
	if err == nil || calls != 6 {
		t.Fatal("redirect bound", calls, err)
	}
	for _, declared := range []int64{-1, 30} {
		client.Transport = modelTransport(func(*http.Request) (*http.Response, error) {
			r := connectorResponse(200, strings.Repeat("x", 30), nil)
			r.ContentLength = declared
			return r, nil
		})
		if _, err = s.connectorDownload(context.Background(), "GET", "https://api.box.com/file", nil, []string{"box.com"}, ""); err == nil {
			t.Fatal("oversized stream accepted")
		}
	}
	calls = 0
	client.Transport = modelTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return connectorResponse(429, "", http.Header{"Retry-After": {"0"}}), nil
	})
	if _, err = s.connectorDownload(context.Background(), "GET", "https://api.box.com/file", nil, []string{"box.com"}, ""); err == nil || calls != 3 {
		t.Fatal("retry bound", calls, err)
	}
}

func TestConnectorCancellationRemovesLateGoodMemUpload(t *testing.T) {
	for _, mode := range []string{"picker", "sync", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			f, admin, user := connectorFixture(t)
			seedConnector(t, f, user, "box", false)
			transport := modelTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/items") {
					return connectorResponse(200, M{"entries": []M{{"id": "file", "name": "notice.txt", "type": "file"}}}, nil), nil
				}
				if strings.HasSuffix(r.URL.Path, "/revoke") {
					return connectorResponse(200, M{}, nil), nil
				}
				return connectorResponse(200, "Notice is 30 days.", nil), nil
			})
			f.s.connectorHTTP.Transport, f.s.downloadHTTP.Transport = transport, transport
			entered, release := make(chan struct{}), make(chan struct{})
			f.s.gm.Client.Transport = modelTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == "POST" && r.URL.Path == "/v1/memories" {
					close(entered)
					<-release
					// Model an upload that the remote service accepted despite the
					// caller cancelling: its late response must never resurrect it.
					r = r.Clone(context.Background())
				}
				return http.DefaultTransport.RoundTrip(r)
			})
			var job string
			if mode != "sync" {
				job = str(f.call("POST", "/pickers/box/import?async=true", admin, []M{{"id": "file", "name": "notice.txt", "accessToken": "fixture-access"}}, 202), "job_id")
			} else {
				job = str(f.call("POST", "/connectors/box/sync", admin, M{"folder_id": "root"}, 200), "sync_id")
			}
			done := runChatFixtureJob(t, f, job)
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("upload did not begin")
			}
			if mode == "shutdown" {
				f.s.cancel()
			} else if mode == "picker" {
				f.call("DELETE", "/jobs/"+job, admin, nil, 200)
			} else {
				f.call("POST", "/connectors/box/disconnect", admin, nil, 200)
			}
			close(release)
			awaitChatFixture(t, done)
			var count int
			if err := f.s.db.QueryRow(`SELECT count(*) FROM cc_objects WHERE kind='document' AND owner_id=$1`, user).Scan(&count); err != nil || count != 0 {
				t.Fatal("late upload retained", count, err)
			}
			out := f.call("GET", "/jobs/"+job, admin, nil, 200)
			want := "cancelled"
			if mode == "shutdown" {
				want = "failed"
			}
			if out["status"] != want || out["result"] != nil {
				t.Fatal(out)
			}
		})
	}
}

func TestGooglePickerNeverExposesGenerationKey(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "private-gemini-key")
	t.Setenv("GOOGLE_PICKER_API_KEY", "")
	if value := connectorEnvironment("https://app.example")["google_api_key"]; value != "" {
		t.Fatal("Gemini key became browser-visible", value)
	}
	t.Setenv("GOOGLE_PICKER_API_KEY", "restricted-browser-key")
	if value := connectorEnvironment("https://app.example")["google_api_key"]; value != "restricted-browser-key" {
		t.Fatal("missing picker key", value)
	}
}

func TestConnectorListingProtocolsMatchPython(t *testing.T) {
	f, _, user := connectorFixture(t)
	f.s.cfg.ConnectorSettings["clio_base_url"] = "https://eu.app.clio.com"
	raw, err := os.ReadFile("testdata/connectors.json")
	if err != nil {
		t.Fatal(err)
	}
	fixture := M{}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, tc := range maps(fixture["units"]) {
		if tc["kind"] != "listing" {
			continue
		}
		t.Run(str(tc, "provider")+"/"+str(tc, "folder"), func(t *testing.T) {
			d, _ := f.s.connectorDefinition(str(tc, "provider"))
			generation := seedConnector(t, f, user, d.ID, false)
			v, _ := f.s.connectorConfig(context.Background(), d.Settings)
			calls := 0
			f.s.connectorHTTP.Transport = modelTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Authorization") != "Bearer fixture-access" {
					t.Error("missing user-scoped grant")
				}
				switch d.ID {
				case "google_drive":
					if r.URL.Query().Get("supportsAllDrives") != "true" || r.URL.Query().Get("includeItemsFromAllDrives") != "true" {
						t.Error("shared drives omitted")
					}
				case "onedrive":
					if !strings.Contains(r.URL.Path, "/drives/drive/items/folder/children") {
						t.Error("drive scope lost")
					}
				case "dropbox":
					if r.Method != "POST" {
						t.Error("Dropbox listing must POST")
					}
				case "filevine":
					if r.URL.Query().Get("requestedPage") != "1" || r.Header.Get("x-fv-orgid") != "fv-org" {
						t.Error("Filevine session context or page lost")
					}
				case "imanage":
					if !strings.Contains(r.URL.Path, "/customers/customer/") {
						t.Error("customer context lost")
					}
				}
				return connectorResponse(200, tc["payload"], nil), nil
			})
			page, err := f.s.connectorList(context.Background(), user, d, v, generation, str(tc, "folder"), "")
			if err != nil || calls != 1 || len(page.Files) != len(maps(tc["expected"])) || page.Next != str(tc, "next") {
				t.Fatalf("listing: %+v calls=%d err=%v", page, calls, err)
			}
		})
	}
}

func TestConnectorSharePointTraversesEverySiteAndDrivePage(t *testing.T) {
	f, _, user := connectorFixture(t)
	generation := seedConnector(t, f, user, "onedrive", false)
	d, _ := f.s.connectorDefinition("onedrive")
	v, _ := f.s.connectorConfig(context.Background(), "microsoft")
	const base = "https://graph.microsoft.com/v1.0"
	f.s.connectorHTTP.Transport = modelTransport(func(r *http.Request) (*http.Response, error) {
		var payload M
		switch r.URL.Path {
		case "/v1.0/me/drive/root/children":
			payload = M{"value": []M{}}
		case "/v1.0/sites":
			payload = M{"value": []M{{"id": "s1", "displayName": "First"}}, "@odata.nextLink": base + "/sites/next"}
		case "/v1.0/sites/next":
			payload = M{"value": []M{{"id": "s2", "displayName": "Second"}}}
		case "/v1.0/sites/s1/drives":
			payload = M{"value": []M{{"id": "d1", "name": "Documents"}}, "@odata.nextLink": base + "/sites/s1/drives/next"}
		case "/v1.0/sites/s1/drives/next":
			payload = M{"value": []M{{"id": "d2", "name": "Archive"}}}
		case "/v1.0/sites/s2/drives":
			payload = M{"value": []M{{"id": "d3", "name": "Documents"}, {"id": "d1", "name": "Duplicate"}}}
		default:
			t.Error("unexpected path", r.URL)
			payload = M{}
		}
		return connectorResponse(200, payload, nil), nil
	})
	page, err := f.s.connectorList(context.Background(), user, d, v, generation, "", "")
	if err != nil || len(page.Files) != 3 || len(page.Errors) != 0 || page.Files[2].ID != "drive:d3" {
		t.Fatal(page, err)
	}
}

func TestConnectorProviderTokenContracts(t *testing.T) {
	f, _, _ := connectorFixture(t)
	for _, d := range connectorDefinitions {
		t.Run(d.ID, func(t *testing.T) {
			v, _ := f.s.connectorConfig(context.Background(), d.Settings)
			f.s.connectorHTTP.Transport = modelTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "POST" {
					t.Error("token exchange must POST")
				}
				if d.ID == "filevine" {
					var in M
					if json.NewDecoder(r.Body).Decode(&in) != nil {
						t.Error("invalid Filevine session JSON")
					}
					stamp := str(in, "apiTimestamp")
					if _, err := time.Parse(time.RFC3339, stamp); err != nil {
						t.Error(err)
					}
					expected := fmt.Sprintf("%x", md5.Sum([]byte("fixture-client/"+stamp+"/fixture-secret")))
					if in["mode"] != "key" || in["apiKey"] != "fixture-client" || in["apiHash"] != expected {
						t.Error("invalid Filevine handshake")
					}
					return connectorResponse(200, M{"accessToken": "session", "userId": M{"native": 1}, "orgId": M{"native": 2}}, nil), nil
				}
				_ = r.ParseForm()
				if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "old-refresh" {
					t.Error("invalid refresh grant")
				}
				if d.ID == "netdocuments" {
					if r.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("fixture-client:fixture-secret")) || r.Form.Get("client_secret") != "" {
						t.Error("NetDocuments requires Basic client authentication")
					}
				} else if r.Form.Get("client_id") != "fixture-client" || r.Form.Get("client_secret") != "fixture-secret" {
					t.Error("missing client credentials")
				}
				return connectorResponse(200, M{"access_token": "new-access", "refresh_token": "rotated", "expires_in": 3600}, nil), nil
			})
			creds, err := f.s.exchangeConnectorToken(context.Background(), d, v, "refresh_token", "old-refresh", M{"refresh_token": "old-refresh"})
			if err != nil {
				t.Fatal(err)
			}
			if d.ID == "filevine" {
				if creds["access_token"] != "session" || creds["user_id"] != "1" || creds["org_id"] != "2" {
					t.Fatal(creds)
				}
			} else if creds["refresh_token"] != "rotated" || creds["access_token"] != "new-access" {
				t.Fatal(creds)
			}
		})
	}
}

func TestConnectorAccountDeletionRevokesAndErasesFlows(t *testing.T) {
	f, admin, _ := connectorFixture(t)
	member := f.account(admin, "offboard@example.test")
	user := str(f.call("GET", "/auth/me", member, nil, 200), "id")
	seedConnector(t, f, user, "google_drive", false)
	f.call("GET", "/connectors/google_drive/auth", member, nil, 200)
	var revoked atomic.Bool
	f.s.connectorHTTP.Transport = modelTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/revoke" {
			t.Error("unexpected provider call", r.URL)
		}
		_ = r.ParseForm()
		if r.Form.Get("token") != "fixture-refresh" {
			t.Error("did not revoke refresh grant")
		}
		revoked.Store(true)
		return connectorResponse(200, M{}, nil), nil
	})
	f.call("DELETE", "/admin/users/"+user, admin, nil, 200)
	var remaining int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM cc_kv WHERE key LIKE 'connector-oauth:%' AND value->>'user_id'=$1`, user).Scan(&remaining); err != nil || remaining != 0 || !revoked.Load() {
		t.Fatal("offboarding retained grant/flow", remaining, revoked.Load(), err)
	}
	if err := f.s.db.QueryRow(`SELECT count(*) FROM cc_connector_accounts WHERE user_id=$1`, user).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("offboarding retained account", remaining, err)
	}
}
