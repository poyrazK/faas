package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"regexp"

	"github.com/pressly/goose/v3"

	"github.com/onebox-faas/faas/migrations"
)

// reservationMigrationFilenameRe preserves the ADR-041 convention for the
// frozen legacy set. A reservation is a deliberate no-op fence, so recording
// a missing historical reservation is safe; a real legacy migration must
// continue to fail closed.
var reservationMigrationFilenameRe = regexp.MustCompile(`(?i)^[0-9]{5}_(.*_)?(reservation|reserve_slot)(_[^/]*)?\.sql$`)

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
func historicalMigrationOption(ctx context.Context, sqlDB *sql.DB) (goose.OptionsFunc, []int64, error) {
	current, err := goose.GetDBVersionContext(ctx, sqlDB)
	if err != nil {
		return nil, nil, err
	}
	if current <= 0 {
		return nil, nil, nil
	}

	known, err := ledgerMigrationVersions(ctx, sqlDB)
	if err != nil {
		return nil, nil, err
	}

	found, err := goose.CollectMigrations(".", 0, current)
	if err != nil {
		return nil, nil, err
	}
	option, allowed := migrationOptionsForHistoricalGaps(current, known, found)
	return option, allowed, nil
}

func ledgerMigrationVersions(ctx context.Context, sqlDB *sql.DB) (map[int64]struct{}, error) {
	return migrationVersions(ctx, sqlDB, false)
}

func appliedMigrationVersions(ctx context.Context, sqlDB *sql.DB) (map[int64]struct{}, error) {
	return migrationVersions(ctx, sqlDB, true)
}

func migrationVersions(ctx context.Context, sqlDB *sql.DB, appliedOnly bool) (map[int64]struct{}, error) {
	query := `SELECT version_id FROM goose_db_version`
	if appliedOnly {
		// Goose records both apply and rollback events. Only the newest event
		// for each version describes its current state; an older true row must
		// not hide a later rollback.
		query = `SELECT version_id
			FROM (
				SELECT DISTINCT ON (version_id) version_id, is_applied
				FROM goose_db_version
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
