// Package db — Postgres connection + migrations + LISTEN/NOTIFY helpers.
//
// Spec §4.2 / §5: apid, schedd, imaged, builderd (M6) all share the same
// Postgres cluster. This package owns the connection lifecycle and the
// notification channels the daemons use to coordinate without direct calls
// (CLAUDE.md §Component ownership: "components talk via Postgres rows +
// pg_notify, or gRPC on unix sockets").
//
// Migrations are baked into the binary via embed.FS and applied on startup
// with goose; the schema is the source of truth in migrations/*.sql.
package db

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open dials Postgres and returns a connection pool. DSN precedence:
//  1. $DATABASE_URL
//  2. $FAAS_DATABASE_URL
//  3. default `postgres:///faas?host=/run/postgresql&user=faas` (peer auth,
//     matches the ansible postgres role).
func Open(ctx context.Context, dsnOverride string) (*pgxpool.Pool, error) {
	dsn := dsnOverride
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		dsn = os.Getenv("FAAS_DATABASE_URL")
	}
	if dsn == "" {
		dsn = "postgres:///faas?host=/run/postgresql&user=faas"
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}
	// Sane defaults for a one-box daemon. schedd + imaged may subscribe via
	// LISTEN which holds a connection for the lifetime of the daemon.
	cfg.MaxConns = 8
	cfg.MinConns = 1
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: open pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}
