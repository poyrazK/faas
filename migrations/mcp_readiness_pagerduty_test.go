package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessPagerDutyMigration(t *testing.T) {
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
	raw, err := os.ReadFile("20261017181315116_mcp_readiness_pagerduty.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	for _, sql := range []string{parts[0], parts[0], parts[1], parts[0], parts[0]} {
		if _, err = tx.Exec(ctx, sql); err != nil {
			t.Fatal("replay", err)
		}
	}
	var account, app, rule, incident string
	if err = tx.QueryRow(ctx, `INSERT INTO accounts(email,plan) VALUES('pagerduty-migration@example.com','pro') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO apps(account_id,slug,ram_mb) VALUES($1,'pagerduty-migration',256) RETURNING id::text`, account).Scan(&app); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO alert_rules(account_id,app_id,name,metric,comparison,threshold,window_spec,action,webhook_url,webhook_secret_sealed) VALUES($1,$2,'attention','mcp_readiness_unhealthy','gt',0,'5m','webhook','https://example.com/hook','fixture') RETURNING id::text`, account, app).Scan(&rule); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO mcp_readiness_incidents(id,rule_id,account_id,app_id,scope,coverage,opened_at) VALUES(gen_random_uuid(),$1,$2,$3,'staging',repeat('a',64),now()) RETURNING id::text`, rule, account, app).Scan(&incident); err != nil {
		t.Fatal(err)
	}

	for _, values := range []string{"0,false,''::bytea,''", "1,true,''::bytea,repeat('a',64)", "1,true,'sealed'::bytea,'bad'", "1,false,'sealed'::bytea,''", "1,true,repeat('a',4097)::bytea,repeat('a',64)"} {
		if _, err = tx.Exec(ctx, `SAVEPOINT invalid_policy`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO mcp_readiness_pagerduty_policies(rule_id,version,enabled,integration_key_sealed,fingerprint) VALUES($1,`+values+`)`, rule); err == nil {
			t.Fatal("invalid policy", values)
		}
		if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT invalid_policy`); err != nil {
			t.Fatal(err)
		}
	}
	for _, route := range []string{`{"primary":{"provider":null}}`, `{"primary":{"provider":"unknown"}}`, `{"primary":{"provider":"pagerduty","url":"http://private.local","secret":"sealed","receiver_id":"bad"}}`, `{"primary":{"provider":"pagerduty"}}`} {
		if _, err = tx.Exec(ctx, `SAVEPOINT invalid_route`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE mcp_readiness_incidents SET delivery_routing=$2 WHERE id=$1`, incident, route); err == nil {
			t.Fatal("invalid route", route)
		}
		if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT invalid_route`); err != nil {
			t.Fatal(err)
		}
	}
	for _, record := range []string{
		`INSERT INTO mcp_readiness_pagerduty_policies(rule_id,version,enabled,integration_key_sealed,fingerprint) SELECT rule_id,1,false,''::bytea,'' FROM mcp_readiness_incidents WHERE id=$1`,
		`UPDATE mcp_readiness_incidents SET delivery_routing='{"primary":{"provider":"pagerduty","url":"https://events.pagerduty.com/v2/enqueue","receiver_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","secret":"c2VhbGVk"}}' WHERE id=$1`,
		`INSERT INTO mcp_readiness_incident_events(id,incident_id,rule_id,kind,occurred_at,payload,observed_value,available_at,destination_deliveries) SELECT gen_random_uuid(),id,rule_id,'alert.fired',now(),'{}',1,now(),'{"primary":{"provider":"pagerduty","status":"delivered"}}' FROM mcp_readiness_incidents WHERE id=$1`,
	} {
		if _, err = tx.Exec(ctx, `SAVEPOINT live_history`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, record, incident); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, parts[0]); err != nil {
			t.Fatal("live replay", err)
		}
		if _, err = tx.Exec(ctx, parts[1]); err == nil {
			t.Fatal("rollback discarded provider history")
		}
		if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT live_history`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, parts[1]); err != nil {
		t.Fatal("legacy rollback", err)
	}
}
