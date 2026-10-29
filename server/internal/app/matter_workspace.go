package app

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
)

const matterSelect = `SELECT m.id,m.name,m.client_name,m.owner_id,m.created_at,m.updated_at,m.is_personal,(SELECT count(*) FROM cc_matter_members mm WHERE mm.matter_id=m.id AND mm.user_id<>m.owner_id)+1 FROM cc_matters m `

func scanMatter(row scanner, user string) (M, error) {
	var id, name, client, owner string
	var created, updated any
	var personal bool
	var count int
	if err := row.Scan(&id, &name, &client, &owner, &created, &updated, &personal, &count); err != nil {
		return nil, err
	}
	role := "member"
	if owner == user {
		role = "owner"
	}
	return M{"id": id, "name": name, "client_name": nullable(client), "owner_id": owner, "role": role, "is_owner": owner == user, "is_personal": personal, "member_count": count, "created_at": created, "updated_at": updated}, nil
}

func (s *Server) matterDetail(q request) (any, error) {
	id := q.r.PathValue("id")
	if err := s.checkMatter(q.r.Context(), q.user.ID, id, false); err != nil {
		return nil, err
	}
	m, err := scanMatter(s.db.QueryRowContext(q.r.Context(), matterSelect+`WHERE m.id=$1`, id), q.user.ID)
	if err != nil {
		return nil, err
	}
	members, err := s.matterMembers(q)
	if err != nil {
		return nil, err
	}
	m["members"] = object(members)["members"]
	return m, nil
}

func (s *Server) updateMatter(q request) (any, error) {
	id := q.r.PathValue("id")
	if err := s.checkMatter(q.r.Context(), q.user.ID, id, true); err != nil {
		return nil, err
	}
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	var name, client string
	var personal bool
	if err = s.db.QueryRowContext(q.r.Context(), `SELECT name,client_name,is_personal FROM cc_matters WHERE id=$1`, id).Scan(&name, &client, &personal); err != nil {
		return nil, err
	}
	if personal {
		return nil, bad("A personal matter cannot be renamed")
	}
	if _, ok := m["name"]; ok {
		name = strings.TrimSpace(str(m, "name"))
	}
	if _, ok := m["client_name"]; ok {
		client = strings.TrimSpace(str(m, "client_name"))
	}
	if name == "" || len(name) > 200 || len(client) > 200 {
		return nil, bad("Matter and client names must be at most 200 characters; matter name is required")
	}
	res, err := s.db.ExecContext(q.r.Context(), `UPDATE cc_matters SET name=$1,client_name=$2,updated_at=now() WHERE id=$3 AND owner_id=$4`, name, client, id, q.user.ID)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, missing()
	}
	s.audit(q.r.Context(), q.user.ID, "matter.update", "matter", id, M{})
	return s.matterDetail(q)
}

func (s *Server) transferMatter(q request) (any, error) {
	id := q.r.PathValue("id")
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	next := str(m, "new_owner_id")
	tx, err := s.db.BeginTx(q.r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var personal bool
	if err = tx.QueryRowContext(q.r.Context(), `SELECT is_personal FROM cc_matters WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, q.user.ID).Scan(&personal); err != nil {
		return nil, err
	}
	if personal {
		return nil, bad("A personal matter cannot be transferred")
	}
	var eligible bool
	if err = tx.QueryRowContext(q.r.Context(), `SELECT EXISTS(SELECT 1 FROM cc_matter_members mm JOIN cc_users u ON u.id=mm.user_id WHERE mm.matter_id=$1 AND u.id=$2 AND u.active)`, id, next).Scan(&eligible); err != nil {
		return nil, err
	}
	if !eligible {
		return nil, bad("The new owner must be an active member of this matter")
	}
	if _, err = tx.ExecContext(q.r.Context(), `INSERT INTO cc_matter_members(matter_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, q.user.ID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(q.r.Context(), `UPDATE cc_matters SET owner_id=$1,updated_at=now() WHERE id=$2`, next, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "matter.transfer", "matter", id, M{"new_owner_id": next})
	return s.matterDetail(q)
}

func (s *Server) matterResources(q request) (any, error) {
	id := q.r.PathValue("id")
	if err := s.checkMatter(q.r.Context(), q.user.ID, id, false); err != nil {
		return nil, err
	}
	kind, key := "document", "documents"
	if strings.HasSuffix(q.r.URL.Path, "/chats") {
		kind, key = "chat_session", "sessions"
	}
	if strings.HasSuffix(q.r.URL.Path, "/analyses") {
		kind, key = "analysis", "analyses"
	}
	items, err := s.objects(q.r.Context(), q.user.ID, kind)
	if err != nil {
		return nil, err
	}
	out := []M{}
	var personal bool
	if err = s.db.QueryRowContext(q.r.Context(), `SELECT is_personal FROM cc_matters WHERE id=$1`, id).Scan(&personal); err != nil {
		return nil, err
	}
	for _, item := range items {
		if str(item, "matter_id") == id || (personal && str(item, "matter_id") == "" && str(item, "user_id") == q.user.ID) {
			if kind == "document" {
				item = s.refreshDocument(q.r.Context(), item)
			} else if kind == "chat_session" {
				item = sessionSummary(item, false)
			} else if kind == "analysis" {
				item = M{"analysis_id": item["id"], "document_id": item["document_id"], "filename": item["filename"], "contract_type": item["contract_type"], "created_at": item["created_at"], "issues": len(maps(item["issues"])), "user_id": item["user_id"], "matter_id": item["matter_id"]}
			}
			out = append(out, item)
		}
	}
	return M{key: out, "count": len(out), "total": len(out)}, nil
}

func (s *Server) moveMatterDocuments(q request) (any, error) {
	id := q.r.PathValue("id")
	if err := s.checkMatter(q.r.Context(), q.user.ID, id, false); err != nil {
		return nil, err
	}
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	ids := stringsOf(m["document_ids"])
	if len(ids) == 0 || len(ids) > 500 {
		return nil, bad("Select between 1 and 500 documents")
	}
	tx, err := s.db.BeginTx(q.r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, docID := range ids {
		var allowed bool
		if err = tx.QueryRowContext(q.r.Context(), `SELECT owner_id=$1 OR EXISTS(SELECT 1 FROM cc_matters WHERE id=o.matter_id AND owner_id=$1) FROM cc_objects o WHERE kind='document' AND id=$2 FOR UPDATE`, q.user.ID, docID).Scan(&allowed); err != nil {
			return nil, err
		}
		if !allowed {
			return nil, missing()
		}
		if _, err = tx.ExecContext(q.r.Context(), `UPDATE cc_objects SET matter_id=$1,updated_at=now() WHERE kind='document' AND id=$2`, id, docID); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "matter.documents.move", "matter", id, M{"document_ids": ids})
	return M{"status": "moved", "matter_id": id, "moved": ids}, nil
}

func (s *Server) matterActivity(q request) (any, error) {
	id := q.r.PathValue("id")
	if err := s.checkMatter(q.r.Context(), q.user.ID, id, false); err != nil {
		return nil, err
	}
	limit := 50
	if n, err := strconv.Atoi(q.r.URL.Query().Get("limit")); err == nil {
		limit = max(1, min(200, n))
	}
	rows, err := s.db.QueryContext(q.r.Context(), `SELECT a.id,a.created_at,a.event_type,COALESCE(a.user_id,''),u.email,a.resource_type,a.resource_id,a.details FROM cc_audit a LEFT JOIN cc_users u ON u.id=a.user_id WHERE (a.resource_type='matter' AND a.resource_id=$1) OR a.details->>'matter_id'=$1 OR EXISTS(SELECT 1 FROM cc_objects o WHERE o.matter_id=$1 AND o.id=a.resource_id) ORDER BY a.id DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []M{}
	for rows.Next() {
		var eid int64
		var timestamp any
		var event, user, resource, resourceID string
		var email sql.NullString
		var raw []byte
		if err = rows.Scan(&eid, &timestamp, &event, &user, &email, &resource, &resourceID, &raw); err != nil {
			return nil, err
		}
		details := M{}
		if err = json.Unmarshal(raw, &details); err != nil {
			return nil, err
		}
		events = append(events, M{"id": eid, "timestamp": timestamp, "event_type": event, "action": nullable(str(details, "action")), "user_id": nullable(user), "user_email": nullable(email.String), "resource_type": resource, "resource_id": resourceID, "success": true})
	}
	return M{"events": events}, rows.Err()
}

func (s *Server) matterRoutes() {
	s.api("GET /api/v1/matters/all", false, s.listMatters)
	s.api("GET /api/v1/matters/{id}", false, s.matterDetail)
	s.api("PATCH /api/v1/matters/{id}", false, s.updateMatter)
	s.api("POST /api/v1/matters/{id}/transfer", false, s.transferMatter)
	s.api("GET /api/v1/matters/{id}/documents", false, s.matterResources)
	s.api("POST /api/v1/matters/{id}/documents/move", false, s.moveMatterDocuments)
	s.api("GET /api/v1/matters/{id}/chats", false, s.matterResources)
	s.api("GET /api/v1/matters/{id}/analyses", false, s.matterResources)
	s.api("GET /api/v1/matters/{id}/activity", false, s.matterActivity)
}
