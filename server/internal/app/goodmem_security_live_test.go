package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Requires the pinned real GoodMem integration stack. No authorization mock:
// direct upstream requests intentionally omit or contradict CaseCite filters.
func TestGoodMemSecurityLive(t *testing.T) {
	file := os.Getenv("TEST_GOODMEM_SECURITY_CONFIG")
	if file == "" {
		t.Skip("set TEST_GOODMEM_SECURITY_CONFIG for the real GoodMem security contract")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var security GoodMemSecurity
	if err = json.Unmarshal(raw, &security); err != nil {
		t.Fatal(err)
	}
	adminRaw, err := os.ReadFile(os.Getenv("TEST_GOODMEM_PROVISIONING_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	state := M{}
	if err = json.Unmarshal(adminRaw, &state); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(str(state, "api_key"))) {
		t.Fatal("root credential leaked into runtime configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	base := env("TEST_GOODMEM_BASE_URL", security.BaseURL)
	root := NewGoodMem(Config{GoodMemURL: base, GoodMemKey: str(state, "api_key")})
	embedder, err := root.JSON(ctx, "POST", "/v1/embedders", M{"displayName": "CaseCite security alternate " + uuid(), "providerType": "OPENAI", "endpointUrl": "http://provider:8080/v1", "modelIdentifier": "test-security-embedding-" + uuid(), "credentials": M{"kind": "CREDENTIAL_KIND_API_KEY", "apiKey": M{"inlineSecret": "test-provider-only"}}, "dimensionality": 16, "distributionType": "DENSE", "supportedModalities": []string{"TEXT"}, "labels": M{"casecite": "enabled"}})
	if err != nil {
		t.Fatal(err)
	}
	provisioning := filepath.Join(t.TempDir(), "provisioning.json")
	output := filepath.Join(t.TempDir(), "runtime.json")
	if err = savePrivateJSON(provisioning, M{"api_key": str(state, "api_key"), "base_url": base, "space_id": security.SpaceID, "embedder_id": str(state, "embedder_id")}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOODMEM_BASE_URL", base)
	t.Setenv("GOODMEM_PROVISIONING_FILE", provisioning)
	t.Setenv("GOODMEM_IDENTITY_CAPACITY", "8")
	t.Setenv("GOODMEM_ALLOWED_EMBEDDER_IDS", str(embedder, "embedderId"))
	if err = SetupGoodMem(ctx, output); err != nil {
		t.Fatal(err)
	}
	if err = SetupGoodMem(ctx, output); err != nil {
		t.Fatal("idempotent setup", err)
	}
	raw, err = os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &security); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(str(state, "api_key"))) {
		t.Fatal("provisioner leaked root key")
	}
	f := newFixture(t)
	f.s.log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	f.s.cfg.GoodMemSecurity = &security
	f.s.cfg.GoodMemURL = base
	f.s.cfg.GoodMemKey = security.APIKey
	f.s.cfg.GoodMemSpace = security.SpaceID
	f.s.gm = NewGoodMem(f.s.cfg)
	f.s.gm.ReadKey = f.s.goodMemReadKey
	outage := &policyOutageTransport{base: http.DefaultTransport}
	f.s.gm.Client.Transport = outage
	if err = f.s.InitializeGoodMemSecurity(ctx); err != nil {
		t.Fatal(err)
	}
	owner := f.admin()
	member := f.account(owner, "member@example.test")
	stranger := f.account(owner, "stranger@example.test")
	uid := func(access string) string { return str(f.call("GET", "/auth/me", access, nil, 200), "id") }
	ownerID, memberID, strangerID := uid(owner), uid(member), uid(stranger)
	reader := func(user string) *GoodMem {
		key, err := f.s.goodMemReadKey(goodMemUserContext(ctx, user))
		if err != nil {
			t.Fatal(err)
		}
		g := NewGoodMem(f.s.cfg)
		g.Key = key
		return g
	}
	matter := f.call("POST", "/matters", owner, M{"name": "Confidential litigation"}, 200)
	f.call("POST", "/matters/"+str(matter, "id")+"/members", owner, M{"email": "member@example.test"}, 200)
	private := f.upload(owner, "", "private.txt", "Owner private memorandum. Notice is 30 days.")
	shared := f.upload(owner, str(matter, "id"), "shared.txt", "Shared matter memorandum. Notice is 45 days.")
	memberDoc := f.upload(member, str(matter, "id"), "member.txt", "Member original work product.")
	pdf, err := os.ReadFile("testdata/scanned_notice.pdf")
	if err != nil {
		t.Fatal(err)
	}
	scan := f.upload(owner, str(matter, "id"), "scan.pdf", string(pdf))
	f.s.StartWorkers()
	scan, err = f.s.waitDocumentIndexed(ctx, ownerID, str(scan, "id"))
	if err != nil {
		t.Fatal("OCR", err)
	}
	// Alternate embeddings get another space with exactly the same ACL.
	profile, err := f.s.resolveIndexProfile(ctx, M{"document_embedder_id": str(embedder, "embedderId")})
	if err != nil {
		t.Fatal(err)
	}
	outage.failSpaceCreate.Store(true)
	if _, err = f.s.secureDocumentProfile(ctx, str(scan, "id"), profile); err == nil {
		t.Fatal("space creation outage was ignored")
	}
	outage.failSpaceCreate.Store(false)
	if err = f.s.InitializeGoodMemSecurity(ctx); err != nil {
		t.Fatal("failed space creation poisoned startup", err)
	}
	profile, err = f.s.secureDocumentProfile(ctx, str(scan, "id"), profile)
	if err != nil {
		t.Fatal(err)
	}
	alternate := uuid()
	if _, err = f.s.gm.withIndexProfile(profile).CreateTextIndex(ctx, alternate, str(scan, "id"), str(scan, "memory_id"), "scan.pdf", []byte("Alternate OCR representation of the confidential notice."), "goodmem_ocr"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.gm.Wait(goodMemUserContext(ctx, ownerID), alternate); err != nil {
		t.Fatal(err)
	}
	ownerGM, memberGM, strangerGM := reader(ownerID), reader(memberID), reader(strangerID)
	hits, err := memberGM.withIndexProfile(object(shared["index_profile"])).SearchWithRerank(ctx, "notice", []string{str(shared, "memory_id")}, 5, false)
	if err != nil || len(hits) == 0 {
		t.Fatal("authorized semantic retrieval failed", err)
	}
	deny := func(g *GoodMem, path string) {
		t.Helper()
		_, err := g.JSON(ctx, "GET", path, nil)
		if !goodMemStatus(err, 403) && !goodMemStatus(err, 401) {
			t.Fatalf("GoodMem did not deny %s: %v", path, err)
		}
	}
	allow := func(g *GoodMem, memory string) {
		t.Helper()
		if _, err := g.Content(ctx, memory); err != nil {
			t.Fatal("authorized original/derived read denied", err)
		}
	}
	for _, memory := range []string{str(shared, "memory_id"), str(scan, "memory_id"), str(scan, "index_memory_id"), alternate} {
		if memory == "" {
			t.Fatal("missing document representation")
		}
		allow(memberGM, memory)
		deny(strangerGM, "/v1/memories/"+memory+"/content")
	}
	deny(memberGM, "/v1/memories/"+str(private, "memory_id")+"/content")
	deny(strangerGM, "/v1/memories/"+str(scan, "memory_id")+"/pages")
	deny(strangerGM, "/v1/memories/"+str(scan, "memory_id")+"/pages/0/image")
	page, err := memberGM.Page(ctx, str(scan, "memory_id"), "0", nil)
	if err != nil {
		t.Fatal(err)
	}
	page.Body.Close()
	// Retrieval is denied at the space boundary, including multi-space queries
	// and a filter that incorrectly names an otherwise authorized document.
	retrieveDenied := func(g *GoodMem, spaces []M) {
		t.Helper()
		response, err := g.do(ctx, "POST", "/v1/memories:retrieve", bytes.NewReader(jsonBytes(M{"message": "notice", "spaceKeys": spaces, "requestedSize": 10, "fetchMemory": true})), "application/json")
		if goodMemStatus(err, 403) || goodMemStatus(err, 401) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		b, _ := io.ReadAll(response.Body)
		if !strings.Contains(string(b), "PERMISSION_DENIED") && !strings.Contains(string(b), `"code":7`) {
			t.Fatalf("retrieval was not denied by GoodMem: %s", b)
		}
		if strings.Contains(string(b), "chunkText") {
			t.Fatal("denied retrieval leaked chunks")
		}
	}
	for _, space := range []string{str(object(shared["index_profile"]), "space_id"), str(object(scan["index_profile"]), "space_id"), str(profile, "space_id")} {
		retrieveDenied(strangerGM, []M{{"spaceId": space}})
		retrieveDenied(memberGM, []M{{"spaceId": str(object(private["index_profile"]), "space_id"), "filter": "CAST(val('$.casecite_memory_id') AS TEXT) IN ('" + str(shared, "memory_id") + "')"}, {"spaceId": space}})
	}
	// Controller has OWN content authority, not root/instance-wide reads.
	foreign, err := root.JSON(ctx, "POST", "/v1/spaces", M{"name": "Outside CaseCite " + uuid(), "defaultChunkingConfig": indexChunking(nil), "spaceEmbedders": []M{{"embedderId": str(state, "embedder_id"), "defaultRetrievalWeight": 1}}})
	if err != nil {
		t.Fatal(err)
	}
	foreignGM := *root
	foreignGM.Space = str(foreign, "spaceId")
	foreignMemory := uuid()
	if _, err = foreignGM.Create(ctx, foreignMemory, uuid(), "foreign.txt", "text/plain", []byte("Unrelated private application")); err != nil {
		t.Fatal(err)
	}
	controller := NewGoodMem(f.s.cfg)
	deny(controller, "/v1/memories/"+foreignMemory+"/content")
	if _, err = controller.JSON(ctx, "POST", "/v1/apikeys", M{}); !goodMemStatus(err, 403) && !goodMemStatus(err, 412) {
		t.Fatal("runtime controller could issue keys", err)
	}
	if _, err = strangerGM.JSON(ctx, "POST", "/v1/access-policy/role-assignments", M{"principalId": security.Subjects[0].PrincipalID, "role": "SPACE_VIEWER", "assignedResource": M{"kind": "SPACE", "resourceId": str(object(private["index_profile"]), "space_id")}}); !goodMemStatus(err, 403) {
		t.Fatal("reader could change access", err)
	}
	// Simulate a crashed/late grant request which has a durable local ID but
	// has not reached GoodMem yet. Removal must tombstone that ID first.
	sharedSpace := str(object(shared["index_profile"]), "space_id")
	var memberPrincipal, oldAssignment string
	if err = f.s.db.QueryRow(`SELECT principal_id FROM cc_goodmem_subjects WHERE user_id=$1 AND NOT retired`, memberID).Scan(&memberPrincipal); err != nil {
		t.Fatal(err)
	}
	if err = f.s.db.QueryRow(`SELECT assignment_id FROM cc_goodmem_access WHERE space_id=$1 AND principal_id=$2`, sharedSpace, memberPrincipal).Scan(&oldAssignment); err != nil {
		t.Fatal(err)
	}
	if err = root.DeletePolicy(ctx, "role-assignments", oldAssignment); err != nil {
		t.Fatal(err)
	}
	lateAssignment := uuid()
	if _, err = f.s.db.Exec(`UPDATE cc_goodmem_access SET assignment_id=$3,state='pending' WHERE space_id=$1 AND principal_id=$2`, sharedSpace, memberPrincipal, lateAssignment); err != nil {
		t.Fatal(err)
	}
	outage.failRevoke.Store(true)
	f.call("DELETE", "/matters/"+str(matter, "id")+"/members/"+memberID, owner, nil, 502)
	var stillMember bool
	if err = f.s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM cc_matter_members WHERE matter_id=$1 AND user_id=$2)`, str(matter, "id"), memberID).Scan(&stillMember); err != nil || !stillMember {
		t.Fatal("revocation outage incorrectly committed membership removal", err)
	}
	outage.failRevoke.Store(false)
	f.call("DELETE", "/matters/"+str(matter, "id")+"/members/"+memberID, owner, nil, 200)
	if _, err = controller.JSON(ctx, "POST", "/v1/access-policy/role-assignments", M{"roleAssignmentId": lateAssignment, "principalId": memberPrincipal, "role": "SPACE_VIEWER", "assignedResource": M{"kind": "SPACE", "resourceId": sharedSpace}}); !goodMemStatus(err, 409) {
		t.Fatal("late role grant could revive removed membership", err)
	}
	for _, memory := range []string{str(shared, "memory_id"), str(scan, "memory_id"), str(scan, "index_memory_id"), alternate} {
		deny(memberGM, "/v1/memories/"+memory+"/content")
	}
	allow(memberGM, str(memberDoc, "memory_id")) // owner retains only their own document
	late := uuid()
	if _, err = f.s.gm.withIndexProfile(profile).CreateTextIndex(ctx, late, str(scan, "id"), str(scan, "memory_id"), "scan.pdf", []byte("A delayed OCR result after revocation"), "goodmem_ocr"); err != nil {
		t.Fatal(err)
	}
	deny(memberGM, "/v1/memories/"+late+"/content")
	f.call("PATCH", "/documents/"+str(shared, "id"), owner, M{"matter_id": nil}, 200)
	f.call("POST", "/matters/"+str(matter, "id")+"/members", owner, M{"email": "member@example.test"}, 200)
	deny(memberGM, "/v1/memories/"+str(shared, "memory_id")+"/content")
	allow(memberGM, str(scan, "memory_id"))
	f.call("PATCH", "/admin/users/"+memberID+"/active", owner, M{"is_active": false}, 200)
	deny(memberGM, "/v1/memories/"+str(memberDoc, "memory_id")+"/content")
	allow(ownerGM, str(private, "memory_id"))
	f.call("DELETE", "/matters/"+str(matter, "id"), owner, nil, 200)
	f.call("DELETE", "/documents/"+str(private, "id"), owner, nil, 200)
	if _, err = ownerGM.Content(ctx, str(private, "memory_id")); err == nil {
		t.Fatal("deleted original remains readable")
	}
	if _, err = f.s.gm.Content(ctx, str(scan, "memory_id")); err == nil {
		t.Fatal("unbound application read fell back to controller")
	}
	// The offline upgrade preserves the original byte-for-byte and the stable
	// application document ID, then removes both legacy shared-space memories.
	legacyID, legacyOriginal, legacyIndex := uuid(), uuid(), uuid()
	legacyClient := *root
	legacyClient.Space = security.SpaceID
	if _, err = legacyClient.Create(ctx, legacyOriginal, legacyID, "legacy.pdf", "application/pdf", pdf); err != nil {
		t.Fatal(err)
	}
	if _, err = legacyClient.CreateTextIndex(ctx, legacyIndex, legacyID, legacyOriginal, "legacy.pdf", []byte("Migrated legacy notice OCR text"), "goodmem_ocr"); err != nil {
		t.Fatal(err)
	}
	if _, err = root.Wait(ctx, legacyIndex); err != nil {
		t.Fatal(err)
	}
	legacy := M{"memory_id": legacyOriginal, "index_memory_id": legacyIndex, "filename": "legacy.pdf", "content_type": "application/pdf", "sha256": digest(string(pdf)), "status": "indexed", "ocr_required": true, "index_profile": M{"space_id": security.SpaceID}}
	if err = f.s.saveObject(ctx, ownerID, "document", legacyID, "", legacy); err != nil {
		t.Fatal(err)
	}
	if err = f.s.InitializeGoodMemSecurity(ctx); err == nil {
		t.Fatal("startup accepted an unisolated legacy library")
	}
	if err = f.s.MigrateGoodMemSecurity(ctx, provisioning); err != nil {
		t.Fatal("legacy migration", err)
	}
	if err = f.s.MigrateGoodMemSecurity(ctx, provisioning); err != nil {
		t.Fatal("migration replay", err)
	}
	migrated, err := f.s.getObject(ctx, ownerID, "document", legacyID)
	if err != nil {
		t.Fatal(err)
	}
	content, err := ownerGM.Content(ctx, str(migrated, "memory_id"))
	if err != nil || !bytes.Equal(content, pdf) {
		t.Fatal("migration changed or lost original", err)
	}
	for _, memory := range []string{str(migrated, "memory_id"), str(migrated, "index_memory_id")} {
		deny(strangerGM, "/v1/memories/"+memory+"/content")
	}
	for _, memory := range []string{legacyOriginal, legacyIndex} {
		if _, err = root.Memory(ctx, memory); !goodMemStatus(err, 404) {
			t.Fatal("legacy copy survived successful cutover", err)
		}
	}
	if err = f.s.InitializeGoodMemSecurity(ctx); err != nil {
		t.Fatal("restart after migration", err)
	}
	lateGrant := uuid()
	pending := M{"principal_id": security.Subjects[len(security.Subjects)-1].PrincipalID, "temporary_grants": []M{{"id": lateGrant, "rule": permission("READ_MEMORY", "ANY")}}}
	if err = finishSetupTemporaryGrants(ctx, root, pending); err != nil {
		t.Fatal(err)
	}
	if _, err = root.JSON(ctx, "POST", "/v1/access-policy/grants", M{"grantId": lateGrant, "audience": M{"principalId": pending["principal_id"]}, "rule": permission("READ_MEMORY", "ANY")}); !goodMemStatus(err, 409) {
		t.Fatal("late provisioning grant revived broad authority", err)
	}
	// Replenishment must tolerate retired principals, preserve active reader
	// credentials and replace the reserve without ever reactivating old keys.
	if err = SetupGoodMem(ctx, output); err != nil {
		t.Fatal("replenishment after retirement", err)
	}
	deny(memberGM, "/v1/memories/"+str(memberDoc, "memory_id")+"/content")
}

type policyOutageTransport struct {
	base            http.RoundTripper
	failRevoke      atomic.Bool
	failSpaceCreate atomic.Bool
}

func (p *policyOutageTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if p.failRevoke.Load() && r.Method == "DELETE" && strings.Contains(r.URL.Path, "/role-assignments/") || p.failSpaceCreate.Load() && r.Method == "POST" && r.URL.Path == "/v1/spaces" {
		return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("test policy outage")), Request: r}, nil
	}
	return p.base.RoundTrip(r)
}
