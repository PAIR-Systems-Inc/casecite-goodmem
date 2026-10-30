package app

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Called under the schema advisory lock in the same transaction as bootstrap.
// Applied migrations are immutable, including across application rollbacks.
func migrate(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS cc_migrations(name text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	files, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, file := range files {
		b, err := migrations.ReadFile("migrations/" + file.Name())
		if err != nil {
			return err
		}
		var checksum string
		err = tx.QueryRowContext(ctx, `SELECT checksum FROM cc_migrations WHERE name=$1`, file.Name()).Scan(&checksum)
		if err == nil {
			if checksum != digest(string(b)) {
				return fmt.Errorf("applied migration %s was modified", file.Name())
			}
			continue
		}
		if !notFound(err) {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(b)); err != nil {
			return fmt.Errorf("migration %s: %w", file.Name(), err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO cc_migrations(name,checksum) VALUES($1,$2)`, file.Name(), digest(string(b))); err != nil {
			return err
		}
	}
	return nil
}
