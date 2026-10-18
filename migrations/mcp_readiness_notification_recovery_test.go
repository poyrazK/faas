package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessNotificationRecoveryMigration(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	raw, err := os.ReadFile("20261017181315046_mcp_readiness_notification_recovery.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	for _, sql := range []string{parts[0], parts[0], parts[1], parts[0], parts[0]} {
		if _, err = tx.Exec(ctx, sql); err != nil {
			t.Fatal("replay", err)
		}
	}
	var account, app, rule, incident, event string
	if err = tx.QueryRow(ctx, `INSERT INTO accounts(email,plan) VALUES('notification-migration@example.com','pro') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO apps(account_id,slug,ram_mb) VALUES($1,'notification-migration',256) RETURNING id::text`, account).Scan(&app); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO alert_rules(account_id,app_id,name,metric,comparison,threshold,window_spec,action,webhook_url,webhook_secret_sealed) VALUES($1,$2,'notification','mcp_readiness_unhealthy','gt',0,'5m','webhook','https://example.com/hook','fixture') RETURNING id::text`, account, app).Scan(&rule); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO mcp_readiness_incidents(id,rule_id,account_id,app_id,scope,coverage,opened_at) VALUES(gen_random_uuid(),$1,$2,$3,'staging',repeat('a',64),now()) RETURNING id::text`, rule, account, app).Scan(&incident); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO mcp_readiness_incident_events(id,incident_id,rule_id,kind,occurred_at,payload,observed_value,available_at,delivery_status,delivery_version,last_operator_action) VALUES(gen_random_uuid(),$1,$2,'alert.fired',now(),'{}',1,now(),'abandoned',3,'{"reason":"retired"}') RETURNING id::text`, incident, rule).Scan(&event); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO mcp_readiness_notification_actions(app_id,account_id,request_id,event_id,action,expected_version,reason,receipt) VALUES($1,$2,gen_random_uuid(),$3,'abandon',2,'retired','{}')`, app, account, event); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal("live replay", err)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT rollback_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[1]); err == nil {
		t.Fatal("rollback discarded recovery history")
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT rollback_guard`); err != nil {
		t.Fatal(err)
	}
	var version int64
	if err = tx.QueryRow(ctx, `SELECT delivery_version FROM mcp_readiness_incident_events WHERE id=$1`, event).Scan(&version); err != nil || version != 3 {
		t.Fatal("replay changed version", version, err)
	}
}
