package db

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"path/filepath"
	"regexp"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"

	"github.com/onebox-faas/faas/migrations"
)

// reservationMigrationFilenameRe preserves the ADR-041 convention for the
// frozen legacy set. A reservation is a deliberate no-op fence, so recording
// a missing historical reservation is safe; a real legacy migration must
// continue to fail closed.
var reservationMigrationFilenameRe = regexp.MustCompile(`(?i)^[0-9]{5}_(.*_)?(reservation|reserve_slot)(_[^/]*)?\.sql$`)

// undefinedTableSQLState is Postgres's code for "relation does not exist".
const undefinedTableSQLState = "42P01"

// errNoLedgerYet reports whether err means goose_db_version is not there.
//
// A database with no ledger has no migration history, and this whole file
// exists to reconcile history: it compares the ledger against the migration
// files to spot versions goose would reject as missing. With no ledger there
// is nothing to reconcile, so the answer is "no options" — exactly what the
// current <= 0 branch already returns for a fresh database.
//
// Treating the absence as a failure instead turned a legitimate state into a
// migration error:
//
//	migrate: db: inspect migration ledger: ERROR: relation "goose_db_version"
//	does not exist (SQLSTATE 42P01)
//
// which failed pg-shard tests intermittently (for example
// TestPgReconcile_RemoveAndReaddRestoresIdentity in CI run 35052140508)
// against a database that goose would have initialised moments later —
// goose.UpContext creates the ledger itself and then applies every migration.
//
// This does not hide a ledger that disappears mid-migration: goose still
// recreates it and replays, and replaying against a schema that already has
// the objects fails loudly through annotateSchemaDrift.
func errNoLedgerYet(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == undefinedTableSQLState
}

// logNoLedger records that the reconcile pre-flight found no ledger.
//
// Proceeding is correct, but it must not be silent. A brand-new database hits
// this once and goose initialises it; the same line appearing against a
// database that HAS been migrated means the ledger went missing, which is
// worth investigating even though the migration recovers.
func logNoLedger(err error) {
	log.Printf("db: no migration ledger yet; skipping history reconcile and letting goose initialise it (%v)", err)
}

// missingHistoricalMigrations returns the migration files that Goose would
// reject as missing before the database's current version. The known set
// intentionally includes every row, not only is_applied=true rows, matching
// Goose's own findMissingMigrations behaviour.
func missingHistoricalMigrations(current int64, known map[int64]struct{}, found goose.Migrations) goose.Migrations {
	missing := make(goose.Migrations, 0)
	for _, migration := range found {
		if migration.Version >= current {
			continue
		}
		if _, ok := known[migration.Version]; ok {
			continue
		}
		missing = append(missing, migration)
	}
	return missing
}

func isReservationMigrationSource(source string) bool {
	return reservationMigrationFilenameRe.MatchString(filepath.Base(source))
}

// effectiveMigrationVersion returns the highest migration that is currently
// applied. Goose's GetDBVersionContext returns the most recently inserted
// applied ledger row. After an allow-missing run applies older timestamp
// migrations, that row can be lower than migrations which remain applied.
// Goose's Up path compares gaps against the numeric maximum, so our decision
// to enable allow-missing must use the same boundary.
func effectiveMigrationVersion(reported int64, applied map[int64]struct{}) int64 {
	current := reported
	for version := range applied {
		if version > current {
			current = version
		}
	}
	return current
}

// migrationOptionsForHistoricalGaps enables Goose's out-of-order mode only
// for explicitly safe namespaces:
//   - legacy no-op reservation files, preserving the pre-cutover repair path;
//   - timestamp migrations at or after ADR-142's cutover marker.
//
// A missing real migration from the frozen 1..590 range keeps Goose's strict
// failure path. This prevents the new concurrency model from silently
// replaying old migrations that were not authored under its replay-safe
// contract.
func migrationOptionsForHistoricalGaps(current int64, known map[int64]struct{}, found goose.Migrations) (goose.OptionsFunc, []int64) {
	missing := missingHistoricalMigrations(current, known, found)
	if len(missing) == 0 {
		return nil, nil
	}

	versions := make([]int64, 0, len(missing))
	for _, migration := range missing {
		if !isReservationMigrationSource(migration.Source) && !migrations.IsTimestampMigrationVersion(migration.Version) {
			return nil, nil
		}
		versions = append(versions, migration.Version)
	}
	return goose.WithAllowMissing(), versions
}

// historicalMigrationOption reads the database ledger and returns an
// allow-missing option only when every historical gap is allowed by
// migrationOptionsForHistoricalGaps. The caller must hold MigrationLockKey.
func historicalMigrationOption(ctx context.Context, sqlDB *sql.DB, ledger string) (goose.OptionsFunc, []int64, error) {
	reportedCurrent, err := goose.GetDBVersionContext(ctx, sqlDB)
	if err != nil {
		if errNoLedgerYet(err) {
			logNoLedger(err)
			return nil, nil, nil
		}
		return nil, nil, err
	}
	applied, err := appliedMigrationVersions(ctx, sqlDB, ledger)
	if err != nil {
		if errNoLedgerYet(err) {
			logNoLedger(err)
			return nil, nil, nil
		}
		return nil, nil, err
	}
	current := effectiveMigrationVersion(reportedCurrent, applied)
	if current <= 0 {
		return nil, nil, nil
	}

	known, err := ledgerMigrationVersions(ctx, sqlDB, ledger)
	if err != nil {
		if errNoLedgerYet(err) {
			logNoLedger(err)
			return nil, nil, nil
		}
		return nil, nil, err
	}

	found, err := goose.CollectMigrations(".", 0, current)
	if err != nil {
		return nil, nil, err
	}
	option, allowed := migrationOptionsForHistoricalGaps(current, known, found)
	return option, allowed, nil
}

func ledgerMigrationVersions(ctx context.Context, sqlDB *sql.DB, ledger string) (map[int64]struct{}, error) {
	return migrationVersions(ctx, sqlDB, ledger, false)
}

func appliedMigrationVersions(ctx context.Context, sqlDB *sql.DB, ledger string) (map[int64]struct{}, error) {
	return migrationVersions(ctx, sqlDB, ledger, true)
}

// migrationVersions reads the ledger goose was pointed at (ledgerTableName),
// never an unqualified name that could resolve to another schema's table.
func migrationVersions(ctx context.Context, sqlDB *sql.DB, ledger string, appliedOnly bool) (map[int64]struct{}, error) {
	query := `SELECT version_id FROM ` + ledger
	if appliedOnly {
		// Goose records both apply and rollback events. Only the newest event
		// for each version describes its current state; an older true row must
		// not hide a later rollback.
		query = `SELECT version_id
			FROM (
				SELECT DISTINCT ON (version_id) version_id, is_applied
				FROM ` + ledger + `
				ORDER BY version_id, id DESC
			) AS latest
			WHERE is_applied = true`
	}
	rows, err := sqlDB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	versions := make(map[int64]struct{})
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		versions[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return versions, nil
}
