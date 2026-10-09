//go:build !no_pg

// adr: 829
package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestAutomationFailureAlertMigrationReplayAndRollback(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	ctx := t.Context()
	migrateUpOnce(ctx, t, pool)
	raw, err := migrations.FS.ReadFile("20261009205602232_automation_failure_alert.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing down migration")
	}
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
	}
	var metric, comparison, window, plan string
	var threshold float64
	var cooldown, count int
	if err := pool.QueryRow(ctx, `SELECT metric,comparison,threshold,window_spec,default_cooldown_minutes,minimum_plan FROM alert_presets WHERE name='automation_failures'`).Scan(&metric, &comparison, &threshold, &window, &cooldown, &plan); err != nil {
		t.Fatal(err)
	}
	if metric != "workflow_failures" || comparison != "gte" || threshold != api.WorkflowFailureAlertThreshold || window != "5m" || cooldown != api.WorkflowFailureAlertCooldownMinutes || plan != "hobby" {
		t.Fatalf("unexpected defaults: %s %s %v %s %d %s", metric, comparison, threshold, window, cooldown, plan)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM alert_presets WHERE name='automation_backlog'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("existing preset changed: %d %v", count, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE alert_presets SET default_cooldown_minutes=45 WHERE name='automation_failures'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT default_cooldown_minutes FROM alert_presets WHERE name='automation_failures'`).Scan(&cooldown); err != nil || cooldown != 45 {
		t.Fatalf("replay overwrote configuration: %d %v", cooldown, err)
	}

	account := seedAccount(t, ctx, pool)
	app := seedApp(t, ctx, pool, account)
	if _, err := pool.Exec(ctx, `INSERT INTO alert_rules(account_id,app_id,name,metric,comparison,threshold,window_spec,webhook_url,webhook_secret_sealed,action)
 VALUES($1,$2,'failure notification','workflow_failures','gte',1,'5m','https://example.com/hook',$3,'webhook')`, account, app, []byte{0}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM alert_presets WHERE name='automation_failures'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback failed: %d %v", count, err)
	}

	if err := pool.QueryRow(ctx, `SELECT count(*) FROM alert_rules WHERE account_id=$1 AND metric='workflow_failures'`, account).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rollback discarded customer rule: %d %v", count, err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
}
