package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessReminderDefaultsMigration(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	raw, err := os.ReadFile("20261017181315102_mcp_readiness_reminder_defaults.sql")
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
	if err = tx.QueryRow(ctx, `INSERT INTO accounts(email,plan) VALUES('reminder-defaults-migration@example.com','pro') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO apps(account_id,slug,ram_mb) VALUES($1,'reminder-defaults-migration',256) RETURNING id::text`, account).Scan(&app); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO alert_rules(account_id,app_id,name,metric,comparison,threshold,window_spec,action,webhook_url,webhook_secret_sealed) VALUES($1,$2,'attention','mcp_readiness_unhealthy','gt',0,'5m','webhook','https://example.com/hook','fixture') RETURNING id::text`, account, app).Scan(&rule); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO mcp_readiness_incidents(id,rule_id,account_id,app_id,scope,coverage,opened_at) VALUES(gen_random_uuid(),$1,$2,$3,'staging',repeat('a',64),now()) RETURNING id::text`, rule, account, app).Scan(&incident); err != nil {
		t.Fatal(err)
	}

	for _, values := range []string{"0,60,1", "1,59,1", "1,86401,1", "1,60,11", "1,0,1", "1,60,0"} {
		if _, err = tx.Exec(ctx, `SAVEPOINT invalid_config`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO mcp_readiness_reminder_defaults(rule_id,version,interval_seconds,max_reminders) VALUES($1,`+values+`)`, rule); err == nil {
			t.Fatal("invalid config", values)
		}
		if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT invalid_config`); err != nil {
			t.Fatal(err)
		}
	}
	for _, snapshot := range []string{`{"policy_source":"invalid"}`, `{"policy_source":null}`, `{"defaults_version":null}`, `{"defaults_version":-1}`, `{"policy_source":"rule_default"}`} {
		if _, err = tx.Exec(ctx, `SAVEPOINT invalid_source`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO mcp_readiness_incident_attention(incident_id,snapshot) VALUES($1,$2)`, incident, snapshot); err == nil {
			t.Fatal("invalid source", snapshot)
		}
		if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT invalid_source`); err != nil {
			t.Fatal(err)
		}
	}
	// Both configured defaults and captured version zero block a destructive down.
	for _, record := range []string{
		`INSERT INTO mcp_readiness_reminder_defaults(rule_id,version,interval_seconds,max_reminders) VALUES($1,1,0,0)`,
		`INSERT INTO mcp_readiness_incident_attention(incident_id,snapshot) SELECT id,'{"policy_source":"rule_default","defaults_version":0}' FROM mcp_readiness_incidents WHERE rule_id=$1`,
	} {
		if _, err = tx.Exec(ctx, `SAVEPOINT live_record`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, record, rule); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, parts[0]); err != nil {
			t.Fatal("live replay", err)
		}
		if _, err = tx.Exec(ctx, parts[1]); err == nil {
			t.Fatal("rollback discarded captured defaults")
		}
		if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT live_record`); err != nil {
			t.Fatal(err)
		}
	}
	// Legacy snapshots remain byte-for-byte unchanged and carry no defaults history.
	if _, err = tx.Exec(ctx, `INSERT INTO mcp_readiness_incident_attention(incident_id,snapshot) VALUES($1,'{"version":0,"count":0}')`, incident); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	var unchanged bool
	if err = tx.QueryRow(ctx, `SELECT snapshot='{"version":0,"count":0}'::jsonb FROM mcp_readiness_incident_attention WHERE incident_id=$1`, incident).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal(unchanged, err)
	}
	if _, err = tx.Exec(ctx, parts[1]); err != nil {
		t.Fatal("legacy rollback", err)
	}
}
