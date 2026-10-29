package app

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

func sessionSummary(m M, workspace bool) M {
	result := M{"id": m["id"], "title": m["title"], "created_at": m["created_at"], "updated_at": m["updated_at"]}
	if workspace {
		result["surface"] = m["surface"]
	} else {
		result["message_count"] = number(m, "message_count", 0)
		result["user_id"], result["matter_id"] = m["user_id"], m["matter_id"]
	}
	return result
}
func sessionLimit(q request, fallback int) (int, error) {
	if raw := q.r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			return 0, bad("limit must be between 1 and 200")
		}
		return n, nil
	}
	return fallback, nil
}
func checkSessionTitle(value any, max int) error {
	title, ok := value.(string)
	if !ok || len([]rune(title)) < 1 || len([]rune(title)) > max {
		return bad("Session title must contain 1 to " + strconv.Itoa(max) + " characters")
	}
	return nil
}
func checkSessionPayload(value any) error {
	payload, ok := value.(map[string]any)
	if !ok {
		return bad("Session payload must be an object")
	}
	if len(jsonBytes(payload)) > 2000000 {
		return fail(413, "Session snapshot too large to save.")
	}
	return nil
}
func (s *Server) workspaceSessions(q request) (any, error) {
	workspace := strings.HasPrefix(q.r.URL.Path, "/api/v1/workspace-sessions")
	kind, defaultLimit, titleLimit := "chat_session", 50, 255
	if workspace {
		kind, defaultLimit, titleLimit = "workspace_session", 100, 300
	}
	id := q.r.PathValue("id")
	if id == "" && q.r.Method == "GET" {
		limit, err := sessionLimit(q, defaultLimit)
		if err != nil {
			return nil, err
		}
		// Do not load multi-megabyte snapshots just to render the History list.
		rows, err := s.db.QueryContext(q.r.Context(), `SELECT o.id,o.owner_id,COALESCE(o.matter_id,''),o.data-'payload',o.created_at,o.updated_at FROM cc_objects o WHERE `+visibleSQL+` AND o.kind=$2 ORDER BY o.updated_at DESC,o.id LIMIT $3`, q.user.ID, kind, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := []M{}
		for rows.Next() {
			m, err := scanObject(rows)
			if err != nil {
				return nil, err
			}
			items = append(items, sessionSummary(m, workspace))
		}
		return M{"sessions": items}, rows.Err()
	}
	if id == "" && q.r.Method == "POST" {
		input, err := body(q)
		if err != nil {
			return nil, err
		}
		if err = checkSessionTitle(input["title"], titleLimit); err != nil {
			return nil, err
		}
		data := M{"title": input["title"], "message_count": 0}
		matter := ""
		if workspace {
			surface, ok := input["surface"].(string)
			if !ok || len([]rune(surface)) < 1 || len([]rune(surface)) > 50 {
				return nil, bad("Session surface must contain 1 to 50 characters")
			}
			data["surface"] = surface
			payload, exists := input["payload"]
			if !exists {
				payload = M{}
			}
			if err = checkSessionPayload(payload); err != nil {
				return nil, err
			}
			data["payload"] = payload
		} else {
			data["title"] = fallback(strings.TrimSpace(str(input, "title")), "Untitled conversation")
			matter = str(input, "matter_id")
			if err = s.checkMatter(q.r.Context(), q.user.ID, matter, false); err != nil {
				return nil, err
			}
		}
		id = uuid()
		if err = s.saveObject(q.r.Context(), q.user.ID, kind, id, matter, data); err != nil {
			return nil, err
		}
		m, err := s.getObject(q.r.Context(), q.user.ID, kind, id)
		if err != nil {
			return nil, err
		}
		if workspace {
			writeJSON(q.w, 201, sessionSummary(m, true))
			return nil, nil
		}
		return sessionSummary(m, false), nil
	}
	if q.r.Method == "GET" {
		m, err := s.getObject(q.r.Context(), q.user.ID, kind, id)
		if err != nil {
			return nil, err
		}
		result := sessionSummary(m, workspace)
		if workspace {
			result["payload"] = object(m["payload"])
		} else {
			messages, err := s.chatMessages(q.r.Context(), id)
			if err != nil {
				return nil, err
			}
			result["messages"] = messages
		}
		s.audit(q.r.Context(), q.user.ID, kind+".read", kind, id, M{})
		return result, nil
	}
	if workspace && q.r.Method == "PUT" {
		input, err := body(q)
		if err != nil {
			return nil, err
		}
		if title := input["title"]; title != nil {
			if err = checkSessionTitle(title, titleLimit); err != nil {
				return nil, err
			}
		}
		if payload := input["payload"]; payload != nil {
			if err = checkSessionPayload(payload); err != nil {
				return nil, err
			}
		}
		tx, err := s.db.BeginTx(q.r.Context(), nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		m, err := scanObject(tx.QueryRowContext(q.r.Context(), `SELECT id,owner_id,COALESCE(matter_id,''),data,created_at,updated_at FROM cc_objects WHERE kind=$1 AND id=$2 AND owner_id=$3 FOR UPDATE`, kind, id, q.user.ID))
		if err != nil {
			return nil, err
		}
		for _, key := range []string{"title", "payload"} {
			if input[key] != nil {
				m[key] = input[key]
			}
		}
		if _, err = tx.ExecContext(q.r.Context(), `UPDATE cc_objects SET data=$3,updated_at=now() WHERE kind=$1 AND id=$2`, kind, id, jsonBytes(m)); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		m, err = s.getObject(q.r.Context(), q.user.ID, kind, id)
		if err != nil {
			return nil, err
		}
		return sessionSummary(m, true), nil
	}
	if q.r.Method == "DELETE" {
		allowed := `owner_id=$3`
		if !workspace {
			allowed += ` OR EXISTS(SELECT 1 FROM cc_matters m WHERE m.id=cc_objects.matter_id AND m.owner_id=$3)`
		}
		r, err := s.db.ExecContext(q.r.Context(), `DELETE FROM cc_objects WHERE kind=$1 AND id=$2 AND (`+allowed+`)`, kind, id, q.user.ID)
		if err != nil {
			return nil, err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, fail(404, "Session not found")
		}
		s.audit(q.r.Context(), q.user.ID, kind+".delete", kind, id, M{})
		return M{"status": "deleted", "id": id}, nil
	}
	return nil, fail(405, "Method not allowed")
}
func (s *Server) chatMessages(ctx context.Context, id string) ([]M, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,data,created_at FROM cc_chat_messages WHERE session_id=$1 ORDER BY sequence`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []M{}
	for rows.Next() {
		var mid string
		var raw []byte
		var created time.Time
		if err = rows.Scan(&mid, &raw, &created); err != nil {
			return nil, err
		}
		m := M{}
		if err = json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		m["id"], m["created_at"] = mid, created.UTC().Format(time.RFC3339Nano)
		messages = append(messages, m)
	}
	return messages, rows.Err()
}
func (s *Server) appendChatMessage(ctx context.Context, user, id, role, content string, citations []M, strategy, stats any) (M, error) {
	if role != "user" && role != "assistant" {
		return nil, bad("Invalid message role")
	}
	if citations == nil {
		citations = []M{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var exists string
	if err = tx.QueryRowContext(ctx, `SELECT o.id FROM cc_objects o WHERE `+visibleSQL+` AND kind='chat_session' AND id=$2 FOR UPDATE`, user, id).Scan(&exists); err != nil {
		return nil, err
	}
	message := M{"role": role, "content": content, "citations": citations, "strategy": strategy, "stats": stats}
	mid := uuid()
	var created time.Time
	if err = tx.QueryRowContext(ctx, `INSERT INTO cc_chat_messages(id,session_id,data) VALUES($1,$2,$3) RETURNING created_at`, mid, id, jsonBytes(message)).Scan(&created); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE cc_objects SET updated_at=now(),data=jsonb_set(data,'{message_count}',to_jsonb(COALESCE((data->>'message_count')::int,0)+1)) WHERE kind='chat_session' AND id=$1`, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	message["id"], message["created_at"] = mid, created.UTC().Format(time.RFC3339Nano)
	return message, nil
}
func (s *Server) workspaceSessionRoutes() {
	for _, base := range []string{"/api/v1/workspace-sessions", "/api/v1/chat/sessions"} {
		s.api("GET "+base, false, s.workspaceSessions)
		s.api("POST "+base, false, s.workspaceSessions)
		s.api("GET "+base+"/{id}", false, s.workspaceSessions)
		s.api("DELETE "+base+"/{id}", false, s.workspaceSessions)
	}
	s.api("PUT /api/v1/workspace-sessions/{id}", false, s.workspaceSessions)
}
