package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"regexp"

	"github.com/pressly/goose/v3"
)

// reservationMigrationFilenameRe mirrors the reservation convention used by
// migrations/embed_test.go and scripts/ci/check_migration_slots.sh. A
// reservation is a deliberate no-op fence, so recording a missing historical
// reservation is safe; a real migration must continue to fail closed.
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

// migrationOptionsForMissingReservations detects the narrow historical-gap
// case that can be repaired automatically. Goose's WithAllowMissing option
// is safe only when every missing file is a no-op reservation; using it for a
// real schema migration could apply DDL out of order. A nil option preserves
// Goose's normal fail-closed behaviour for every other gap.
func migrationOptionsForMissingReservations(current int64, known map[int64]struct{}, found goose.Migrations) (goose.OptionsFunc, []int64) {
	missing := missingHistoricalMigrations(current, known, found)
	if len(missing) == 0 {
		return nil, nil
	}

	versions := make([]int64, 0, len(missing))
	for _, migration := range missing {
		if !isReservationMigrationSource(migration.Source) {
			return nil, nil
		}
		versions = append(versions, migration.Version)
	}
	return goose.WithAllowMissing(), versions
}

// reservationMigrationOption reads the database ledger and returns an
// allow-missing option only when all historical gaps are no-op reservations.
// The caller must hold MigrationLockKey before invoking this function.
func reservationMigrationOption(ctx context.Context, sqlDB *sql.DB) (goose.OptionsFunc, []int64, error) {
	current, err := goose.GetDBVersionContext(ctx, sqlDB)
	if err != nil {
		return nil, nil, err
	}
	if current <= 0 {
		return nil, nil, nil
	}

	rows, err := sqlDB.QueryContext(ctx, `SELECT version_id FROM goose_db_version`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	known := make(map[int64]struct{})
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, nil, err
		}
		known[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	found, err := goose.CollectMigrations(".", 0, current)
	if err != nil {
		return nil, nil, err
	}
	option, repaired := migrationOptionsForMissingReservations(current, known, found)
	return option, repaired, nil
}
