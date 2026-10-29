package app

import (
	"context"
	"strings"
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
	if ownerOnly {
		var personal bool
		if err = s.db.QueryRowContext(ctx, `SELECT is_personal FROM cc_matters WHERE id=$1`, id).Scan(&personal); err != nil {
			return err
		}
		if personal {
			return bad("Personal matters cannot be shared, renamed, transferred or deleted")
		}
	}
	return nil
}
func (s *Server) listMatters(q request) (any, error) {
	all := strings.HasSuffix(q.r.URL.Path, "/all")
	if all {
		if err := permit(q, "admin.users"); err != nil {
			return nil, err
		}
	} else {
		if _, err := s.db.ExecContext(q.r.Context(), `INSERT INTO cc_matters(id,owner_id,name,is_personal) VALUES($1,$2,'Personal',true) ON CONFLICT(owner_id) WHERE is_personal DO NOTHING`, uuid(), q.user.ID); err != nil {
			return nil, err
		}
	}
	rows, err := s.db.QueryContext(q.r.Context(), matterSelect+`WHERE ($2 AND NOT m.is_personal) OR (NOT $2 AND (m.owner_id=$1 OR EXISTS(SELECT 1 FROM cc_matter_members mm WHERE mm.matter_id=m.id AND mm.user_id=$1))) ORDER BY m.is_personal DESC,m.updated_at DESC`, q.user.ID, all)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []M{}
	for rows.Next() {
		m, err := scanMatter(rows, q.user.ID)
		if err != nil {
			return nil, err
		}
		if all && str(m, "owner_id") != q.user.ID {
			if err = s.checkMatter(q.r.Context(), q.user.ID, str(m, "id"), false); notFound(err) {
				m["role"] = "none"
			} else if err != nil {
				return nil, err
			}
		}
		out = append(out, m)
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
	if err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "matter.create", "matter", id, M{})
	return scanMatter(s.db.QueryRowContext(q.r.Context(), matterSelect+`WHERE m.id=$1`, id), q.user.ID)
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
		out = append(out, M{"user_id": uid, "email": email, "name": name, "is_owner": owner, "role": map[bool]string{true: "owner", false: "member"}[owner]})
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
	return s.matterDetail(q)
}
func (s *Server) removeMember(q request) (any, error) {
	id := q.r.PathValue("id")
	if err := s.checkMatter(q.r.Context(), q.user.ID, id, true); err != nil {
		return nil, err
	}
	if q.r.PathValue("user") == q.user.ID {
		return nil, bad("Transfer ownership before removing the owner")
	}
	_, err := s.db.ExecContext(q.r.Context(), `DELETE FROM cc_matter_members WHERE matter_id=$1 AND user_id=$2`, id, q.r.PathValue("user"))
	if err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "matter.member.remove", "matter", id, M{"user_id": q.r.PathValue("user")})
	return s.matterDetail(q)
}
func (s *Server) deleteMatter(q request) (any, error) {
	id := q.r.PathValue("id")
	if err := s.checkMatter(q.r.Context(), q.user.ID, id, true); err != nil {
		return nil, err
	}
	_, err := s.db.ExecContext(q.r.Context(), `DELETE FROM cc_matters WHERE id=$1`, id)
	return M{"status": "deleted", "id": id, "note": "Documents and saved work return to their owners' private libraries."}, err
}
