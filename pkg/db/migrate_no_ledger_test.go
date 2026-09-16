package db

// A database with no goose_db_version has no migration history.
//
// historicalMigrationOption exists to reconcile history — it compares the
// ledger against the migration files to find versions goose would reject as
// missing. When the ledger is absent there is nothing to reconcile, so the
// answer is "no options", and goose.UpContext goes on to create the ledger
// and apply every migration.
//
// It used to return the Postgres error instead, turning that legitimate state
// into a migration failure:
//
//	migrate: db: inspect migration ledger: ERROR: relation "goose_db_version"
//	does not exist (SQLSTATE 42P01)
//
// which failed pg-shard tests intermittently against databases goose would
// have initialised moments later (CI run 35052140508,
// TestPgReconcile_RemoveAndReaddRestoresIdentity).

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// sqlDBOnFreshSchema returns a *sql.DB on an isolated, empty schema — no
// tables at all, so no goose_db_version.
func sqlDBOnFreshSchema(t *testing.T) *sql.DB {
	t.Helper()
	pool := pgtest.Open(t)
	cfg := pool.Config()
	if cfg == nil || cfg.ConnConfig == nil {
		t.Fatal("pool has no config")
	}
	sqlDB, err := sql.Open("pgx", stdlib.RegisterConnConfig(cfg.ConnConfig))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB
}

// Guard the premise: the schema really has no ledger, so the tests below are
// exercising the absent-ledger path rather than passing for another reason.
func TestFreshSchemaHasNoLedger(t *testing.T) {
	sqlDB := sqlDBOnFreshSchema(t)

	var n int
	err := sqlDB.QueryRowContext(context.Background(),
		`SELECT count(*) FROM goose_db_version`).Scan(&n)
	if err == nil {
		t.Fatalf("fresh schema already has a goose_db_version with %d rows; "+
			"these tests would not exercise the absent-ledger path", n)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != undefinedTableSQLState {
		t.Fatalf("want SQLSTATE %s (undefined_table), got %v", undefinedTableSQLState, err)
	}
}

func TestMigrationVersions_MissingLedgerIsDetected(t *testing.T) {
	sqlDB := sqlDBOnFreshSchema(t)

	_, err := appliedMigrationVersions(context.Background(), sqlDB, defaultLedgerTable)
	if err == nil {
		t.Skip("appliedMigrationVersions no longer surfaces the missing ledger; " +
			"nothing left for errNoLedgerYet to classify")
	}
	if !errNoLedgerYet(err) {
		t.Errorf("errNoLedgerYet did not recognise %v as a missing ledger, so "+
			"historicalMigrationOption would fail the migration instead of "+
			"letting goose create the table", err)
	}
}

// The behaviour that matters: inspecting a ledger-less database is not an
// error, and reports no history.
func TestHistoricalMigrationOption_ToleratesAMissingLedger(t *testing.T) {
	sqlDB := sqlDBOnFreshSchema(t)

	option, outOfOrder, err := historicalMigrationOption(context.Background(), sqlDB, defaultLedgerTable)
	if err != nil {
		t.Fatalf("inspecting a database with no ledger failed: %v\n"+
			"A database with no goose_db_version has no history to reconcile; "+
			"goose.UpContext creates the ledger and applies every migration.", err)
	}
	if option != nil {
		t.Error("a database with no history asked for out-of-order migration options")
	}
	if len(outOfOrder) != 0 {
		t.Errorf("a database with no history reported %d out-of-order migrations: %v",
			len(outOfOrder), outOfOrder)
	}
}

// errNoLedgerYet must classify only the missing-table case; widening it would
// swallow real migration failures.
func TestErrNoLedgerYet_IgnoresOtherFailures(t *testing.T) {
	cases := map[string]error{
		"nil":                nil,
		"plain error":        errors.New("connection refused"),
		"duplicate column":   &pgconn.PgError{Code: "42701"},
		"undefined column":   &pgconn.PgError{Code: "42703"},
		"insufficient privs": &pgconn.PgError{Code: "42501"},
	}
	for name, err := range cases {
		if errNoLedgerYet(err) {
			t.Errorf("%s was classified as a missing ledger and would be swallowed", name)
		}
	}
	if !errNoLedgerYet(&pgconn.PgError{Code: undefinedTableSQLState}) {
		t.Error("the undefined_table case was not recognised")
	}
}
