package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

type goodMemUserKey struct{}
type goodMemLockKey struct{}

func goodMemUserContext(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, goodMemUserKey{}, user)
}

// Serialize policy transitions across replicas, ingestion and maintenance. A
// transaction-scoped lock is released even if cancellation discards the socket.
func (s *Server) withGoodMemLock(ctx context.Context, fn func(context.Context) error) error {
	if s.cfg.GoodMemSecurity == nil || ctx.Value(goodMemLockKey{}) == true {
		return fn(ctx)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(734280099)`); err != nil {
		return err
	}
	return fn(context.WithValue(ctx, goodMemLockKey{}, true))
}

func (s *Server) InitializeGoodMemSecurity(ctx context.Context) error {
	return s.initializeGoodMemSecurity(ctx, false)
}
func (s *Server) initializeGoodMemSecurity(ctx context.Context, migration bool) error {
	if s.cfg.GoodMemSecurity == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return s.withGoodMemLock(ctx, func(ctx context.Context) error {
		for _, subject := range s.cfg.GoodMemSecurity.Subjects {
			if subject.PrincipalID == "" || subject.APIKey == "" {
				return errors.New("incomplete GoodMem subject credential")
			}
			secret, err := s.crypt(subject.APIKey, false)
			if err != nil {
				return err
			}
			if _, err = s.db.ExecContext(ctx, `INSERT INTO cc_goodmem_subjects(principal_id,credential) VALUES($1,$2) ON CONFLICT(principal_id) DO UPDATE SET credential=EXCLUDED.credential`, subject.PrincipalID, secret); err != nil {
				return err
			}
		}
		var legacy bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cc_objects o WHERE kind='document' AND COALESCE(o.data->>'goodmem_security','')<>'1') OR EXISTS(SELECT 1 FROM cc_kv WHERE key LIKE 'goodmem-migration:%')`).Scan(&legacy); err != nil {
			return err
		}
		if legacy && !migration {
			return errors.New("legacy shared-space documents require the offline migrate-goodmem-security command before serving requests")
		}
		if err := s.gm.Check(ctx); err != nil {
			return err
		}
		if migration {
			return nil
		}
		return s.reconcileGoodMemSpaces(ctx, nil)
	})
}

func (s *Server) ensureGoodMemSubject(ctx context.Context, user string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT principal_id FROM cc_goodmem_subjects WHERE user_id=$1 AND NOT retired`, user).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !notFound(err) {
		return "", err
	}
	for {
		err = s.db.QueryRowContext(ctx, `SELECT principal_id FROM cc_goodmem_subjects WHERE user_id IS NULL AND NOT retired ORDER BY created_at,principal_id LIMIT 1`).Scan(&id)
		if notFound(err) {
			return "", fail(503, "GoodMem reader identities are unavailable. Increase GOODMEM_IDENTITY_CAPACITY and rerun setup-goodmem.")
		}
		if err != nil {
			return "", err
		}
		bound, err := s.bindGoodMemSubject(ctx, id, user)
		if err != nil {
			return "", err
		}
		if !bound {
			if _, err = s.db.ExecContext(ctx, `UPDATE cc_goodmem_subjects SET retired=true WHERE principal_id=$1`, id); err != nil {
				return "", err
			}
			continue
		}
		_, err = s.db.ExecContext(ctx, `UPDATE cc_goodmem_subjects SET user_id=$1 WHERE principal_id=$2`, user, id)
		return id, err
	}
}

func (s *Server) goodMemReadKey(ctx context.Context) (string, error) {
	user, _ := ctx.Value(goodMemUserKey{}).(string)
	if user == "" {
		return "", fail(403, "A user identity is required to read GoodMem documents")
	}
	read := func() (string, error) {
		var encrypted string
		err := s.db.QueryRowContext(ctx, `SELECT g.credential FROM cc_goodmem_subjects g JOIN cc_users u ON u.id=g.user_id WHERE u.id=$1 AND u.active AND NOT g.retired`, user).Scan(&encrypted)
		if err != nil {
			return "", err
		}
		return s.crypt(encrypted, true)
	}
	key, err := read()
	if err == nil {
		return key, nil
	}
	if !notFound(err) {
		return "", err
	}
	err = s.withGoodMemLock(ctx, func(ctx context.Context) error {
		var active bool
		if err := s.db.QueryRowContext(ctx, `SELECT active FROM cc_users WHERE id=$1`, user).Scan(&active); err != nil || !active {
			return fail(403, "The account is no longer active")
		}
		if _, err := s.ensureGoodMemSubject(ctx, user); err != nil {
			return err
		}
		ids, err := s.goodMemDocumentIDs(ctx, visibleSQL, user)
		if err != nil {
			return err
		}
		return s.reconcileGoodMemSpaces(ctx, ids)
	})
	if err != nil {
		return "", err
	}
	return read()
}

func (s *Server) goodMemDocumentIDs(ctx context.Context, where string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT o.id FROM cc_objects o WHERE o.kind='document' AND (`+where+`) ORDER BY o.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Server) goodMemSpaces(ctx context.Context, documents []string, dirtyOnly bool) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT space_id FROM cc_goodmem_spaces WHERE ($1::text[] IS NULL OR document_id=ANY($1)) AND (NOT $2 OR dirty) ORDER BY space_id`, documents, dirtyOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Persist the intended revocation before the network call. An uncertain reply
// leaves its ID available for retry; neither SQL deletion nor a cascade erases it.
func (s *Server) revokeGoodMemAssignment(ctx context.Context, space, principal, id string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE cc_goodmem_access SET state='revoking' WHERE assignment_id=$1`, id); err != nil {
		return err
	}
	// A cancelled create may still be in flight upstream. A 404 on DELETE is
	// not proof it cannot arrive later. Materialize its immutable ID first;
	// revocation then leaves a tombstone that every late duplicate conflicts with.
	if err := s.gm.finalizePolicyCreate(ctx, "role-assignments", id, M{"roleAssignmentId": id, "principalId": principal, "role": "SPACE_VIEWER", "assignedResource": M{"kind": "SPACE", "resourceId": space}}); err != nil {
		return err
	}
	if err := s.gm.DeletePolicy(ctx, "role-assignments", id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM cc_goodmem_access WHERE space_id=$1 AND principal_id=$2 AND assignment_id=$3`, space, principal, id)
	return err
}

func (g *GoodMem) finalizePolicyCreate(ctx context.Context, kind, id string, payload M) error {
	_, err := g.JSON(ctx, "GET", "/v1/access-policy/"+kind+"/"+id, nil)
	if goodMemStatus(err, 404) {
		_, err = g.JSON(ctx, "POST", "/v1/access-policy/"+kind, payload)
		if goodMemStatus(err, 409) {
			return nil
		}
	}
	return err
}

func (s *Server) suspendGoodMemSpaces(ctx context.Context, documents []string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE cc_goodmem_spaces SET dirty=true WHERE document_id=ANY($1)`, documents); err != nil {
		return err
	}
	spaces, err := s.goodMemSpaces(ctx, documents, false)
	if err != nil {
		return err
	}
	for _, space := range spaces {
		rows, err := s.db.QueryContext(ctx, `SELECT principal_id,assignment_id FROM cc_goodmem_access WHERE space_id=$1`, space)
		if err != nil {
			return err
		}
		a := [][2]string{}
		for rows.Next() {
			var p, id string
			if err = rows.Scan(&p, &id); err != nil {
				rows.Close()
				return err
			}
			a = append(a, [2]string{p, id})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, entry := range a {
			if err = s.revokeGoodMemAssignment(ctx, space, entry[0], entry[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) reconcileGoodMemSpaces(ctx context.Context, documents []string) error {
	spaces, err := s.goodMemSpaces(ctx, documents, false)
	if err != nil {
		return err
	}
	for _, space := range spaces {
		if err = s.reconcileGoodMemSpace(ctx, space); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) reconcileGoodMemSpace(ctx context.Context, space string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT u.id FROM cc_goodmem_spaces g JOIN cc_objects o ON o.id=g.document_id AND o.kind='document' JOIN cc_users u ON u.active AND (u.id=o.owner_id OR EXISTS(SELECT 1 FROM cc_matters m WHERE m.id=o.matter_id AND (m.owner_id=u.id OR EXISTS(SELECT 1 FROM cc_matter_members mm WHERE mm.matter_id=m.id AND mm.user_id=u.id)))) WHERE g.space_id=$1 AND COALESCE(o.data->>'status','')<>'deleting' ORDER BY u.id`, space)
	if err != nil {
		return err
	}
	users := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		users = append(users, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	desired := map[string]bool{}
	for _, user := range users {
		principal, err := s.ensureGoodMemSubject(ctx, user)
		if err != nil {
			return err
		}
		desired[principal] = true
	}
	rows, err = s.db.QueryContext(ctx, `SELECT principal_id,assignment_id,state FROM cc_goodmem_access WHERE space_id=$1`, space)
	if err != nil {
		return err
	}
	assignments := [][3]string{}
	for rows.Next() {
		var p, id, state string
		if err = rows.Scan(&p, &id, &state); err != nil {
			rows.Close()
			return err
		}
		assignments = append(assignments, [3]string{p, id, state})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, a := range assignments {
		if !desired[a[0]] || a[2] == "revoking" {
			if err = s.revokeGoodMemAssignment(ctx, space, a[0], a[1]); err != nil {
				return err
			}
			continue
		}
		if a[2] == "active" {
			delete(desired, a[0])
		}
	}
	for principal := range desired {
		id := uuid()
		if err = s.db.QueryRowContext(ctx, `INSERT INTO cc_goodmem_access(space_id,principal_id,assignment_id,state) VALUES($1,$2,$3,'pending') ON CONFLICT(space_id,principal_id) DO UPDATE SET state='pending' RETURNING assignment_id`, space, principal, id).Scan(&id); err != nil {
			return err
		}
		_, err = s.gm.JSON(ctx, "POST", "/v1/access-policy/role-assignments", M{"roleAssignmentId": id, "principalId": principal, "role": "SPACE_VIEWER", "assignedResource": M{"kind": "SPACE", "resourceId": space}})
		if goodMemStatus(err, 409) {
			actual, e := s.gm.JSON(ctx, "GET", "/v1/access-policy/role-assignments/"+id, nil)
			if e != nil {
				return e
			}
			if str(actual, "principalId") != principal || str(object(actual["assignedResource"]), "resourceId") != space || actual["revokedAt"] != nil {
				return errors.New("GoodMem role assignment does not match pending grant")
			}
			err = nil
		}
		if err != nil {
			return err
		}
		if _, err = s.db.ExecContext(ctx, `UPDATE cc_goodmem_access SET state='active' WHERE assignment_id=$1`, id); err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `UPDATE cc_goodmem_spaces SET dirty=false WHERE space_id=$1`, space)
	return err
}

func (s *Server) retireGoodMemUser(ctx context.Context, user string) error {
	// Mark first: if the reply is lost, never reuse a potentially dead identity.
	rows, err := s.db.QueryContext(ctx, `UPDATE cc_goodmem_subjects SET retired=true WHERE user_id=$1 RETURNING principal_id`, user)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		_, err = s.gm.JSON(ctx, "DELETE", "/v1/service-identities/"+id, nil)
		if err != nil && !goodMemStatus(err, 404) {
			return err
		}
	}
	return nil
}

func (s *Server) repairGoodMemAccess(ctx context.Context) error {
	if s.cfg.GoodMemSecurity == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return s.withGoodMemLock(ctx, func(ctx context.Context) error {
		spaces, err := s.goodMemSpaces(ctx, nil, true)
		if err != nil {
			return err
		}
		for _, space := range spaces {
			if err = s.reconcileGoodMemSpace(ctx, space); err != nil {
				return err
			}
		}
		return nil
	})
}

// Access-changing endpoints revoke before modifying SQL and grant only after the
// handler commits. Failures leave a durable dirty marker and safe undergrant;
// retries/maintenance reconcile against committed state, never a stale job copy.
func (s *Server) goodMemMutation(pattern string, q request, fn func(request) (any, error)) (any, error) {
	if s.cfg.GoodMemSecurity == nil {
		return fn(q)
	}
	var selector string
	var args []any
	retire := ""
	id := q.r.PathValue("id")
	switch pattern {
	case "POST /api/v1/matters/{id}/members", "DELETE /api/v1/matters/{id}/members/{user}", "DELETE /api/v1/matters/{id}":
		if err := s.checkMatter(q.r.Context(), q.user.ID, id, true); err != nil {
			return nil, err
		}
		selector, args = "o.matter_id=$1", []any{id}
	case "PATCH /api/v1/documents/{id}", "DELETE /api/v1/documents/{id}":
		doc, err := s.document(q)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(pattern, "PATCH") {
			err = own(q, doc)
		} else {
			err = s.canManageDocument(q.r.Context(), q.user.ID, doc)
		}
		if err != nil {
			return nil, err
		}
		selector, args = "o.id=$1", []any{id}
	case "POST /api/v1/matters/{id}/documents/move":
		if err := s.checkMatter(q.r.Context(), q.user.ID, id, false); err != nil {
			return nil, err
		}
		// Only documents the caller could move are suspended, including sources
		// outside the destination matter. Invalid moves cannot affect strangers.
		selector, args = "o.owner_id=$1 OR EXISTS(SELECT 1 FROM cc_matters m WHERE m.id=o.matter_id AND m.owner_id=$1)", []any{q.user.ID}
	case "DELETE /api/v1/admin/users/{id}", "PATCH /api/v1/users/{id}", "PATCH /api/v1/admin/users/{id}/active":
		if err := permit(q, "admin.users"); err != nil {
			return nil, err
		}
		if id == q.user.ID {
			return nil, bad("You cannot change your own account here")
		}
		selector, args = "o.owner_id=$1 OR EXISTS(SELECT 1 FROM cc_matters m WHERE m.id=o.matter_id AND (m.owner_id=$1 OR EXISTS(SELECT 1 FROM cc_matter_members mm WHERE mm.matter_id=m.id AND mm.user_id=$1)))", []any{id}
		if strings.HasPrefix(pattern, "DELETE") {
			retire = id
		} else {
			b, err := io.ReadAll(io.LimitReader(q.r.Body, 16<<20))
			if err != nil {
				return nil, err
			}
			q.r.Body = io.NopCloser(bytes.NewReader(b))
			m := M{}
			if json.Unmarshal(b, &m) != nil {
				return nil, bad("Invalid JSON body")
			}
			if m["active"] == false || m["is_active"] == false {
				retire = id
			}
		}
	default:
		if pattern != "POST /api/v1/settings/clear" {
			return fn(q)
		}
		selector, args = "o.owner_id=$1", []any{q.user.ID}
	}
	var result any
	err := s.withGoodMemLock(q.r.Context(), func(ctx context.Context) error {
		q.r = q.r.WithContext(ctx)
		docs, err := s.goodMemDocumentIDs(ctx, selector, args...)
		if err != nil {
			return err
		}
		if err = s.suspendGoodMemSpaces(ctx, docs); err != nil {
			return err
		}
		if retire != "" {
			if err = s.retireGoodMemUser(ctx, retire); err != nil {
				return err
			}
		}
		result, err = fn(q)
		// Even rejected mutations must restore the still-valid, committed ACL.
		reconcileErr := s.reconcileGoodMemSpaces(ctx, docs)
		if err != nil {
			return err
		}
		if reconcileErr != nil {
			return fail(503, "The change was saved, but document access synchronization is pending. It will retry automatically.")
		}
		return nil
	})
	return result, err
}
