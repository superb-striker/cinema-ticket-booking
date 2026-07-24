package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Migrate(ctx context.Context, db *pgxpool.Pool, directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return fmt.Errorf("no SQL migrations found in %s", directory)
	}
	checksums := make(map[string]string, len(names))
	contents := make(map[string][]byte, len(names))
	for _, name := range names {
		body, readErr := os.ReadFile(filepath.Join(directory, name))
		if readErr != nil {
			return readErr
		}
		sum := sha256.Sum256(body)
		checksums[name] = hex.EncodeToString(sum[:])
		contents[name] = body
	}
	conn, err := db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('cinema-schema-migrations'))`); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('cinema-schema-migrations'))`)
	if _, err = conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY, checksum TEXT, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	if _, err = conn.Exec(ctx, `ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum TEXT`); err != nil {
		return err
	}
	// Older releases used the PostgreSQL container init hook. Baseline those two
	// migrations once when their final table proves that they were applied.
	var legacy bool
	if err = conn.QueryRow(ctx, `SELECT to_regclass('public.payment_actions') IS NOT NULL`).Scan(&legacy); err != nil {
		return err
	}
	if legacy {
		if _, err = conn.Exec(ctx, `INSERT INTO schema_migrations(name,checksum) VALUES
			('001_production_foundation.sql',$1),('002_demo_catalog.sql',$2) ON CONFLICT DO NOTHING`,
			checksums["001_production_foundation.sql"], checksums["002_demo_catalog.sql"]); err != nil {
			return err
		}
	}
	for _, name := range names {
		var stored *string
		err = conn.QueryRow(ctx, `SELECT checksum FROM schema_migrations WHERE name=$1`, name).Scan(&stored)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil {
			if stored == nil || *stored == "" {
				if _, err = conn.Exec(ctx, `UPDATE schema_migrations SET checksum=$2 WHERE name=$1`, name, checksums[name]); err != nil {
					return err
				}
			} else if *stored != checksums[name] {
				return fmt.Errorf("applied migration %s has been modified", name)
			}
			continue
		}
		body := contents[name]
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)`, name, checksums[name]); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
	}
	if _, err = conn.Exec(ctx, `ALTER TABLE schema_migrations ALTER COLUMN checksum SET NOT NULL`); err != nil {
		return err
	}
	return nil
}
