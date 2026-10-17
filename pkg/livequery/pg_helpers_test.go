package livequery

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// openLogicalDB creates a fresh database on the server named by
// LIVEQUERY_DATABASE_URL (or DATABASE_URL) and skips unless that server runs
// wal_level=logical. Each test gets its own database because publications and
// slots are per database.
func openLogicalDB(t *testing.T) (string, *pgxpool.Pool) {
	t.Helper()
	base := os.Getenv("LIVEQUERY_DATABASE_URL")
	if base == "" {
		base = os.Getenv("DATABASE_URL")
	}
	if base == "" {
		t.Skip("livequery: LIVEQUERY_DATABASE_URL or DATABASE_URL not set")
	}
	// CREATE DATABASE can take a while on a loaded server.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Skipf("livequery: connect %s: %v", base, err)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	var level string
	if err := admin.QueryRow(ctx, "SHOW wal_level").Scan(&level); err != nil {
		t.Fatal(err)
	}
	if level != "logical" {
		t.Skipf("livequery: wal_level is %s, need logical", level)
	}
	name := fmt.Sprintf("lq_%d_%d", os.Getpid(), rand.Uint32())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	dsn := u.String()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if admin, err := pgx.Connect(ctx, base); err == nil {
			_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			_ = admin.Close(ctx)
		}
	})
	return dsn, pool
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}
