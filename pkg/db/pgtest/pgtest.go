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
	"fmt"
	"os"
	"testing"

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
	if _, err := boot.Exec(ctx, "create extension if not exists citext schema public"); err != nil {
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
