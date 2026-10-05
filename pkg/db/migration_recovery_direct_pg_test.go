package db

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	faasschema "github.com/onebox-faas/faas"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/migrationsqlc"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestApplicationStandardLedgerRecoveryUsesDirectDatabase(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("ledger recovery acceptance needs an explicit DATABASE_URL")
	}
	if _, err := exec.LookPath("pg_dump"); err != nil {
		t.Skip("ledger recovery acceptance needs pg_dump")
	}
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	ordinary, direct := pgtest.OpenMigrated(t), pgtest.OpenMigrated(t)
	registerDirectPool(ordinary, direct)
	defer func() { directMu.Lock(); delete(directPools, ordinary); directMu.Unlock() }()
	ctx := context.Background()
	var version int
	if err := direct.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version/10000 != 16 {
		t.Skip("ledger recovery is explicitly limited to PostgreSQL 16")
	}
	sources, err := migrations.ApplicationStandardRecoverySources()
	if err != nil {
		t.Fatal(err)
	}
	versions := make([]int64, 0, len(sources))
	for id := range sources {
		versions = append(versions, id)
	}
	if _, err := direct.Exec(ctx, "DELETE FROM goose_db_version WHERE version_id=ANY($1)", versions); err != nil {
		t.Fatal(err)
	}
	if _, err := ordinary.Exec(ctx, "ALTER TABLE application_standard_versions ALTER COLUMN definition DROP NOT NULL"); err != nil {
		t.Fatal(err)
	}
	plan, err := PreviewApplicationStandardLedgerRecovery(ctx, ordinary)
	if err != nil {
		t.Fatalf("direct database recovery: %v; %s", err, recoverySchemaDiagnostic(t, ctx, direct))
	}
	if _, err := ApplyApplicationStandardLedgerRecovery(ctx, ordinary, plan.ApprovalHash); err != nil {
		t.Fatal(err)
	}
}

// Diagnostics use only the freshly migrated, private test database. Production
// recovery still requires byte-identical canonical schema and approved writers.
func recoverySchemaDiagnostic(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err.Error()
	}
	defer func() { _ = tx.Rollback(ctx) }()
	writers, err := migrationsqlc.New().CheckMigrationRecoveryWriters(ctx, tx)
	if err != nil || !writers.Valid || !writers.Bool {
		return fmt.Sprintf("writers=%+v err=%v", writers, err)
	}
	snapshot, err := migrationsqlc.New().ExportMigrationRecoverySnapshot(ctx, tx)
	if err != nil {
		return err.Error()
	}
	raw, err := migrationRecoverySchema(ctx, pool.Config().ConnConfig, snapshot)
	if err != nil {
		return err.Error()
	}
	actual, expected := strings.Split(string(raw), "\n"), strings.Split(faasschema.CanonicalSQL(), "\n")
	for i := 0; i < len(actual) && i < len(expected); i++ {
		if actual[i] != expected[i] {
			return fmt.Sprintf("schema line %d: got %.240q; want %.240q", i+1, actual[i], expected[i])
		}
	}
	return fmt.Sprintf("schema line counts: got %d want %d", len(actual), len(expected))
}
