package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessAttentionMigration(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	raw, err := os.ReadFile("20261017181315063_mcp_readiness_attention.sql")
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
	if err = tx.QueryRow(ctx, `INSERT INTO accounts(email,plan) VALUES('attention-migration@example.com','pro') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO apps(account_id,slug,ram_mb) VALUES($1,'attention-migration',256) RETURNING id::text`, account).Scan(&app); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO alert_rules(account_id,app_id,name,metric,comparison,threshold,window_spec,action,webhook_url,webhook_secret_sealed) VALUES($1,$2,'attention','mcp_readiness_unhealthy','gt',0,'5m','webhook','https://example.com/hook','fixture') RETURNING id::text`, account, app).Scan(&rule); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO mcp_readiness_incidents(id,rule_id,account_id,app_id,scope,coverage,opened_at) VALUES(gen_random_uuid(),$1,$2,$3,'staging',repeat('a',64),now()) RETURNING id::text`, rule, account, app).Scan(&incident); err != nil {
		t.Fatal(err)
	}
	for _, record := range []string{
		`INSERT INTO mcp_readiness_incident_attention(incident_id,snapshot) VALUES($1,'{"version":1,"count":0}')`,
		`INSERT INTO mcp_readiness_attention_actions(app_id,account_id,request_id,incident_id,request,receipt,created_at) SELECT app_id,account_id,gen_random_uuid(),id,'{}','{}',now() FROM mcp_readiness_incidents WHERE id=$1`,
		`INSERT INTO mcp_readiness_incident_events(id,incident_id,rule_id,kind,occurred_at,payload,observed_value,available_at,notification_reason,reminder_number) SELECT gen_random_uuid(),id,rule_id,'alert.fired',now(),'{}',1,now(),'reminder',1 FROM mcp_readiness_incidents WHERE id=$1`,
	} {
		if _, err = tx.Exec(ctx, `SAVEPOINT record_guard`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, record, incident); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, parts[0]); err != nil {
			t.Fatal("live replay", err)
		}
		if _, err = tx.Exec(ctx, `SAVEPOINT rollback_guard`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, parts[1]); err == nil {
			t.Fatal("rollback discarded attention history")
		}
		if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT record_guard`); err != nil {
			t.Fatal(err)
		}
	}
	for _, snapshot := range []string{`{"version":-1}`, `{"count":11}`, `[]`} {
		if _, err = tx.Exec(ctx, `SAVEPOINT invalid_snapshot`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO mcp_readiness_incident_attention(incident_id,snapshot) VALUES($1,$2)`, incident, snapshot); err == nil {
			t.Fatal("invalid snapshot accepted", snapshot)
		}
		if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT invalid_snapshot`); err != nil {
			t.Fatal(err)
		}
	}
	// Evidence-only snapshots carry no operator decisions and can safely roll back.
	if _, err = tx.Exec(ctx, `INSERT INTO mcp_readiness_incident_attention(incident_id,snapshot) VALUES($1,'{"version":0,"count":0}')`, incident); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[1]); err != nil {
		t.Fatal("evidence-only rollback", err)
	}
}
