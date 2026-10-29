package app

import (
	"context"
	"strings"
	"time"
)

func (s *Server) checkMatter(ctx context.Context, user, id string, ownerOnly bool) error {
	if id == "" {
		return nil
	}
	var owner string
	err := s.db.QueryRowContext(ctx, `SELECT m.owner_id FROM cc_matters m WHERE m.id=$1 AND (m.owner_id=$2 OR EXISTS(SELECT 1 FROM cc_matter_members mm WHERE mm.matter_id=m.id AND mm.user_id=$2))`, id, user).Scan(&owner)
	if err != nil {
		return err
	}
	if ownerOnly && owner != user {
		return fail(403, "Only the matter owner can change membership or remove the matter")
	}
	return nil
}
func (s *Server) listMatters(q request) (any, error) {
	rows, err := s.db.QueryContext(q.r.Context(), `SELECT m.id,m.name,m.client_name,m.owner_id,m.created_at,m.updated_at FROM cc_matters m WHERE m.owner_id=$1 OR EXISTS(SELECT 1 FROM cc_matter_members mm WHERE mm.matter_id=m.id AND mm.user_id=$1) ORDER BY m.updated_at DESC`, q.user.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []M{}
	for rows.Next() {
		var id, name, client, owner string
		var created, updated time.Time
		if err = rows.Scan(&id, &name, &client, &owner, &created, &updated); err != nil {
			return nil, err
		}
		out = append(out, M{"id": id, "name": name, "client_name": client, "owner_id": owner, "is_owner": owner == q.user.ID, "created_at": created, "updated_at": updated})
	}
	return M{"matters": out}, rows.Err()
}
func (s *Server) createMatter(q request) (any, error) {
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(str(m, "name"))
	if name == "" || len(name) > 200 {
		return nil, bad("Matter name must be 1–200 characters")
	}
	id := uuid()
	_, err = s.db.ExecContext(q.r.Context(), `INSERT INTO cc_matters(id,owner_id,name,client_name) VALUES($1,$2,$3,$4)`, id, q.user.ID, name, str(m, "client_name"))
	return M{"id": id, "name": name, "client_name": m["client_name"], "owner_id": q.user.ID, "is_owner": true}, err
}
func (s *Server) matterMembers(q request) (any, error) {
	id := q.r.PathValue("id")
	if err := s.checkMatter(q.r.Context(), q.user.ID, id, false); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(q.r.Context(), `SELECT u.id,u.email,u.name,u.id=m.owner_id FROM cc_users u JOIN cc_matters m ON m.id=$1 WHERE u.id=m.owner_id OR EXISTS(SELECT 1 FROM cc_matter_members mm WHERE mm.matter_id=m.id AND mm.user_id=u.id) ORDER BY u.name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []M{}
	for rows.Next() {
		var uid, email, name string
		var owner bool
		if err = rows.Scan(&uid, &email, &name, &owner); err != nil {
			return nil, err
		}
		out = append(out, M{"user_id": uid, "email": email, "name": name, "is_owner": owner})
	}
	return M{"members": out}, rows.Err()
}
func (s *Server) addMember(q request) (any, error) {
	id := q.r.PathValue("id")
	if err := s.checkMatter(q.r.Context(), q.user.ID, id, true); err != nil {
		return nil, err
	}
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	var uid string
	if err = s.db.QueryRowContext(q.r.Context(), `SELECT id FROM cc_users WHERE lower(email)=$1 AND active`, strings.ToLower(strings.TrimSpace(str(m, "email")))).Scan(&uid); err != nil {
		if notFound(err) {
			return nil, bad("No active account has that email")
		}
		return nil, err
	}
	_, err = s.db.ExecContext(q.r.Context(), `INSERT INTO cc_matter_members(matter_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, uid)
	if err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "matter.member.add", "matter", id, M{"user_id": uid})
	return s.matterMembers(q)
}
func (s *Server) removeMember(q request) (any, error) {
	id := q.r.PathValue("id")
	if err := s.checkMatter(q.r.Context(), q.user.ID, id, true); err != nil {
		return nil, err
	}
	_, err := s.db.ExecContext(q.r.Context(), `DELETE FROM cc_matter_members WHERE matter_id=$1 AND user_id=$2`, id, q.r.PathValue("user"))
	if err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "matter.member.remove", "matter", id, M{"user_id": q.r.PathValue("user")})
	return s.matterMembers(q)
}
func (s *Server) deleteMatter(q request) (any, error) {
	id := q.r.PathValue("id")
	if err := s.checkMatter(q.r.Context(), q.user.ID, id, true); err != nil {
		return nil, err
	}
	_, err := s.db.ExecContext(q.r.Context(), `DELETE FROM cc_matters WHERE id=$1`, id)
	return M{"status": "deleted", "note": "Documents and saved work return to their owners' private libraries."}, err
}
