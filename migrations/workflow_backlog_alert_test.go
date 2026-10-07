//go:build !no_pg

// adr: 651
package migrations_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestWorkflowBacklogAlertMigrationPreservesRulesAndRejectsActions(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	ctx := t.Context()
	account := seedAccount(t, ctx, pool)
	app := seedApp(t, ctx, pool, account)
	raw, err := migrations.FS.ReadFile("20261007151150594_workflow_backlog_alert.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing down migration")
	}
	const insert = `INSERT INTO alert_rules(account_id,app_id,name,metric,comparison,threshold,window_spec,webhook_url,webhook_secret_sealed,action)
 VALUES($1,$2,$3,$4,'gte',300,'5m','https://example.com/hook',$5,$6)`
	for _, tc := range []struct{ name, metric string }{{"existing workflow rule", "workflow_pending_age_seconds"}, {"backlog rule", "workflow_due_age_seconds"}} {
		if _, err := pool.Exec(ctx, insert, account, app, tc.name, tc.metric, []byte{0}, "webhook"); err != nil {
			t.Fatal(err)
		}
	}
	for _, action := range []string{"rollback", "demote", "promote"} {
		_, err := pool.Exec(ctx, insert, account, app, action, "workflow_due_age_seconds", []byte{0}, action)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "alert_rules_workflow_notification_chk" {
			t.Fatalf("%s permitted or wrong guard: %v", action, err)
		}
	}
	if _, err := pool.Exec(ctx, down); err == nil || !strings.Contains(err.Error(), "remove automation backlog alert rules") {
		t.Fatalf("rollback with customer rule: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM alert_rules WHERE account_id=$1", account).Scan(&count); err != nil || count != 2 {
		t.Fatalf("rollback changed customer rules: %d %v", count, err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM alert_rules WHERE account_id=$1 AND metric='workflow_due_age_seconds'", account); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal("rollback", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM alert_presets WHERE name='automation_backlog'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback retained preset: %d %v", count, err)
	}
	for range 2 {
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatal("reapply/replay", err)
		}
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM alert_presets WHERE name='automation_backlog'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("preset seed is not idempotent: %d %v", count, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM alert_rules WHERE account_id=$1", account).Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration changed existing workflow rule: %d %v", count, err)
	}
}
