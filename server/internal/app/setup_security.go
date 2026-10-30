package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// GoodMem currently allows only humans to issue credentials. Provision a reserve
// of unassigned, powerless service principals offline; runtime can assign their
// live space grants, but cannot mint keys or widen their immutable ceilings.
type GoodMemSubject struct {
	PrincipalID string `json:"principal_id"`
	APIKey      string `json:"api_key"`
}

type GoodMemSecurity struct {
	EmbedderIDs  []string         `json:"embedder_ids"`
	RerankerID   string           `json:"reranker_id,omitempty"`
	Version      int              `json:"security_version"`
	ControllerID string           `json:"controller_id"`
	APIKey       string           `json:"api_key"`
	SpaceID      string           `json:"space_id"`
	BaseURL      string           `json:"base_url"`
	Subjects     []GoodMemSubject `json:"subjects"`
}

func savePrivateJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".goodmem-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func permission(operation, selector string) M { return M{"operation": operation, "selector": selector} }
func resourcePermission(operation, selector, kind, id string) M {
	r := permission(operation, selector)
	r["assignedResource"] = M{"kind": kind, "resourceId": id}
	return r
}

func SetupGoodMem(ctx context.Context, output string) error {
	adminFile := env("GOODMEM_PROVISIONING_FILE", filepath.Join(filepath.Dir(filepath.Dir(output)), "casecite-provisioning", "admin.json"))
	unlock, err := lockGoodMemSetup(adminFile)
	if err != nil {
		return err
	}
	defer unlock()
	a, _ := filepath.Abs(adminFile)
	b, _ := filepath.Abs(output)
	if a == b {
		return errors.New("provisioning and runtime files must be different")
	}
	// Refuse to silently leave a legacy root credential in a runtime mount.
	if raw, err := os.ReadFile(output); err == nil {
		var existing GoodMemSecurity
		if json.Unmarshal(raw, &existing) != nil || existing.Version != 1 {
			return errors.New("legacy GoodMem connection: move it to GOODMEM_PROVISIONING_FILE, unmount that directory from app, then rerun setup; run migrate-goodmem-security before starting an existing library")
		}
	}
	if err := setupGoodMemAdmin(ctx, adminFile); err != nil {
		return err
	}
	raw, err := os.ReadFile(adminFile)
	if err != nil {
		return err
	}
	state := M{}
	if err = json.Unmarshal(raw, &state); err != nil {
		return err
	}
	save := func() error { return savePrivateJSON(adminFile, state) }
	g := NewGoodMem(Config{GoodMemURL: str(state, "base_url"), GoodMemKey: str(state, "api_key")})
	if os.Getenv("GOODMEM_SETUP_MODELS") == "true" {
		if err := setupModels(ctx, g, state, save); err != nil {
			return err
		}
	}
	rerankerID := env("GOODMEM_RERANKER_ID", str(state, "reranker_id"))
	controller := object(state["controller"])
	if len(controller) == 0 {
		controller = M{"principal_id": uuid()}
		state["controller"] = controller
		if err = save(); err != nil {
			return err
		}
	}
	if err = ensureSetupIdentity(ctx, g, controller, "CaseCite controller "); err != nil {
		return err
	}
	controllerID := str(controller, "principal_id")
	// OWN content authority follows spaces created by this controller. Access
	// administration remains ownership-derived after the bootstrap grant is gone.
	rules := []M{}
	for _, op := range []string{"CREATE_SPACE", "READ_SPACE", "DELETE_SPACE", "READ_MEMORY", "CREATE_MEMORY", "DELETE_MEMORY", "LIST_MEMORY", "READ_SERVICE_IDENTITY", "DELETE_SERVICE_IDENTITY"} {
		rules = append(rules, permission(op, "OWN"))
	}
	for _, op := range []string{"OCR_DOCUMENT", "READ_EMBEDDER", "LIST_EMBEDDER"} {
		rules = append(rules, permission(op, "ANY"))
	}
	rules = append(rules, resourcePermission("READ_SPACE", "EXACT", "SPACE", str(state, "space_id")))
	embedders := map[string]bool{}
	if id := str(state, "voyage_embedder_id"); id != "" {
		embedders[id] = true
	}
	baseSpace, e := g.JSON(ctx, "GET", "/v1/spaces/"+str(state, "space_id"), nil)
	if e != nil {
		return e
	}
	for _, binding := range maps(baseSpace["spaceEmbedders"]) {
		embedders[str(binding, "embedderId")] = true
	}
	for _, id := range strings.Split(os.Getenv("GOODMEM_ALLOWED_EMBEDDER_IDS"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			embedders[id] = true
		}
	}
	ids := []string{}
	for id := range embedders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rules = append(rules, resourcePermission("EXECUTE_EMBEDDER", "EXACT", "EMBEDDER", id))
	}
	if id := rerankerID; id != "" {
		rules = append(rules, resourcePermission("EXECUTE_RERANKER", "EXACT", "RERANKER", id))
	}
	if err = setupScopedCredential(ctx, g, controller, rules, []M{permission("MANAGE_ACCESS", "ANY")}, save); err != nil {
		return err
	}
	count, err := strconv.Atoi(env("GOODMEM_IDENTITY_CAPACITY", "64"))
	if err != nil || count < 1 || count > 10000 {
		return errors.New("GOODMEM_IDENTITY_CAPACITY must be 1–10000 (total provisioned identities)")
	}
	subjects := maps(state["subjects"])
	for len(subjects) < count {
		subjects = append(subjects, M{"principal_id": uuid()})
	}
	state["subjects"] = subjects
	if err = save(); err != nil {
		return err
	}
	runtime := GoodMemSecurity{EmbedderIDs: ids, RerankerID: rerankerID, Version: 1, ControllerID: controllerID, APIKey: str(controller, "api_key"), SpaceID: str(state, "space_id"), BaseURL: str(state, "base_url")}
	for i := 0; i < len(subjects) || len(runtime.Subjects) < count; i++ {
		if i == len(subjects) {
			subjects = append(subjects, M{"principal_id": uuid()})
			state["subjects"] = subjects
			if err = save(); err != nil {
				return err
			}
		}
		subject := subjects[i]
		if subject["retired"] == true {
			continue
		}
		if str(subject, "api_key") != "" {
			actual, e := g.JSON(ctx, "GET", "/v1/service-identities/"+str(subject, "principal_id"), nil)
			if goodMemStatus(e, 404) || e == nil && actual["deletedAt"] != nil {
				subject["retired"] = true
				if err = save(); err != nil {
					return err
				}
				continue
			}
			if e != nil {
				return e
			}
		}
		if err = ensureSetupIdentity(ctx, g, subject, "CaseCite reader "); err != nil {
			return err
		}
		// ANY in the immutable ceiling allows future space memberships. These
		// temporary live grants exist only while offline setup issues the key;
		// they are durably journaled and revoked BEFORE publishing runtime data.
		ceiling := []M{permission("READ_SPACE", "ANY"), permission("READ_MEMORY", "ANY"), permission("LIST_MEMORY", "ANY")}
		subjectRules := []M{}
		if runtime.RerankerID != "" {
			subjectRules = append(subjectRules, resourcePermission("EXECUTE_RERANKER", "EXACT", "RERANKER", runtime.RerankerID))
		}
		if err = setupScopedCredential(ctx, g, subject, subjectRules, ceiling, save); err != nil {
			return err
		}
		if subject["transferred"] != true {
			_, err = g.JSON(ctx, "POST", "/v1/service-identities/"+str(subject, "principal_id")+":transferOwnership", M{"newOwnerId": controllerID})
			if err != nil {
				actual, e := g.JSON(ctx, "GET", "/v1/service-identities/"+str(subject, "principal_id"), nil)
				if e != nil || str(actual, "ownerPrincipalId") != controllerID {
					return err
				}
			}
			subject["transferred"] = true
			if err = save(); err != nil {
				return err
			}
		}
		runtime.Subjects = append(runtime.Subjects, GoodMemSubject{str(subject, "principal_id"), str(subject, "api_key")})
	}
	return savePrivateJSON(output, runtime)
}

func ensureSetupIdentity(ctx context.Context, g *GoodMem, state M, prefix string) error {
	id := str(state, "principal_id")
	_, err := g.JSON(ctx, "GET", "/v1/service-identities/"+id, nil)
	if goodMemStatus(err, 404) {
		_, err = g.JSON(ctx, "POST", "/v1/service-identities", M{"serviceIdentityId": id, "displayName": prefix + id})
		if goodMemStatus(err, 409) {
			_, err = g.JSON(ctx, "GET", "/v1/service-identities/"+id, nil)
		}
	}
	return err
}

func goodMemStatus(err error, status int) bool {
	var e *GoodMemError
	return errors.As(err, &e) && e.Status == status
}

func setupScopedCredential(ctx context.Context, g *GoodMem, state M, permanent, temporary []M, save func() error) error {
	ceiling := append(append([]M{}, permanent...), temporary...)
	fingerprint := digest(string(jsonBytes(ceiling)))
	// Resolve a create whose request or response may still be in flight, then
	// revoke its immutable ID. A DELETE-404 alone would permit a late ANY grant.
	if err := finishSetupTemporaryGrants(ctx, g, state); err != nil {
		return err
	}
	grants := object(state["permanent_grants_v1"])
	state["permanent_grants_v1"] = grants
	desired := map[string]bool{}
	for _, rule := range permanent {
		hash := digest(string(jsonBytes(rule)))
		desired[hash] = true
		id := str(grants, hash)
		if id == "" {
			id = uuid()
			grants[hash] = id
			if err := save(); err != nil {
				return err
			}
		}
		_, err := g.JSON(ctx, "POST", "/v1/access-policy/grants", M{"grantId": id, "audience": M{"principalId": str(state, "principal_id")}, "rule": rule})
		if err != nil && !goodMemStatus(err, 409) {
			return fmt.Errorf("runtime grant %s: %w", str(rule, "operation"), err)
		}
	}
	for hash, value := range grants {
		if !desired[hash] {
			id, _ := value.(string)
			if err := g.DeletePolicy(ctx, "grants", id); err != nil {
				return err
			}
			delete(grants, hash)
		}
	}
	if str(state, "api_key") != "" && str(state, "ceiling_hash") == fingerprint {
		return save()
	}
	// Unknown one-time responses and changed ceilings require a fresh key.
	if id := str(state, "key_id"); id != "" {
		_, err := g.JSON(ctx, "DELETE", "/v1/apikeys/"+id, nil)
		if err != nil && !goodMemStatus(err, 404) {
			return err
		}
	}
	delete(state, "api_key")
	state["ceiling_hash"] = fingerprint
	if err := save(); err != nil {
		return err
	}
	for _, rule := range temporary {
		id := uuid()
		state["temporary_grants"] = append(maps(state["temporary_grants"]), M{"id": id, "rule": rule})
		if err := save(); err != nil {
			return err
		}
		if _, err := g.JSON(ctx, "POST", "/v1/access-policy/grants", M{"grantId": id, "audience": M{"principalId": str(state, "principal_id")}, "rule": rule}); err != nil {
			return err
		}
	}
	state["key_id"] = uuid()
	if err := save(); err != nil {
		return err
	}
	m, err := g.JSON(ctx, "POST", "/v1/apikeys", M{"apiKeyId": str(state, "key_id"), "subjectPrincipalId": str(state, "principal_id"), "authorityMode": "SCOPED", "ceiling": ceiling, "labels": M{"app": "casecite"}})
	if err != nil {
		return err
	}
	state["api_key"] = str(m, "rawApiKey")
	if str(state, "api_key") == "" {
		return errors.New("GoodMem omitted scoped API key")
	}
	if err = save(); err != nil {
		return err
	}
	if err = finishSetupTemporaryGrants(ctx, g, state); err != nil {
		return err
	}
	return save()
}

func finishSetupTemporaryGrants(ctx context.Context, g *GoodMem, state M) error {
	var grants []M
	raw, err := json.Marshal(state["temporary_grants"])
	if err != nil {
		return fmt.Errorf("invalid temporary grant journal: %w", err)
	}
	if err = json.Unmarshal(raw, &grants); err != nil {
		return fmt.Errorf("invalid temporary grant journal; repair provisioning state before continuing: %w", err)
	}
	// Validate the whole journal before changing anything. Unknown formats must
	// never be silently discarded while a broad provisioning grant may be live.
	for _, grant := range grants {
		rule := object(grant["rule"])
		if str(grant, "id") == "" || str(state, "principal_id") == "" || str(rule, "operation") == "" || str(rule, "selector") == "" {
			return errors.New("invalid temporary grant journal: missing grant ID, principal or rule")
		}
	}
	for _, grant := range grants {
		id := str(grant, "id")
		payload := M{"grantId": id, "audience": M{"principalId": str(state, "principal_id")}, "rule": grant["rule"]}
		if err := g.finalizePolicyCreate(ctx, "grants", id, payload); err != nil {
			return err
		}
		if err := g.DeletePolicy(ctx, "grants", id); err != nil {
			return err
		}
	}
	state["temporary_grants"] = []M{}
	return nil
}

func (g *GoodMem) DeletePolicy(ctx context.Context, kind, id string) error {
	_, err := g.JSON(ctx, "DELETE", "/v1/access-policy/"+kind+"/"+id, nil)
	if goodMemStatus(err, 404) {
		return nil
	}
	return err
}
