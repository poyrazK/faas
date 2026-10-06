//go:build !no_pg

package migrations_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestCheckedProjectReleaseMigrationRoundTrip(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	ctx := context.Background()
	migrateUpOnce(ctx, t, pool)
	source, err := os.ReadFile("20261006072607001_checked_project_release.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("down migration missing")
	}
	for _, sql := range []string{up, down, up, up} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	var definition string
	if err := pool.QueryRow(ctx, "SELECT pg_get_functiondef('authorize_binding_release_graph(uuid,uuid,jsonb)'::regprocedure)").Scan(&definition); err != nil || !strings.Contains(definition, "authorize_binding_release_traffic") {
		t.Fatalf("graph qualification missing: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='app_tasks'::regclass AND conname='app_tasks_binding_verification_check'").Scan(&definition); err != nil || !strings.Contains(definition, "target_deployment_id") {
		t.Fatalf("exact probe constraint missing: %v", err)
	}
}
