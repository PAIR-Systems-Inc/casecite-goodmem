package app

import (
	"context"
	_ "embed"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
)

//go:embed permissions.json
var permissionCatalogJSON []byte

//go:embed route_permissions.json
var routePermissionsJSON []byte

var pathParameter = regexp.MustCompile(`\{[^}]+\}`)

func routePermissions(pattern string) []string {
	var contracts map[string][]string
	if err := json.Unmarshal(routePermissionsJSON, &contracts); err != nil {
		panic(err)
	}
	if keys, ok := contracts[pathParameter.ReplaceAllString(pattern, "{}")]; ok {
		return keys
	}
	// Compatibility routes retained from the first Go implementation.
	switch pattern {
	case "POST /api/v1/workflows/{id}/{action}":
		return []string{"contracts.use"}
	case "POST /api/v1/documents", "PATCH /api/v1/documents/{id}":
		return []string{"documents.upload"}
	case "GET /api/v1/documents/{id}/pages", "GET /api/v1/documents/{id}/pages/{index}/image", "GET /api/v1/documents/{id}/file":
		return []string{"documents.view"}
	case "GET /api/v1/users", "POST /api/v1/users", "PATCH /api/v1/users/{id}":
		return []string{"admin.users"}
	case "GET /api/v1/audit":
		return []string{"admin.audit"}
	}
	return nil
}

var roles = []string{"admin", "attorney", "paralegal", "viewer"}

func permissionCatalog() []M {
	var out []M
	if err := json.Unmarshal(permissionCatalogJSON, &out); err != nil {
		panic(err)
	}
	return out
}

func defaultPermissions(role string) []string {
	keys := []string{}
	for _, p := range permissionCatalog() {
		key := str(p, "key")
		if role == "admin" || (role == "attorney" && !strings.HasPrefix(key, "admin.")) ||
			(role == "paralegal" && !strings.HasPrefix(key, "admin.") && key != "matters.manage") ||
			(role == "viewer" && slices.Contains([]string{"chat.use", "documents.view", "tools.research", "judge_intel.use"}, key)) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

func (s *Server) rolePermissions(ctx context.Context, role string) ([]string, error) {
	var keys []string
	err := s.kvGet(ctx, "permissions:"+role, &keys)
	if notFound(err) {
		keys, err = defaultPermissions(role), nil
	}
	if err != nil {
		return nil, err
	}
	if role == "admin" && !slices.Contains(keys, "admin.users") {
		keys = append(keys, "admin.users")
	}
	return keys, nil
}

func permit(q request, key string) error {
	if !slices.Contains(q.user.Permissions, key) {
		return fail(403, "Your role does not include permission: "+key+". Ask an administrator to grant it in Settings > Users.")
	}
	return nil
}

func (s *Server) permissionMatrix(q request) (any, error) {
	if err := permit(q, "admin.users"); err != nil {
		return nil, err
	}
	if q.r.Method == "PUT" {
		role := q.r.PathValue("role")
		if !slices.Contains(roles, role) {
			return nil, bad("Unknown role")
		}
		m, err := body(q)
		if err != nil {
			return nil, err
		}
		keys := stringsOf(m["permissions"])
		if _, ok := m["permissions"].([]any); !ok {
			return nil, bad("permissions must be an array")
		}
		for _, k := range keys {
			if !slices.Contains(defaultPermissions("admin"), k) {
				return nil, bad("Unknown permission: " + k)
			}
		}
		if role == "admin" && !slices.Contains(keys, "admin.users") {
			keys = append(keys, "admin.users")
		}
		slices.Sort(keys)
		keys = slices.Compact(keys)
		if err := s.kvSet(q.r.Context(), "permissions:"+role, keys, 0); err != nil {
			return nil, err
		}
		s.audit(q.r.Context(), q.user.ID, "permissions.update", "role", role, M{"permissions": keys})
	} else if q.r.Method == "POST" {
		if _, err := s.db.ExecContext(q.r.Context(), `DELETE FROM cc_kv WHERE key LIKE 'permissions:%'`); err != nil {
			return nil, err
		}
		s.audit(q.r.Context(), q.user.ID, "permissions.reset", "role", "", M{})
	}
	matrix, defaults := M{}, M{}
	customized := []string{}
	for _, role := range roles {
		keys, err := s.rolePermissions(q.r.Context(), role)
		if err != nil {
			return nil, err
		}
		slices.Sort(keys)
		matrix[role] = keys
		defaults[role] = defaultPermissions(role)
		if !slices.Equal(keys, defaultPermissions(role)) {
			customized = append(customized, role)
		}
	}
	return M{"permissions": permissionCatalog(), "roles": roles, "assigned": matrix, "defaults": defaults, "customized": customized, "locked": M{"admin": []string{"admin.users"}}}, nil
}
