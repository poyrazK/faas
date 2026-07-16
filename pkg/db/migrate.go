package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/onebox-faas/faas/migrations"
)

// MigrateUp applies all pending migrations against the given pool. It is
// idempotent — running it on an already-migrated database is a no-op. Called
// from each daemon's main() at startup so the schema is correct before the
// HTTP / LISTEN loop opens.
//
// Goose is run via the standard database/sql interface rather than pgx
// directly because goose maintains its own connection state and the pgx stdlib
// shim gives us both: pgx's connection pool plus goose's migration runner.
func MigrateUp(ctx context.Context, pool *pgxpool.Pool) error {
	cfg := pool.Config()
	if cfg == nil || cfg.ConnConfig == nil {
		return errors.New("db: MigrateUp: pool has no config")
	}
	connStr := stdlib.RegisterConnConfig(cfg.ConnConfig)
	sqlDB, err := sql.Open("pgx", connStr)
	if err != nil {
		return fmt.Errorf("db: open stdlib: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("db: set goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
		return fmt.Errorf("db: goose up: %w", err)
	}
	return nil
}
