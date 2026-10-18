package acceptance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
)

func DatabaseIdentity(dsn string) (string, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return "", errors.New("invalid dedicated CI acceptance database configuration")
	}
	// Bind the journal to a database without persisting its password or DSN.
	value := cfg.ConnConfig.Host + "\x00" + strconv.Itoa(int(cfg.ConnConfig.Port)) + "\x00" + cfg.ConnConfig.Database + "\x00" + cfg.ConnConfig.User
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:]), nil
}

func authorityDigest(j Journal) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{j.RunID, j.Schema, j.DatabaseID, j.SourceSHA, j.RunnerSHA, j.GuestSHA, j.Bucket, j.Image, j.Origin}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func schemaPool(ctx context.Context, dsn string, j Journal) (*pgxpool.Pool, error) {
	id, err := DatabaseIdentity(dsn)
	if err != nil || !j.Valid() || id != j.DatabaseID {
		return nil, errors.New("CI acceptance database does not match the recovery journal")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid CI acceptance database configuration")
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = make(map[string]string)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = j.Schema + ",public"
	cfg.MaxConns = 12
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("CI acceptance database unavailable")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("CI acceptance database unavailable")
	}
	return pool, nil
}

// CreateSchema owns test-only DDL. Normal intent and lifecycle mutations use
// the production stores. The marker and schema commit together before migration.
func CreateSchema(ctx context.Context, dsn string, j Journal) (*pgxpool.Pool, error) {
	pool, err := schemaPool(ctx, dsn, j)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			pool.Close()
		}
	}()
	failed := func(err error) (*pgxpool.Pool, error) { return nil, err }
	var migrated, extension bool
	if err := pool.QueryRow(ctx, "SELECT to_regclass('public.goose_db_version') IS NOT NULL, EXISTS(SELECT 1 FROM pg_extension WHERE extname='citext')").Scan(&migrated, &extension); err != nil || migrated || !extension {
		return failed(errors.New("CI acceptance requires a dedicated test database with citext in public and no public migration ledger"))
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return failed(errors.New("CI acceptance schema creation failed"))
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The identifier is derived from our random UUID, never an operator path.
	if _, err := tx.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{j.Schema}.Sanitize()); err != nil {
		return failed(errors.New("CI acceptance schema creation failed"))
	}
	if _, err := tx.Exec(ctx, "CREATE TABLE ci_acceptance_owner (run_id uuid PRIMARY KEY, journal_digest text NOT NULL)"); err != nil {
		return failed(errors.New("CI acceptance owner marker creation failed"))
	}
	if _, err := tx.Exec(ctx, "INSERT INTO ci_acceptance_owner VALUES($1,$2)", j.RunID, authorityDigest(j)); err != nil {
		return failed(errors.New("CI acceptance owner marker creation failed"))
	}
	if err := tx.Commit(ctx); err != nil {
		return failed(errors.New("CI acceptance schema commit unavailable; retain the journal"))
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		return failed(errors.New("CI acceptance migrations failed; retain the owned schema"))
	}
	success = true
	return pool, nil
}

func OpenSchema(ctx context.Context, dsn string, j Journal) (*pgxpool.Pool, error) {
	pool, err := schemaPool(ctx, dsn, j)
	if err != nil {
		return nil, err
	}
	var id, digest string
	if err := pool.QueryRow(ctx, "SELECT run_id::text,journal_digest FROM "+pgx.Identifier{j.Schema, "ci_acceptance_owner"}.Sanitize()).Scan(&id, &digest); err != nil || id != j.RunID || digest != authorityDigest(j) {
		pool.Close()
		return nil, errors.New("CI acceptance schema ownership cannot be confirmed")
	}
	return pool, nil
}

func DropSchema(ctx context.Context, dsn string, j Journal) error {
	if !j.Cleanup || !j.NativeLeaks {
		return errors.New("CI acceptance schema must retain authority until cleanup and native leak checks pass")
	}
	probe, err := schemaPool(ctx, dsn, j)
	if err != nil {
		return err
	}
	var exists bool
	err = probe.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)", j.Schema).Scan(&exists)
	probe.Close()
	if err != nil {
		return errors.New("CI acceptance schema retirement lookup failed")
	}
	if !exists {
		return nil // A previous owned DROP may have lost its acknowledgement.
	}
	pool, err := OpenSchema(ctx, dsn, j)
	if err != nil {
		return err
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{j.Schema}.Sanitize()+" CASCADE")
	if err != nil {
		return errors.New("CI acceptance schema retirement failed")
	}
	return nil
}
