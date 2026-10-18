package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessPagerDutyAcknowledgementsMigration(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	raw, err := os.ReadFile("20261017181315142_mcp_readiness_pagerduty_acknowledgements.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, sql := range []string{parts[0], parts[0], parts[1], parts[0]} {
		exec(sql)
	}
	var account, app, rule, incident string
	if err = tx.QueryRow(ctx, `INSERT INTO accounts(email,plan) VALUES('pd-ack-migration@example.com','pro') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO apps(account_id,slug,ram_mb) VALUES($1,'pd-ack-migration',256) RETURNING id::text`, account).Scan(&app); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO alert_rules(account_id,app_id,name,metric,comparison,threshold,window_spec,action,webhook_url,webhook_secret_sealed) VALUES($1,$2,'ack','mcp_readiness_unhealthy','gt',0,'5m','webhook','https://example.com/hook','fixture') RETURNING id::text`, account, app).Scan(&rule); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO mcp_readiness_incidents(id,rule_id,account_id,app_id,scope,coverage,opened_at) VALUES(gen_random_uuid(),$1,$2,$3,'staging',repeat('a',64),now()) RETURNING id::text`, rule, account, app).Scan(&incident); err != nil {
		t.Fatal(err)
	}
	for _, values := range []string{"0,false,''::bytea,0,'','',''", "1,true,''::bytea,1,'eu',repeat('a',64),'PSVC001'", "1,true,'sealed'::bytea,0,'eu',repeat('a',64),'PSVC001'", "1,true,'sealed'::bytea,1,'ap',repeat('a',64),'PSVC001'", "1,true,'sealed'::bytea,1,'eu','bad','PSVC001'", "1,true,'sealed'::bytea,1,'eu',repeat('a',64),'https://evil.example'", "1,false,'sealed'::bytea,0,'','',''"} {
		exec(`SAVEPOINT invalid`)
		if _, err = tx.Exec(ctx, `INSERT INTO mcp_readiness_pagerduty_acknowledgements(rule_id,version,enabled,signing_secret_sealed,pagerduty_version,region,fingerprint,service_id) VALUES($1,`+values+`)`, rule); err == nil {
			t.Fatal("invalid webhook policy accepted", values)
		}
		exec(`ROLLBACK TO SAVEPOINT invalid`)
	}
	for _, sql := range []string{
		`INSERT INTO mcp_readiness_pagerduty_acknowledgements(rule_id,version,enabled,signing_secret_sealed,pagerduty_version,region,fingerprint,service_id) VALUES($1,1,false,''::bytea,0,'','','')`,
		`INSERT INTO mcp_readiness_pagerduty_ack_events(rule_id,event_id,id,incident_id,digest,receipt,received_at) SELECT rule_id,'event-1',gen_random_uuid(),id,repeat('a',64),'{"outcome":"stale"}',now() FROM mcp_readiness_incidents WHERE id=$1`,
	} {
		exec(`SAVEPOINT history`)
		if strings.Contains(sql, "ack_events") {
			exec(sql, incident)
		} else {
			exec(sql, rule)
		}
		exec(parts[0])
		if _, err = tx.Exec(ctx, parts[1]); err == nil {
			t.Fatal("rollback discarded acknowledgement history")
		}
		exec(`ROLLBACK TO SAVEPOINT history`)
	}
	exec(parts[1])
	exec(parts[0])
}
