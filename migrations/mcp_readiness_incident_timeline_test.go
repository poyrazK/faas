package migrations_test

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/pressly/goose/v3"
)

func TestMCPReadinessIncidentTimelineMigration(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	const file = "20261017181315007_mcp_readiness_incident_timeline.sql"
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB := stdlib.OpenDB(*pool.Config().ConnConfig)
	defer func() { _ = sqlDB.Close() }()
	// Use Goose itself: concurrent index builds cannot run in the transaction
	// used by ordinary migration replay tests, or in a multi-statement Exec.
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB,
		fstest.MapFS{file: &fstest.MapFile{Data: raw}},
		goose.WithTableName("mcp_timeline_index_test_versions"), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "DELETE FROM mcp_timeline_index_test_versions WHERE version_id<>0"); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal("replay:", err)
	}
	var def string
	var valid bool
	if err = pool.QueryRow(ctx, `SELECT pg_get_indexdef(indexrelid), indisvalid AND indisready FROM pg_index WHERE indexrelid='alert_deliveries_mcp_readiness_timeline_idx'::regclass`).Scan(&def, &valid); err != nil || !valid || !strings.Contains(def, "incident_id") {
		t.Fatal(def, valid, err)
	}

	// Reproduce the artifact left by a cancelled/failed concurrent index build.
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `CREATE TABLE mcp_timeline_index_conflict (value integer); INSERT INTO mcp_timeline_index_conflict VALUES (1),(1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `CREATE UNIQUE INDEX CONCURRENTLY alert_deliveries_mcp_readiness_timeline_idx ON mcp_timeline_index_conflict(value)`); err == nil {
		t.Fatal("expected duplicate values to leave an invalid concurrent index")
	}
	if _, err = provider.Up(ctx); err == nil || !strings.Contains(err.Error(), "readiness timeline index is invalid") {
		t.Fatal("invalid index must not be accepted:", err)
	}
	if pending, err := provider.HasPending(ctx); err != nil || !pending {
		t.Fatal("failed migration was recorded:", pending, err)
	}
	if _, err = pool.Exec(ctx, `DROP INDEX CONCURRENTLY alert_deliveries_mcp_readiness_timeline_idx`); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal("retry after cleanup:", err)
	}
}
