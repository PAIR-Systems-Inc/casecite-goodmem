package app

import (
	"context"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func (s *Server) userSettings(ctx context.Context, user string) (M, error) {
	runtime, err := s.llmRuntime(ctx)
	if err != nil {
		return nil, err
	}
	return s.userSettingsWithRuntime(ctx, user, runtime)
}
func (s *Server) userSettingsWithRuntime(ctx context.Context, user string, runtime llmRuntime) (M, error) {
	if snapshot, ok := ctx.Value(chatSettingsKey{}).(M); ok {
		return snapshot, nil
	}
	m := defaultRAGSettings()
	stored := M{}
	err := s.kvGet(ctx, "settings:"+user, &stored)
	if err != nil && !notFound(err) {
		return nil, err
	}
	for k, v := range stored {
		m[k] = v
	}
	if str(stored, "llm_model") == "" {
		m["llm_model"] = runtime.ChatModel
	}
	if str(stored, "embedding_model") == "" && s.cfg.EmbeddingModel != "" {
		m["embedding_model"] = s.cfg.EmbeddingModel
	}
	if str(m, "embedding_model") == "cohere-embed-v3" {
		m["embedding_model"] = "embed-english-v3.0"
	}
	m["vector_db"] = "goodmem"
	return m, nil
}
func (s *Server) settings(q request) (any, error) {
	if q.r.Method == "PUT" {
		in, err := body(q)
		if err != nil {
			return nil, err
		}
		model := strings.TrimSpace(str(in, "llm_model"))
		if model == "" || len(model) > 200 {
			return nil, bad("A model name is required")
		}
		m, err := s.userSettings(q.r.Context(), q.user.ID)
		if err != nil {
			return nil, err
		}
		m["llm_model"], m["top_k"] = model, max(1, min(20, number(in, "top_k", 8)))
		if err = s.kvSet(q.r.Context(), "settings:"+q.user.ID, m, 0); err != nil {
			return nil, err
		}
	}
	m, err := s.userSettings(q.r.Context(), q.user.ID)
	if err != nil {
		return nil, err
	}
	keys := M{}
	for k, fallback := range map[string]string{"openai": s.cfg.LLMKey, "courtlistener": s.cfg.CourtListenerToken} {
		v, err := s.providerKey(q.r.Context(), q.user.ID, k, fallback)
		if err != nil {
			return nil, err
		}
		keys[k] = v != ""
	}
	m["keys"] = keys
	m["max_upload_mb"] = s.cfg.MaxUpload >> 20
	m["edition"] = "CaseCite GoodMem"
	return m, nil
}
func (s *Server) providerKey(ctx context.Context, user, provider, fallback string) (string, error) {
	var enc string
	err := s.kvGet(ctx, "credential:"+user+":"+provider, &enc)
	if notFound(err) {
		if provider == "courtlistener" {
			key, _, err := s.instanceCourtToken(ctx)
			return key, err
		}
		return fallback, nil
	}
	if err != nil {
		return "", err
	}
	return s.crypt(enc, true)
}
func (s *Server) saveKey(q request) (any, error) {
	provider := q.r.PathValue("provider")
	if provider != "openai" && provider != "courtlistener" {
		return nil, bad("Unsupported key type")
	}
	key := "credential:" + q.user.ID + ":" + provider
	if q.r.Method == "DELETE" {
		_, err := s.db.ExecContext(q.r.Context(), `DELETE FROM cc_kv WHERE key=$1`, key)
		return M{"status": "deleted"}, err
	}
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	value := strings.TrimSpace(str(m, "api_key"))
	if value == "" || len(value) > 8000 {
		return nil, bad("A valid API key is required")
	}
	enc, err := s.crypt(value, false)
	if err != nil {
		return nil, err
	}
	if err = s.kvSet(q.r.Context(), key, enc, 0); err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "credential.save", "provider", provider, M{})
	return M{"status": "saved"}, nil
}
func (s *Server) users(q request) (any, error) {
	if err := admin(q); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(q.r.Context(), `SELECT id,email,name,role,active,must_change_password,created_at FROM cc_users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []M{}
	for rows.Next() {
		var id, email, name, role string
		var active, change bool
		var created time.Time
		if err = rows.Scan(&id, &email, &name, &role, &active, &change, &created); err != nil {
			return nil, err
		}
		out = append(out, M{"id": id, "email": email, "name": name, "role": role, "active": active, "must_change_password": change, "created_at": created})
	}
	return M{"users": out}, rows.Err()
}
func (s *Server) createUser(q request) (any, error) {
	if err := admin(q); err != nil {
		return nil, err
	}
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	email := strings.ToLower(strings.TrimSpace(str(m, "email")))
	name := strings.TrimSpace(str(m, "name"))
	password := str(m, "password")
	role := str(m, "role")
	if role == "" {
		role = "attorney"
	}
	if role != "admin" && role != "attorney" {
		return nil, bad("Role must be admin or attorney")
	}
	if !validPassword(password) || !strings.Contains(email, "@") || name == "" || len(name) > 200 || len(email) > 254 {
		return nil, bad("A name, email, and temporary password of 12–72 bytes are required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	id := uuid()
	_, err = s.db.ExecContext(q.r.Context(), `INSERT INTO cc_users(id,email,name,role,password_hash,must_change_password) VALUES($1,$2,$3,$4,$5,true)`, id, email, name, role, string(hash))
	if err != nil {
		return nil, bad("This email cannot be registered")
	}
	s.audit(q.r.Context(), q.user.ID, "user.create", "user", id, M{})
	return M{"id": id, "email": email, "name": name, "role": role, "must_change_password": true}, nil
}
func (s *Server) updateUser(q request) (any, error) {
	if err := admin(q); err != nil {
		return nil, err
	}
	id := q.r.PathValue("id")
	if id == q.user.ID {
		return nil, bad("Use your account settings to change your own password")
	}
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(q.r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if active, ok := m["active"].(bool); ok {
		if _, err = tx.ExecContext(q.r.Context(), `UPDATE cc_users SET active=$1 WHERE id=$2`, active, id); err != nil {
			return nil, err
		}
		if !active {
			if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_sessions WHERE user_id=$1`, id); err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(q.r.Context(), `UPDATE cc_jobs SET status='cancelled',error='Account disabled',input=CASE WHEN kind='picker.import' THEN input-'encrypted_files' ELSE input END WHERE user_id=$1 AND status IN ('pending','running')`, id); err != nil {
				return nil, err
			}
		}
	}
	if password := str(m, "password"); password != "" {
		if !validPassword(password) {
			return nil, bad("Temporary password must contain 12–72 bytes")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(q.r.Context(), `UPDATE cc_users SET password_hash=$1,must_change_password=true WHERE id=$2`, string(hash), id); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_sessions WHERE user_id=$1`, id); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "user.update", "user", id, M{})
	return M{"status": "updated"}, nil
}
func (s *Server) auditEvents(q request) (any, error) {
	if err := permit(q, "admin.audit"); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(q.r.Context(), `SELECT id,COALESCE(user_id,''),event_type,resource_type,resource_id,created_at FROM cc_audit ORDER BY id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []M{}
	for rows.Next() {
		var id int64
		var user, event, kind, resource string
		var at time.Time
		if err = rows.Scan(&id, &user, &event, &kind, &resource, &at); err != nil {
			return nil, err
		}
		out = append(out, M{"id": id, "user_id": user, "event_type": event, "resource_type": kind, "resource_id": resource, "created_at": at})
	}
	return M{"events": out}, rows.Err()
}
