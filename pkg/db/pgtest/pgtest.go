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
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Skipf("pgtest: cannot connect to Postgres (%v); skipping", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("pgtest: Postgres not reachable (%v); skipping", err)
	}
	schema := randomSchema(t)
	if _, err := pool.Exec(context.Background(), fmt.Sprintf("create schema %s", schema)); err != nil {
		pool.Close()
		t.Fatalf("pgtest: create schema: %v", err)
	}
	t.Setenv("PGOPTIONS", fmt.Sprintf("--search_path=%s,public", schema))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf("drop schema %s cascade", schema))
		pool.Close()
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
