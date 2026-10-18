package mailoutbox

import (
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/onebox-faas/faas/migrations"
	"github.com/pressly/goose/v3"
)

// The concurrent index owns its own rollback, which runs before the older
// outbox migration drops the indexed deployment columns.
func TestPGDeploymentFailurePendingIndexMigrationRollbackOrder(t *testing.T) {
	f := newFixture(t)
	const name = "20261017201832304_platform_mail_deployment_failure_pending_index.sql"
	raw, err := migrations.FS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDBFromPool(f.pool)
	defer db.Close()
	db.SetMaxOpenConns(1)
	provider, err := goose.NewProvider(goose.DialectPostgres, db,
		fstest.MapFS{name: &fstest.MapFile{Data: raw}}, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	checkIndex := func() {
		t.Helper()
		var valid bool
		if err := f.pool.QueryRow(t.Context(), `SELECT coalesce((SELECT indisvalid FROM pg_index
			WHERE indexrelid=to_regclass('deployments_mail_failures_pending_idx')),false)`).Scan(&valid); err != nil || !valid {
			t.Fatal("pending failure index is unavailable", valid, err)
		}
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal("concurrent index migration", err)
	}
	checkIndex()
	if _, err := provider.Down(t.Context()); err != nil {
		t.Fatal("concurrent index rollback", err)
	}
	var absent bool
	if err := f.pool.QueryRow(t.Context(), "SELECT to_regclass('deployments_mail_failures_pending_idx') IS NULL").Scan(&absent); err != nil || !absent {
		t.Fatal("index rollback retained the index", absent, err)
	}
	up, down := migrationFileParts(t, deploymentFailedMigration)
	if _, err := f.pool.Exec(t.Context(), down); err != nil {
		t.Fatal("outbox rollback after index rollback", err)
	}
	if _, err := f.pool.Exec(t.Context(), up); err != nil {
		t.Fatal("outbox reapplication", err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal("concurrent index reapplication", err)
	}
	checkIndex()
}
