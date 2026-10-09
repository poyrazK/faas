//go:build !no_pg

// adr: 905
package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestAutomationFailurePauseMigrationReplayAndGuardedRollback(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	ctx := t.Context()
	migrateUpOnce(ctx, t, pool)
	raw, err := migrations.FS.ReadFile("20261009212606155_automation_failure_pauses.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing down migration")
	}
	account := seedAccount(t, ctx, pool)
	app := seedApp(t, ctx, pool, account)
	if _, err = pool.Exec(ctx, `INSERT INTO workflow_automation_failure_policies(app_id,name,version,enabled,failure_threshold,min_completed_runs,window_seconds) VALUES($1,'guard',1,true,3,5,300);`, app); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO workflow_automation_failure_guards(app_id,name,generation,paused_at) VALUES($1,'guard',1,clock_timestamp())`, app); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = pool.Exec(ctx, up); err != nil {
			t.Fatal(err)
		}
	}
	var paused bool
	if err = pool.QueryRow(ctx, `SELECT paused_at IS NOT NULL FROM workflow_automation_failure_guards WHERE app_id=$1 AND name='guard'`, app).Scan(&paused); err != nil || !paused {
		t.Fatalf("replay cleared guard: %t %v", paused, err)
	}
	if _, err = pool.Exec(ctx, down); err == nil || !strings.Contains(err.Error(), "resume failure-paused automations") {
		t.Fatalf("downgrade silently resumed work: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE workflow_automation_failure_guards SET paused_at=NULL WHERE app_id=$1`, app); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
}
