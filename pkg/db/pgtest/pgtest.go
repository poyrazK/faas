// Package pgtest — shared test helper for spinning up an isolated Postgres
// schema against the same cluster CI / dev uses. Each test gets its own
// schema so parallel runs don't collide and tear-down is a DROP SCHEMA.
//
// Skip with t.Skip if $DATABASE_URL is unset — that lets the foundation PR
// land without forcing every developer to have Postgres running locally.
package pgtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// Open returns a connected pool pointing at a fresh schema. Caller must defer
// Cleanup(t, pool) to drop it.
func Open(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres:///faas?host=/run/postgresql&user=faas"
	}
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set; skipping Postgres integration test")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Skipf("pgtest: cannot parse DATABASE_URL (%v); skipping", err)
	}
	ctx := context.Background()
	schema := randomSchema(t)

	// Bootstrap connection on the default search_path just to create the test
	// schema. We can't use the final pool for this because that pool pins its
	// search_path to a schema that does not exist yet.
	boot, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Skipf("pgtest: cannot connect to Postgres (%v); skipping", err)
	}
	if err := boot.Ping(ctx); err != nil {
		boot.Close()
		t.Skipf("pgtest: Postgres not reachable (%v); skipping", err)
	}
	if _, err := boot.Exec(ctx, fmt.Sprintf("create schema %s", schema)); err != nil {
		boot.Close()
		t.Fatalf("pgtest: create schema: %v", err)
	}
	// Install the migrations' extensions in public (shared, idempotent) so every
	// isolated test schema resolves them via search_path=schema,public. Creating
	// them per-schema instead would register the extension against a schema we
	// later drop, hiding the type from the next test (and racing across packages
	// that share the cluster).
	//
	// pg_extension's unique constraint on name races when two packages install
	// concurrently — the CREATE EXTENSION IF NOT EXISTS does an internal
	// existence check + insert that isn't atomic against itself. Hold a
	// session-scoped advisory lock keyed on the extension name so the second
	// caller waits for the first to commit; the IF NOT EXISTS makes the
	// second call a no-op on retry. Review finding on PR #1205 / pg shard 2b
	// flake (2026-08-30).
	if err := installExtensionOnce(ctx, boot, "citext", "public"); err != nil {
		boot.Close()
		t.Fatalf("pgtest: install citext: %v", err)
	}
	boot.Close()

	// Real pool: every connection defaults its search_path to the isolated
	// schema (extension types like citext still resolve from public). This is
	// what makes goose migrate into the test schema and each test's rows land
	// there — pgx honours RuntimeParams, unlike the PGOPTIONS env var.
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pgtest: open isolated pool: %v", err)
	}
	t.Cleanup(func() {
		// Drop the schema on a fresh connection, not the returned pool: a
		// daemon under test may own the returned pool's lifecycle and have
		// already closed it (a closed pool can't run the DROP, which would
		// otherwise leak the schema and its extensions into the next test).
		if c, cerr := pgxpool.NewWithConfig(ctx, cfg.Copy()); cerr == nil {
			_, _ = c.Exec(ctx, fmt.Sprintf("drop schema %s cascade", schema))
			c.Close()
		}
		// Best-effort close of the returned pool; tolerate an already-closed
		// pool (double Close panics in pgx).
		func() {
			defer func() { _ = recover() }()
			pool.Close()
		}()
	})
	return pool
}

// OpenSQL is the stdlib counterpart for packages that need a *sql.DB
// (goose, sqlx-style code).
func OpenSQL(t *testing.T) *sql.DB {
	t.Helper()
	pool := Open(t)
	cfg := pool.Config()
	if cfg == nil || cfg.ConnConfig == nil {
		t.Fatal("pgtest.OpenSQL: pool has no config")
	}
	connStr := stdlib.RegisterConnConfig(cfg.ConnConfig)
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		t.Fatalf("pgtest.OpenSQL: sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func randomSchema(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("pgtest: rand: %v", err)
	}
	return "faas_test_" + hex.EncodeToString(b[:])
}

// installExtensionOnce creates ext in schema (typically "public") and is safe
// under parallel callers. pg_extension has a unique constraint on name, and
// the `CREATE EXTENSION IF NOT EXISTS` path is two SQL operations (select +
// insert) that race against itself when two test packages on the same cluster
// install at once — the second call hits 23505 ("duplicate key value violates
// unique constraint pg_extension_name_index"). Serialise on a session-scoped
// advisory lock keyed off a stable hash of (ext, schema), then retry once if
// the constraint still trips. Lock releases on session close so we don't leak
// it across the cluster.
func installExtensionOnce(ctx context.Context, pool *pgxpool.Pool, ext, schema string) error {
	lockID := int64(0xfa05_0001) ^ int64(hashKey(ext+":"+schema))
	if _, err := pool.Exec(ctx, "select pg_advisory_lock($1)", lockID); err != nil {
		return err
	}
	defer func() { _, _ = pool.Exec(ctx, "select pg_advisory_unlock($1)", lockID) }()
	if _, err := pool.Exec(ctx, fmt.Sprintf("create extension if not exists %s schema %s", ext, schema)); err != nil {
		// 23505 = unique_violation. The race is a 1-in-N transient — retry once
		// after a short sleep and assume the other caller committed.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			time.Sleep(50 * time.Millisecond)
			if _, err2 := pool.Exec(ctx, fmt.Sprintf("create extension if not exists %s schema %s", ext, schema)); err2 == nil {
				return nil
			} else {
				return err2
			}
		}
		return err
	}
	return nil
}

// hashKey is FNV-1a 64-bit — used to derive an int64 advisory-lock key from
// a short string. We don't need crypto strength; collisions across the
// handful of (ext, schema) pairs in pgtest are not a problem because the
// lock is only contention-fencing, not a security boundary.
func hashKey(s string) uint64 {
	const offset uint64 = 0xcbf29ce484222325
	const prime uint64 = 0x100000001b3
	h := offset
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

// SchemaOf returns the search_path the pool was opened with (the per-test
// schema), or "" if the pool was opened without one. Lets harness code pass
// the same schema to subprocess daemons so they share the test's tables.
func SchemaOf(pool *pgxpool.Pool) string {
	if pool == nil {
		return ""
	}
	cfg := pool.Config()
	if cfg == nil || cfg.ConnConfig == nil {
		return ""
	}
	return cfg.ConnConfig.RuntimeParams["search_path"]
}

// WaitForMigration blocks until goose_db_version on the given pool has
// reached targetVersion (or beyond), or fails the test after deadline.
//
// Call this AFTER db.MigrateUp has been invoked so the version table exists.
// The cmd/e2e meterd test (issue #52 acceptance) uses it to gate daemon
// subprocess boot on schema arrival — without this, the daemon's first
// tick races the migration and crashes with 'relation "accounts" does
// not exist' on CI's postgres15 service. See memory
// cmd-e2e-schedd-migration-race for the race this fixes.
func WaitForMigration(t *testing.T, pool *pgxpool.Pool, targetVersion int64, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for {
		var got int64
		err := pool.QueryRow(context.Background(),
			"SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = true",
		).Scan(&got)
		if err == nil && got >= targetVersion {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("pgtest: migration version %d not reached within %s (last got=%d err=%v)",
				targetVersion, deadline, got, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
