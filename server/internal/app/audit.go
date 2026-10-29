package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (s *Server) auditMAC(v any) string {
	h := hmac.New(sha256.New, []byte(s.cfg.Secret))
	h.Write([]byte("casecite-audit-v1\x00"))
	h.Write(jsonBytes(v))
	return hex.EncodeToString(h.Sum(nil))
}

type auditHead struct {
	ID, Count int64
	Hash      string
}

func (s *Server) readAuditHead(ctx context.Context, tx *sql.Tx) (auditHead, error) {
	var head auditHead
	var signature string
	err := tx.QueryRowContext(ctx, `SELECT last_id,entry_count,entry_hash,signature FROM cc_audit_head WHERE singleton`).Scan(&head.ID, &head.Count, &head.Hash, &signature)
	if notFound(err) {
		var signed bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cc_audit WHERE record IS NOT NULL)`).Scan(&signed); err != nil {
			return head, err
		}
		if signed {
			return head, fmt.Errorf("signed audit records exist but their audit head is missing")
		}
		return head, nil
	}
	if err != nil {
		return head, err
	}
	if !hmac.Equal([]byte(signature), []byte(s.auditMAC(head))) {
		return head, fmt.Errorf("audit head signature is invalid")
	}
	if head.Count > 0 {
		var hash string
		if err = tx.QueryRowContext(ctx, `SELECT entry_hash FROM cc_audit WHERE id=$1`, head.ID).Scan(&hash); err != nil {
			return head, fmt.Errorf("audit head points to a missing record: %w", err)
		}
		if !hmac.Equal([]byte(hash), []byte(head.Hash)) {
			return head, fmt.Errorf("audit head does not match the last record")
		}
	}
	return head, nil
}

func auditRecord(id int64, user, event, kind, resource string, details M, at time.Time, previous string, legacy bool) M {
	return M{"id": fmt.Sprint(id), "timestamp": at.UTC().Format(time.RFC3339Nano), "event_type": event, "user": M{"id": nullable(user)}, "resource": M{"type": kind, "id": resource}, "action": details, "context": M{}, "outcome": M{"success": true}, "integrity": M{"previous_hash": previous, "hash_version": 1, "legacy": legacy}}
}

func (s *Server) appendAudit(ctx context.Context, user, event, kind, resource string, details M) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(734280004)`); err != nil {
		return err
	}
	head, err := s.readAuditHead(ctx, tx)
	if err != nil {
		return err
	}
	// The pre-parity checkpoint contained unsigned records. Preserve them and
	// explicitly label them legacy; signing now cannot authenticate their past.
	if head.Count == 0 {
		rows, err := tx.QueryContext(ctx, `SELECT id,COALESCE(user_id,''),event_type,resource_type,resource_id,details,created_at FROM cc_audit WHERE record IS NULL ORDER BY id`)
		if err != nil {
			return err
		}
		type pending struct {
			id             int64
			record         M
			hash, previous string
		}
		items := []pending{}
		for rows.Next() {
			var id int64
			var u, e, k, r string
			var raw []byte
			var at time.Time
			if err = rows.Scan(&id, &u, &e, &k, &r, &raw, &at); err != nil {
				rows.Close()
				return err
			}
			var d M
			if err = json.Unmarshal(raw, &d); err != nil {
				rows.Close()
				return err
			}
			record := auditRecord(id, u, e, k, r, d, at, head.Hash, true)
			hash := s.auditMAC(record)
			items = append(items, pending{id, record, hash, head.Hash})
			head = auditHead{id, head.Count + 1, hash}
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, item := range items {
			if _, err = tx.ExecContext(ctx, `UPDATE cc_audit SET record=$1,entry_hash=$2,previous_hash=$3 WHERE id=$4`, jsonBytes(item.record), item.hash, item.previous, item.id); err != nil {
				return err
			}
		}
	}
	var id int64
	var at time.Time
	if err = tx.QueryRowContext(ctx, `INSERT INTO cc_audit(user_id,event_type,resource_type,resource_id,details) VALUES($1,$2,$3,$4,$5) RETURNING id,created_at`, nullable(user), event, kind, resource, jsonBytes(details)).Scan(&id, &at); err != nil {
		return err
	}
	record := auditRecord(id, user, event, kind, resource, details, at, head.Hash, false)
	hash := s.auditMAC(record)
	if _, err = tx.ExecContext(ctx, `UPDATE cc_audit SET record=$1,entry_hash=$2,previous_hash=$3 WHERE id=$4`, jsonBytes(record), hash, head.Hash, id); err != nil {
		return err
	}
	head = auditHead{id, head.Count + 1, hash}
	if _, err = tx.ExecContext(ctx, `INSERT INTO cc_audit_head(singleton,last_id,entry_count,entry_hash,signature) VALUES(true,$1,$2,$3,$4) ON CONFLICT(singleton) DO UPDATE SET last_id=EXCLUDED.last_id,entry_count=EXCLUDED.entry_count,entry_hash=EXCLUDED.entry_hash,signature=EXCLUDED.signature`, head.ID, head.Count, head.Hash, s.auditMAC(head)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) auditLogs(q request) (any, error) {
	if err := permit(q, "admin.audit"); err != nil {
		return nil, err
	}
	args := []any{}
	where := []string{"true"}
	for _, filter := range []struct{ query, column, op string }{{"event_type", "event_type", "="}, {"user_id", "user_id", "="}, {"start_date", "created_at", ">="}, {"end_date", "created_at", "<="}} {
		value := q.r.URL.Query().Get(filter.query)
		if value == "" {
			continue
		}
		if strings.HasSuffix(filter.query, "date") {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				if _, err = time.Parse("2006-01-02", value); err != nil {
					return nil, bad("Dates must be ISO 8601")
				}
			}
		}
		args = append(args, value)
		where = append(where, fmt.Sprintf("%s%s$%d", filter.column, filter.op, len(args)))
	}
	limit := 200
	if n, err := strconv.Atoi(q.r.URL.Query().Get("limit")); err == nil {
		limit = max(1, min(10000, n))
	}
	if strings.HasSuffix(q.r.URL.Path, "/export") {
		limit = 100000
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(q.r.Context(), `SELECT id,COALESCE(user_id,''),event_type,resource_type,resource_id,details,created_at,record,COALESCE(entry_hash,'') FROM cc_audit WHERE `+strings.Join(where, " AND ")+fmt.Sprintf(` ORDER BY id DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	logs := []M{}
	for rows.Next() {
		var id int64
		var u, e, k, r, hash string
		var details, raw []byte
		var at time.Time
		if err = rows.Scan(&id, &u, &e, &k, &r, &details, &at, &raw, &hash); err != nil {
			return nil, err
		}
		var record M
		if len(raw) > 0 {
			if err = json.Unmarshal(raw, &record); err != nil {
				return nil, err
			}
		} else {
			var d M
			if err = json.Unmarshal(details, &d); err != nil {
				return nil, err
			}
			record = auditRecord(id, u, e, k, r, d, at, "", true)
		}
		object(record["integrity"])["entry_hash"] = hash
		logs = append(logs, record)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if strings.HasSuffix(q.r.URL.Path, "/export") {
		q.w.Header().Set("Content-Disposition", `attachment; filename="casecite-audit.json"`)
	}
	return M{"logs": logs, "count": len(logs)}, nil
}

func (s *Server) verifyAudit(q request) (any, error) {
	if err := permit(q, "admin.audit"); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(q.r.Context(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	head, err := s.readAuditHead(q.r.Context(), tx)
	if err != nil {
		return M{"valid": false, "entries_checked": 0, "reason": "Audit head signature is invalid"}, nil
	}
	rows, err := tx.QueryContext(q.r.Context(), `SELECT id,COALESCE(user_id,''),event_type,resource_type,resource_id,details,created_at,record,COALESCE(entry_hash,''),COALESCE(previous_hash,'') FROM cc_audit ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var count, legacy, last int64
	previous := ""
	invalid := func(id int64, reason string) M {
		return M{"valid": false, "entries_checked": count, "first_invalid_id": fmt.Sprint(id), "reason": reason, "legacy_entries": legacy, "truncated": false}
	}
	for rows.Next() {
		var id int64
		var user, event, kind, resource, hash, prev string
		var details, raw []byte
		var at time.Time
		if err = rows.Scan(&id, &user, &event, &kind, &resource, &details, &at, &raw, &hash, &prev); err != nil {
			return nil, err
		}
		count++
		var d, record M
		if json.Unmarshal(details, &d) != nil {
			return invalid(id, "Invalid details"), nil
		}
		if len(raw) == 0 {
			legacy++
			if head.Count == 0 {
				continue
			}
			return invalid(id, "Unsigned record in signed chain"), nil
		}
		if json.Unmarshal(raw, &record) != nil {
			return invalid(id, "Invalid record"), nil
		}
		old, _ := object(record["integrity"])["legacy"].(bool)
		if old {
			legacy++
		}
		expected := auditRecord(id, user, event, kind, resource, d, at, previous, old)
		if prev != previous || !hmac.Equal([]byte(hash), []byte(s.auditMAC(expected))) || !hmac.Equal([]byte(hash), []byte(s.auditMAC(record))) {
			return invalid(id, "Record signature or chain linkage is invalid"), nil
		}
		previous, last = hash, id
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if count > legacy || head.Count > 0 {
		if count != head.Count || last != head.ID || previous != head.Hash {
			return invalid(last, "Audit head does not match stored records; entries may have been removed"), nil
		}
	}
	return M{"valid": true, "entries_checked": count, "legacy_entries": legacy, "truncated": false}, nil
}
