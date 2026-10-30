package app

import (
	"database/sql"
	"encoding/json"
	"net/mail"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const adminUserColumns = `id,email,name,role,active,must_change_password,mfa_enabled,created_at,last_login`

func scanAdminUser(row scanner) (M, error) {
	var id, email, name, role string
	var active, change, mfa bool
	var created time.Time
	var last sql.NullTime
	err := row.Scan(&id, &email, &name, &role, &active, &change, &mfa, &created, &last)
	if err != nil {
		return nil, err
	}
	var lastLogin any
	if last.Valid {
		lastLogin = last.Time
	}
	return M{"id": id, "email": email, "name": name, "roles": []string{role}, "is_active": active, "must_change_password": change, "mfa_enabled": mfa, "created_at": created, "last_login": lastLogin}, nil
}

func (s *Server) adminUsers(q request) (any, error) {
	if err := permit(q, "admin.users"); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(q.r.Context(), `SELECT `+adminUserColumns+` FROM cc_users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []M{}
	for rows.Next() {
		u, err := scanAdminUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return M{"users": users}, rows.Err()
}

func (s *Server) inviteUser(q request) (any, error) {
	if err := permit(q, "admin.users"); err != nil {
		return nil, err
	}
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	email := strings.ToLower(strings.TrimSpace(str(m, "email")))
	name := strings.TrimSpace(str(m, "name"))
	role := str(m, "role")
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 || name == "" || len(name) > 200 {
		return nil, bad("A valid email address and name are required")
	}
	if !slices.Contains(roles, role) {
		return nil, bad("Unknown role")
	}
	password := "Aa9!" + token()[:24]
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	id := uuid()
	u, err := scanAdminUser(s.db.QueryRowContext(q.r.Context(), `INSERT INTO cc_users(id,email,name,role,password_hash,must_change_password) VALUES($1,$2,$3,$4,$5,true) ON CONFLICT(email) DO NOTHING RETURNING `+adminUserColumns, id, email, name, role, string(hash)))
	if notFound(err) {
		return nil, fail(409, "An account with this email already exists")
	}
	if err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "user.create", "user", id, M{"role": role})
	return M{"user": u, "temporary_password": password}, nil
}

func (s *Server) adminUserAction(q request) (any, error) {
	if err := permit(q, "admin.users"); err != nil {
		return nil, err
	}
	id := q.r.PathValue("id")
	action := q.r.URL.Path[strings.LastIndex(q.r.URL.Path, "/")+1:]
	m := M{}
	var err error
	if q.r.Method == "PATCH" {
		m, err = body(q)
		if err != nil {
			return nil, err
		}
	}
	tx, err := s.db.BeginTx(q.r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(q.r.Context(), `SELECT pg_advisory_xact_lock(734280003)`); err != nil {
		return nil, err
	}
	var role string
	var active bool
	if err = tx.QueryRowContext(q.r.Context(), `SELECT role,active FROM cc_users WHERE id=$1 FOR UPDATE`, id).Scan(&role, &active); err != nil {
		return nil, err
	}
	newRole, newActive := role, active
	switch action {
	case "role":
		newRole = str(m, "role")
		if !slices.Contains(roles, newRole) {
			return nil, bad("Unknown role")
		}
	case "active":
		var ok bool
		newActive, ok = m["is_active"].(bool)
		if !ok {
			return nil, bad("is_active must be a boolean")
		}
		if !newActive && id == q.user.ID {
			return nil, bad("You cannot deactivate your own account")
		}
	case "reset":
		if id == q.user.ID {
			return nil, bad("Use your Security settings to change your own two-factor authentication")
		}
	case "revoke":
	default:
		return nil, missing()
	}
	if active && role == "admin" && (!newActive || newRole != "admin") {
		var count int
		if err = tx.QueryRowContext(q.r.Context(), `SELECT count(*) FROM cc_users WHERE active AND role='admin'`).Scan(&count); err != nil {
			return nil, err
		}
		if count <= 1 {
			return nil, bad("Cannot remove the last active administrator")
		}
	}
	if action == "role" || action == "active" {
		_, err = tx.ExecContext(q.r.Context(), `UPDATE cc_users SET role=$1,active=$2 WHERE id=$3`, newRole, newActive, id)
	}
	if action == "reset" {
		_, err = tx.ExecContext(q.r.Context(), `UPDATE cc_users SET mfa_enabled=false,mfa_secret='',mfa_recovery='[]',mfa_last_step=-1 WHERE id=$1`, id)
	}
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(q.r.Context(), `DELETE FROM cc_sessions WHERE user_id=$1`, id)
	if err != nil {
		return nil, err
	}
	ended, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_kv WHERE key LIKE 'mfa-challenge:%' AND value->>'user_id'=$1`, id); err != nil {
		return nil, err
	}
	if !newActive {
		if _, err = tx.ExecContext(q.r.Context(), `UPDATE cc_jobs SET status='cancelled',updated_at=now() WHERE user_id=$1 AND status IN ('pending','running')`, id); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_device_codes WHERE user_id=$1`, id); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "user."+action, "user", id, M{"role": newRole, "active": newActive})
	if action == "role" || action == "active" {
		return scanAdminUser(s.db.QueryRowContext(q.r.Context(), `SELECT `+adminUserColumns+` FROM cc_users WHERE id=$1`, id))
	}
	return M{"status": "ok", "sessions_revoked": ended}, nil
}

func (s *Server) exportUser(q request) (any, error) {
	if err := permit(q, "admin.users"); err != nil {
		return nil, err
	}
	id := q.r.PathValue("id")
	profile, err := scanAdminUser(s.db.QueryRowContext(q.r.Context(), `SELECT `+adminUserColumns+` FROM cc_users WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	result := M{"user": profile, "exported_at": nowString()}
	rows, err := s.db.QueryContext(q.r.Context(), `SELECT kind,id,data,created_at,updated_at,matter_id FROM cc_objects WHERE owner_id=$1 ORDER BY kind,created_at`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind, objectID string
		var b []byte
		var created, updated time.Time
		var matter sql.NullString
		if err = rows.Scan(&kind, &objectID, &b, &created, &updated, &matter); err != nil {
			rows.Close()
			return nil, err
		}
		m := M{}
		if err = json.Unmarshal(b, &m); err != nil {
			rows.Close()
			return nil, err
		}
		m["id"] = objectID
		m["created_at"] = created
		m["updated_at"] = updated
		m["matter_id"] = nullable(matter.String)
		items, _ := result[kind].([]M)
		result[kind] = append(items, m)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	settings, err := s.userSettings(q.r.Context(), id)
	if err != nil {
		return nil, err
	}
	result["settings"] = settings
	rows, err = s.db.QueryContext(q.r.Context(), `SELECT row_to_json(m) FROM cc_matters m WHERE owner_id=$1 OR EXISTS(SELECT 1 FROM cc_matter_members mm WHERE mm.matter_id=m.id AND mm.user_id=$1)`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	matters := []M{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var m M
		if err = json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		matters = append(matters, m)
	}
	result["matters"] = matters
	s.audit(q.r.Context(), q.user.ID, "user.export", "user", id, M{})
	return result, rows.Err()
}

func (s *Server) deleteUser(q request) (any, error) {
	if err := permit(q, "admin.users"); err != nil {
		return nil, err
	}
	id := q.r.PathValue("id")
	if id == q.user.ID {
		return nil, bad("You cannot delete your own account")
	}
	// Disable first, then purge GoodMem. A failed external purge leaves an
	// inactive account whose deletion can be retried without losing the IDs.
	tx, err := s.db.BeginTx(q.r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(q.r.Context(), `SELECT pg_advisory_xact_lock(734280003)`); err != nil {
		return nil, err
	}
	var role string
	var active bool
	if err = tx.QueryRowContext(q.r.Context(), `SELECT role,active FROM cc_users WHERE id=$1 FOR UPDATE`, id).Scan(&role, &active); err != nil {
		return nil, err
	}
	if role == "admin" && active {
		var count int
		if err = tx.QueryRowContext(q.r.Context(), `SELECT count(*) FROM cc_users WHERE role='admin' AND active`).Scan(&count); err != nil {
			return nil, err
		}
		if count <= 1 {
			return nil, bad("Cannot delete the last active administrator")
		}
	}
	if _, err = tx.ExecContext(q.r.Context(), `UPDATE cc_users SET active=false WHERE id=$1`, id); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_sessions WHERE user_id=$1`, id); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(q.r.Context(), `UPDATE cc_jobs SET status='cancelled',updated_at=now() WHERE user_id=$1 AND status IN ('pending','running')`, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(q.r.Context(), `SELECT DISTINCT memory FROM cc_objects CROSS JOIN LATERAL (VALUES (data->>'memory_id'),(data->>'index_memory_id')) AS memories(memory) WHERE kind='document' AND owner_id=$1`, id)
	if err != nil {
		return nil, err
	}
	memories := []string{}
	for rows.Next() {
		var memory sql.NullString
		if err = rows.Scan(&memory); err != nil {
			rows.Close()
			return nil, err
		}
		if memory.Valid && memory.String != "" {
			memories = append(memories, memory.String)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, memory := range memories {
		if err = s.scheduleMemoryDeletion(q.r.Context(), memory); err != nil {
			return nil, err
		}
		if err = s.gm.Delete(q.r.Context(), memory); err != nil {
			return nil, err
		}
	}
	tx, err = s.db.BeginTx(q.r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_kv WHERE key=$1 OR key LIKE $2 OR key LIKE $3 OR (key LIKE 'mfa-challenge:%' OR key LIKE 'password-reset:%') AND value->>'user_id'=$4`, "settings:"+id, "credential:"+id+":%", "connector:"+id+":%", id); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_matters WHERE owner_id=$1`, id); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_users WHERE id=$1`, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "user.delete", "user", id, M{"memories_deleted": len(memories)})
	return M{"status": "deleted", "memories_deleted": len(memories)}, nil
}
