package app

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed schema.sql
var schema string

type M = map[string]any

func OpenDB(ctx context.Context, url string) (*sql.DB, error) {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		db.Close()
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(734280001)"); err == nil {
		_, err = tx.ExecContext(ctx, schema)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func jsonBytes(v any) []byte   { b, _ := json.Marshal(v); return b }
func str(m M, k string) string { s, _ := m[k].(string); return s }
func number(m M, k string, fallback int) int {
	if n, ok := m[k].(float64); ok {
		return int(n)
	}
	if n, ok := m[k].(int); ok {
		return n
	}
	return fallback
}
func stringsOf(v any) []string {
	out := []string{}
	switch a := v.(type) {
	case []string:
		return a
	case []any:
		for _, x := range a {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
func object(v any) M {
	m, _ := v.(map[string]any)
	if m == nil {
		return M{}
	}
	return m
}
func nowString() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

const visibleSQL = `(o.owner_id=$1 OR EXISTS(SELECT 1 FROM cc_matters m LEFT JOIN cc_matter_members mm ON mm.matter_id=m.id AND mm.user_id=$1 WHERE m.id=o.matter_id AND (m.owner_id=$1 OR mm.user_id=$1)))`

func (s *Server) objects(ctx context.Context, user, kind string) ([]M, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT o.id,o.owner_id,COALESCE(o.matter_id,''),o.data,o.created_at,o.updated_at FROM cc_objects o WHERE `+visibleSQL+` AND o.kind=$2 ORDER BY o.updated_at DESC`, user, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []M{}
	for rows.Next() {
		m, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanObject(row scanner) (M, error) {
	var id, owner, matter string
	var b []byte
	var created, updated time.Time
	if err := row.Scan(&id, &owner, &matter, &b, &created, &updated); err != nil {
		return nil, err
	}
	m := M{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	m["id"] = id
	m["user_id"] = owner
	m["matter_id"] = nullable(matter)
	m["created_at"] = created.UTC().Format(time.RFC3339Nano)
	m["updated_at"] = updated.UTC().Format(time.RFC3339Nano)
	return m, nil
}

func (s *Server) getObject(ctx context.Context, user, kind, id string) (M, error) {
	return scanObject(s.db.QueryRowContext(ctx, `SELECT o.id,o.owner_id,COALESCE(o.matter_id,''),o.data,o.created_at,o.updated_at FROM cc_objects o WHERE `+visibleSQL+` AND o.kind=$2 AND o.id=$3`, user, kind, id))
}
func (s *Server) saveObject(ctx context.Context, user, kind, id, matter string, m M) error {
	r, err := s.db.ExecContext(ctx, `INSERT INTO cc_objects(kind,id,owner_id,matter_id,data) VALUES($1,$2,$3,$4,$5) ON CONFLICT(kind,id) DO UPDATE SET data=EXCLUDED.data,matter_id=EXCLUDED.matter_id,updated_at=now() WHERE cc_objects.owner_id=EXCLUDED.owner_id`, kind, id, user, nullable(matter), jsonBytes(m))
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if n == 0 {
		return fail(403, "Only the owner may change this item")
	}
	return err
}
func (s *Server) kvGet(ctx context.Context, key string, out any) error {
	var b []byte
	err := s.db.QueryRowContext(ctx, `SELECT value FROM cc_kv WHERE key=$1 AND (expires_at IS NULL OR expires_at>now())`, key).Scan(&b)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
func (s *Server) kvSet(ctx context.Context, key string, v any, ttl time.Duration) error {
	var exp any
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO cc_kv(key,value,expires_at) VALUES($1,$2,$3) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,expires_at=EXCLUDED.expires_at`, key, jsonBytes(v), exp)
	return err
}

// Atomic insert-or-replace-expired for one-time nonces. Never a read-then-write.
func (s *Server) claim(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	r, err := s.db.ExecContext(ctx, `INSERT INTO cc_kv(key,value,expires_at) VALUES($1,'true',now()+$2*interval '1 second') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,expires_at=EXCLUDED.expires_at WHERE cc_kv.expires_at<=now()`, key, int(ttl.Seconds()))
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}
func (s *Server) countWindow(ctx context.Context, key string, ttl time.Duration) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `INSERT INTO cc_kv(key,value,expires_at) VALUES($1,'1',now()+$2*interval '1 second') ON CONFLICT(key) DO UPDATE SET value=CASE WHEN cc_kv.expires_at<=now() THEN '1'::jsonb ELSE to_jsonb((cc_kv.value::text)::int+1) END,expires_at=CASE WHEN cc_kv.expires_at<=now() THEN EXCLUDED.expires_at ELSE cc_kv.expires_at END RETURNING (value::text)::int`, key, int(ttl.Seconds())).Scan(&n)
	return n, err
}
func (s *Server) audit(ctx context.Context, u, event, kind, id string, details M) {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO cc_audit(user_id,event_type,resource_type,resource_id,details) VALUES($1,$2,$3,$4,$5)`, nullable(u), event, kind, id, jsonBytes(details)); err != nil {
		s.log.Error("audit write failed", "error", err)
	}
}
func notFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
