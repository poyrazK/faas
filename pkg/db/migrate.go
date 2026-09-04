package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgconn"
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
// Multi-host safety (ADR-124): before opening the goose shim, the caller
// holds a session-scoped pg_advisory_lock(MigrationLockKey) on a pinned
// pool connection. Every daemon in the fleet that calls MigrateUp blocks
// on the same lock; only one migration runs at a time. Without this, two
// daemons racing the same fresh database both see "no row at v=N", both
// run the same migration, and the second one crashes on the version
// INSERT's unique-key violation — fleet bootstrap becomes a Russian
// roulette of panic logs.
//
// PR-2 audit: every MigrateUp call site in the repo goes through this
// function (and therefore acquires the lock). The six known sites are:
//
//	cmd/builderd/main.go      runDeps.migrate = db.MigrateUp
//	cmd/meterd/main.go        runDeps.migrate = db.MigrateUp
//	cmd/schedd/main.go        runDeps.migrate = db.MigrateUp
//	cmd/apid/main.go          db.MigrateUp(ctx, pool)  (direct)
//	cmd/schema-dump/main.go   db.MigrateUp(ctx, pool)  (direct; always acquires)
//	cmd/imaged/main.go        runDeps.migrate closure wrapping db.MigrateUp
//	cmd/migrate/main.go       db.MigrateUp(ctx, pool)  (-leader mode + default)
//
// The only path that deliberately bypasses MigrateUp is `cmd/migrate
// -status`, which calls db.Status (read-only ledger query) and never
// opens the goose shim. That path is the "I just want to know the
// current version without holding the lock" surface used by CI
// pre-checks; it MUST NOT acquire the lock (otherwise a CI step that
// runs in parallel with a fleet bootstrap would deadlock).
//
// Goose is run via the standard database/sql interface rather than pgx
// directly because goose maintains its own connection state and the pgx stdlib
// shim gives us both: pgx's connection pool plus goose's migration runner.
func MigrateUp(ctx context.Context, pool *pgxpool.Pool) error {
	cfg := pool.Config()
	if cfg == nil || cfg.ConnConfig == nil {
		return errors.New("db: MigrateUp: pool has no config")
	}

	// Pin a pool connection and hold the migration lock for the lifetime
	// of the goose run. The lock is released before this function returns
	// (defer below). AcquireMigrationLock can fail if the pool is full or
	// the network is gone — both are operator-actionable errors, not silent
	// ones, so we surface the wrap directly.
	release, err := AcquireMigrationLock(ctx, pool)
	if err != nil {
		return fmt.Errorf("db: acquire migration lock: %w", err)
	}
	defer func() { _ = release(ctx) }() //nolint:errcheck // PG auto-releases on conn close; logged at call sites

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
	option, outOfOrder, err := historicalMigrationOption(ctx, sqlDB)
	if err != nil {
		return fmt.Errorf("db: inspect migration ledger: %w", err)
	}
	if len(outOfOrder) > 0 {
		log.Printf("db: applying permitted out-of-order migrations: %v", outOfOrder)
	}
	var options []goose.OptionsFunc
	if option != nil {
		options = append(options, option)
	}
	if err := goose.UpContext(ctx, sqlDB, ".", options...); err != nil {
		return fmt.Errorf("db: goose up: %w", annotateSchemaDrift(err))
	}
	return nil
}

// duplicateObjectSQLStates are the Postgres error codes that mean "the thing
// this migration is trying to create is already there". Applying a migration
// to a database whose schema is AHEAD of its goose ledger produces exactly
// these, and nothing else does — a genuine SQL bug in a new migration fails
// with a syntax or constraint error, not a duplicate-object error.
//
// https://www.postgresql.org/docs/current/errcodes-appendix.html
var duplicateObjectSQLStates = map[string]string{
	"42P07": "relation (table/index/view)",
	"42701": "column",
	"42P06": "schema",
	"42710": "object (constraint/type/role)",
	"42723": "function",
}

// SchemaDriftError marks a migration failure caused by the database schema
// having drifted AHEAD of goose's ledger, rather than by a bad migration.
//
// This is the failure that took cd-digitalocean red twice — 00030_invocations
// and 00053_deployments_source_url — and both times the raw error read as an
// application bug:
//
//	ERROR: column "source_url" of relation "deployments" already exists (SQLSTATE 42701)
//
// It is neither. It means someone applied DDL outside goose (manual psql, a
// restored dump, a partially-rolled-back deploy), so goose_db_version does
// not record a migration whose effects are already present. CI never sees it
// because CI always migrates a FRESH database.
type SchemaDriftError struct {
	SQLState string
	Kind     string
	Err      error
}

func (e *SchemaDriftError) Error() string {
	return fmt.Sprintf(
		"schema/ledger drift: this migration creates a %s that already exists (SQLSTATE %s). "+
			"The database schema is AHEAD of goose_db_version — DDL was applied outside goose "+
			"(manual psql, restored dump, or a partially-applied deploy), so goose is replaying "+
			"work that is already done. This is NOT a bug in the migration: it applies cleanly to "+
			"a fresh database, which is why CI is green. "+
			"Reconcile the ledger on the target database rather than editing the migration "+
			"(migrations are append-only). Inspect with `migrate -status`, then record the "+
			"already-applied version with `goose -no-versioning` or a direct INSERT INTO "+
			"goose_db_version. Underlying error: %v",
		e.Kind, e.SQLState, e.Err)
}

func (e *SchemaDriftError) Unwrap() error { return e.Err }

// annotateSchemaDrift wraps a duplicate-object failure in a SchemaDriftError
// so the operator gets the diagnosis instead of a bare SQLSTATE. Any other
// error passes through untouched.
func annotateSchemaDrift(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	kind, ok := duplicateObjectSQLStates[pgErr.Code]
	if !ok {
		return err
	}
	return &SchemaDriftError{SQLState: pgErr.Code, Kind: kind, Err: err}
}

// MigrationStatus is a point-in-time view of the target database's schema
// version against the binary's embedded migration set.
type MigrationStatus struct {
	// DBVersion is the highest version recorded in goose_db_version. Zero
	// on a database that has never been migrated.
	DBVersion int64
	// MaxEmbedded is the highest migration version compiled into this binary.
	// It is diagnostic only: after ADR-142, a high maximum does not prove that
	// every lower timestamp migration has been applied.
	MaxEmbedded int64
	// EmbeddedVersions is the complete migration set required by this binary.
	// Waiters compare this set against the ledger instead of comparing maxima.
	EmbeddedVersions []int64
	// Pending names the migrations that MigrateUp would apply, in order.
	Pending []string
}

// Status reports what MigrateUp would do, without doing it.
//
// The deploy runs this before applying so the CI log always records the
// before-state. When a migration then fails, the log shows whether the box
// was where it was expected to be — which is the first question worth asking
// and previously took an SSH session to answer.
func Status(ctx context.Context, pool *pgxpool.Pool) (MigrationStatus, error) {
	cfg := pool.Config()
	if cfg == nil || cfg.ConnConfig == nil {
		return MigrationStatus{}, errors.New("db: Status: pool has no config")
	}
	connStr := stdlib.RegisterConnConfig(cfg.ConnConfig)
	sqlDB, err := sql.Open("pgx", connStr)
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("db: open stdlib: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return MigrationStatus{}, fmt.Errorf("db: set goose dialect: %w", err)
	}

	// GetDBVersionContext creates goose_db_version if absent, which is what
	// MigrateUp would do anyway — Status stays read-only in every other
	// respect.
	dbVersion, err := goose.GetDBVersionContext(ctx, sqlDB)
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("db: goose db version: %w", err)
	}

	collected, err := goose.CollectMigrations(".", 0, math.MaxInt64)
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("db: collect migrations: %w", err)
	}
	applied, err := appliedMigrationVersions(ctx, sqlDB)
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("db: list applied migrations: %w", err)
	}

	return migrationStatusFrom(dbVersion, collected, applied), nil
}

func migrationStatusFrom(dbVersion int64, collected goose.Migrations, applied map[int64]struct{}) MigrationStatus {
	st := MigrationStatus{DBVersion: dbVersion}
	for _, m := range collected {
		st.EmbeddedVersions = append(st.EmbeddedVersions, m.Version)
		if m.Version > st.MaxEmbedded {
			st.MaxEmbedded = m.Version
		}
		if _, ok := applied[m.Version]; !ok {
			st.Pending = append(st.Pending, filepath.Base(m.Source))
		}
	}
	return st
}
