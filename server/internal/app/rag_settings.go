package app

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

//go:embed rag_settings.json
var ragSettingsJSON []byte

//go:embed prompts.json
var promptsJSON []byte

func ragSettingSpecs() map[string]M {
	var m map[string]M
	if err := json.Unmarshal(ragSettingsJSON, &m); err != nil {
		panic(err)
	}
	return m
}
func defaultRAGSettings() M {
	m := M{}
	for k, spec := range ragSettingSpecs() {
		m[k] = spec["default"]
	}
	return m
}
func defaultPrompts() M {
	var m M
	if err := json.Unmarshal(promptsJSON, &m); err != nil {
		panic(err)
	}
	return m
}

func (s *Server) ragSettings(q request) (any, error) {
	if q.r.Method == "GET" {
		return s.userSettings(q.r.Context(), q.user.ID)
	}
	in, err := body(q)
	if err != nil {
		return nil, err
	}
	m, err := s.userSettings(q.r.Context(), q.user.ID)
	if err != nil {
		return nil, err
	}
	changed := []string{}
	for key, spec := range ragSettingSpecs() {
		value, present := in[key]
		if !present {
			continue
		}
		if key == "vector_db" {
			value = "goodmem"
		}
		if value == nil {
			if spec["default"] != nil {
				return nil, bad(key + " cannot be null")
			}
			m[key] = nil
			changed = append(changed, key)
			continue
		}
		switch def := spec["default"].(type) {
		case bool:
			if _, ok := value.(bool); !ok {
				return nil, bad(key + " must be a boolean")
			}
		case float64:
			n, ok := value.(float64)
			if !ok {
				return nil, bad(key + " must be a number")
			}
			if lower, ok := spec["ge"].(float64); ok && n < lower {
				return nil, bad(fmt.Sprintf("%s must be at least %g", key, lower))
			}
			if upper, ok := spec["le"].(float64); ok && n > upper {
				return nil, bad(fmt.Sprintf("%s must not exceed %g", key, upper))
			}
			if key != "temperature" && key != "similarity_threshold" && n != float64(int64(n)) {
				return nil, bad(key + " must be an integer")
			}
			_ = def
		default:
			text, ok := value.(string)
			if !ok {
				return nil, bad(key + " must be text")
			}
			length := utf8.RuneCountInString(text)
			if lower := number(spec, "min_length", 0); length < lower {
				return nil, bad(key + " is too short")
			}
			if upper := number(spec, "max_length", 0); upper > 0 && length > upper {
				return nil, bad(key + " is too long")
			}
		}
		m[key] = value
		changed = append(changed, key)
	}
	if number(m, "chunk_overlap", 0) >= number(m, "chunk_size", 512) {
		return nil, bad("Chunk overlap must be less than chunk size")
	}
	if id := str(m, "document_embedder_id"); id != "" && in["document_embedder_id"] != nil {
		catalog, err := s.documentEmbeddingCatalog(q.r.Context())
		if err != nil {
			return nil, err
		}
		found := false
		for _, model := range maps(catalog["models"]) {
			found = found || str(model, "id") == id
		}
		if !found {
			return nil, bad("Choose an enabled document embedding model")
		}
	}
	if err = s.kvSet(q.r.Context(), "settings:"+q.user.ID, m, 0); err != nil {
		return nil, err
	}
	slices.Sort(changed)
	s.audit(q.r.Context(), q.user.ID, "settings.update", "rag_settings", "", M{"changed_fields": changed})
	return m, nil
}

var userKeyTypes = []string{"openai", "anthropic", "google", "voyage", "cohere"}

func (s *Server) userKeys(q request) (any, error) {
	if q.r.Method == "GET" {
		status := M{}
		for _, kind := range userKeyTypes {
			key, err := s.providerKey(q.r.Context(), q.user.ID, kind, "")
			if err != nil {
				return nil, err
			}
			if strings.HasSuffix(q.r.URL.Path, "/masked") {
				var masked any
				if key != "" {
					masked = "••••" + key[max(0, len(key)-4):]
				}
				status[kind] = masked
			} else {
				status[kind+"_configured"] = key != ""
			}
		}
		var updated string
		err := s.kvGet(q.r.Context(), "credential-updated:"+q.user.ID, &updated)
		if err != nil && !notFound(err) {
			return nil, err
		}
		status["last_updated"] = nullable(updated)
		return status, nil
	}
	kind := q.r.PathValue("key_type")
	var m M
	var err error
	if q.r.Method == "POST" {
		m, err = body(q)
		if err != nil {
			return nil, err
		}
		kind = str(m, "key_type")
	}
	if !slices.Contains(userKeyTypes, kind) {
		return nil, bad("Unknown key type")
	}
	if q.r.Method == "DELETE" {
		if _, err = s.db.ExecContext(q.r.Context(), `DELETE FROM cc_kv WHERE key=$1`, "credential:"+q.user.ID+":"+kind); err != nil {
			return nil, err
		}
	} else {
		value := strings.Trim(strings.TrimSpace(str(m, "api_key")), "\"'“”‘’")
		value = strings.NewReplacer("–", "-", "—", "-", "−", "-").Replace(value)
		if len(value) < 10 || len(value) > 512 {
			return nil, bad("API keys must be between 10 and 512 characters")
		}
		for _, r := range value {
			if r < 33 || r > 126 {
				return nil, bad("API key contains invalid characters. Re-copy it directly from the provider.")
			}
		}
		encrypted, err := s.crypt(value, false)
		if err != nil {
			return nil, err
		}
		if err = s.kvSet(q.r.Context(), "credential:"+q.user.ID+":"+kind, encrypted, 0); err != nil {
			return nil, err
		}
	}
	if err = s.kvSet(q.r.Context(), "credential-updated:"+q.user.ID, nowString(), 0); err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "credential.update", "provider", kind, M{})
	return M{"status": "success", "message": "API key updated", "key_type": kind}, nil
}

func (s *Server) platformRoutes() {
	s.api("GET /api/v1/branding", true, s.branding)
	s.api("PUT /api/v1/branding", false, s.branding)
	s.api("POST /api/v1/branding/reset", false, s.branding)
	s.api("POST /api/v1/branding/logo", false, s.brandingLogo)
	s.api("GET /api/v1/branding/assets/{filename}", true, s.brandingAsset)
	s.api("GET /api/v1/settings/rag", false, s.ragSettings)
	s.api("GET /api/v1/settings/document-embedders", false, func(q request) (any, error) {
		return s.documentEmbeddingCatalog(q.r.Context())
	})
	s.api("PUT /api/v1/settings/rag", false, s.ragSettings)
	s.api("GET /api/v1/settings/prompts/defaults", false, func(q request) (any, error) { return defaultPrompts(), nil })
	s.api("GET /api/v1/user/keys/status", false, s.userKeys)
	s.api("GET /api/v1/user/keys/masked", false, s.userKeys)
	s.api("POST /api/v1/user/keys/save", false, s.userKeys)
	s.api("DELETE /api/v1/user/keys/{key_type}", false, s.userKeys)
}
