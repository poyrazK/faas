package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessDestinationQueuesMigration(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	raw, err := os.ReadFile("20261017181315090_mcp_readiness_destination_queues.sql")
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
	if err = tx.QueryRow(ctx, `INSERT INTO accounts(email,plan) VALUES('destination-migration@example.com','pro') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO apps(account_id,slug,ram_mb) VALUES($1,'destination-migration',256) RETURNING id::text`, account).Scan(&app); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO alert_rules(account_id,app_id,name,metric,comparison,threshold,window_spec,action,webhook_url,webhook_secret_sealed) VALUES($1,$2,'readiness','mcp_readiness_unhealthy','gt',0,'5m','webhook','https://example.com/hook','fixture') RETURNING id::text`, account, app).Scan(&rule); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO mcp_readiness_incidents(id,rule_id,account_id,app_id,scope,coverage,opened_at) VALUES(gen_random_uuid(),$1,$2,$3,'staging',repeat('a',64),now()) RETURNING id::text`, rule, account, app).Scan(&incident); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO mcp_readiness_incident_events(id,incident_id,rule_id,kind,occurred_at,payload,observed_value,available_at,attempt_count,lease_token,lease_until,delivery_routing) VALUES(gen_random_uuid(),$1,$2,'alert.resolved',now(),'{}',0,now()+interval '5 minutes',2,gen_random_uuid(),now()+interval '2 minutes','{"fallback_delivered":true,"last_destination":"primary"}') RETURNING id::text`, incident, rule).Scan(&event); err != nil {
		t.Fatal(err)
	}
	var before, after string
	const snapshot = `SELECT (jsonb_build_array(lease_token,lease_until,available_at,attempt_count,delivery_routing))::text FROM mcp_readiness_incident_events WHERE id=$1`
	if err = tx.QueryRow(ctx, snapshot, event).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, snapshot, event).Scan(&after); err != nil || before != after {
		t.Fatal("upgrade changed old lease or progress", before, after, err)
	}
	for _, sql := range []string{
		`UPDATE mcp_readiness_incident_events SET destination_deliveries='{"primary":{"status":"failed","attempts":2}}' WHERE id=$1`,
		`INSERT INTO mcp_readiness_notification_actions(app_id,account_id,request_id,event_id,action,expected_version,reason,receipt,destination) SELECT i.app_id,i.account_id,gen_random_uuid(),e.id,'retry',0,'repair','{}','primary' FROM mcp_readiness_incident_events e JOIN mcp_readiness_incidents i ON i.id=e.incident_id WHERE e.id=$1`,
		`INSERT INTO alert_deliveries(rule_id,account_id,app_id,idempotency_key,payload,status,observed_value,fired_at,readiness_failover_reason) SELECT rule_id,account_id,app_id,'queue-blocked','{}','pending',1,now(),'primary_queue_blocked' FROM mcp_readiness_incidents WHERE id=(SELECT incident_id FROM mcp_readiness_incident_events WHERE id=$1)`,
	} {
		if _, err = tx.Exec(ctx, `SAVEPOINT destination_guard`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, sql, event); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, parts[0]); err != nil {
			t.Fatal("live replay", err)
		}
		if _, err = tx.Exec(ctx, parts[1]); err == nil {
			t.Fatal("rollback discarded destination history")
		}
		if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT destination_guard`); err != nil {
			t.Fatal(err)
		}
	}
	for _, invalid := range []string{`[]`, `{"unknown":{}}`} {
		if _, err = tx.Exec(ctx, `SAVEPOINT invalid_destination`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE mcp_readiness_incident_events SET destination_deliveries=$2 WHERE id=$1`, event, invalid); err == nil {
			t.Fatal("invalid destination snapshot accepted", invalid)
		}
		if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT invalid_destination`); err != nil {
			t.Fatal(err)
		}
	}
}
