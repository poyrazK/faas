package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// Goose uses a separate database/sql connection while the migration advisory
// lock stays pinned to the pgx pool. Carry its identity hooks across that
// boundary; registering ConnConfig alone loses the pool's checks (ADR-475).
func migrationSQLDB(cfg *pgxpool.Config) *sql.DB {
	var opts []stdlib.OptionOpenDB
	if cfg.AfterConnect != nil {
		opts = append(opts, stdlib.OptionAfterConnect(cfg.AfterConnect))
	}
	if cfg.PrepareConn != nil {
		opts = append(opts, stdlib.OptionResetSession(func(ctx context.Context, conn *pgx.Conn) error {
			valid, err := cfg.PrepareConn(ctx, conn)
			if err != nil {
				// database/sql ignores ResetSession errors other than ErrBadConn.
				// Discard and recheck on reconnect before any caller SQL runs.
				return errors.Join(driver.ErrBadConn, err)
			}
			if !valid {
				return driver.ErrBadConn
			}
			return nil
		}))
	}
	return stdlib.OpenDB(*cfg.ConnConfig, opts...)
}
