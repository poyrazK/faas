package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessPagerDutyFallbackMigration(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	raw, err := os.ReadFile("20261017181315159_mcp_readiness_pagerduty_fallback.sql")
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
	release, readErr := os.ReadFile("20261017181315189_mcp_readiness_pagerduty_release.sql")
	if readErr != nil {
		t.Fatal(readErr)
	}
	exec(strings.Split(string(release), "-- +goose Down")[1])
	independent, err := os.ReadFile("20261017181315175_mcp_readiness_pagerduty_independent_bindings.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(strings.Split(string(independent), "-- +goose Down")[1])
	exec(parts[1])
	var account, app, rule, incident string
	if err = tx.QueryRow(ctx, `INSERT INTO accounts(email,plan) VALUES('native-fallback-migration@example.com','pro') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO apps(account_id,slug,ram_mb) VALUES($1,'native-fallback-migration',256) RETURNING id::text`, account).Scan(&app); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO alert_rules(account_id,app_id,name,metric,comparison,threshold,window_spec,action,webhook_url,webhook_secret_sealed) VALUES($1,$2,'native-fallback','mcp_readiness_unhealthy','gt',0,'5m','webhook','https://example.com/hook','fixture') RETURNING id::text`, account, app).Scan(&rule); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO mcp_readiness_fallback_policies(rule_id,version,enabled,webhook_url,webhook_secret_sealed,after_failures) VALUES($1,7,true,'https://example.com/fallback','sealed',3)`, rule)
	exec(`INSERT INTO mcp_readiness_pagerduty_acknowledgements(rule_id,version,enabled,signing_secret_sealed,pagerduty_version,region,fingerprint,service_id) VALUES($1,2,true,'sealed',1,'eu',repeat('a',64),'PSVC001')`, rule)
	exec(parts[0])
	exec(parts[0])
	var provider, region, destination string
	var version, fallbackVersion int64
	if err = tx.QueryRow(ctx, `SELECT provider,region,version FROM mcp_readiness_fallback_policies WHERE rule_id=$1`, rule).Scan(&provider, &region, &version); err != nil || provider != "webhook" || region != "" || version != 7 {
		t.Fatal(provider, region, version, err)
	}
	if err = tx.QueryRow(ctx, `SELECT destination,fallback_version FROM mcp_readiness_pagerduty_acknowledgements WHERE rule_id=$1`, rule).Scan(&destination, &fallbackVersion); err != nil || destination != "primary" || fallbackVersion != 0 {
		t.Fatal(destination, fallbackVersion, err)
	}
	exec(parts[1])
	exec(parts[0]) // Legacy configurations can roll back without losing their data.
	if err = tx.QueryRow(ctx, `INSERT INTO mcp_readiness_incidents(id,rule_id,account_id,app_id,scope,coverage,opened_at) VALUES(gen_random_uuid(),$1,$2,$3,'staging',repeat('a',64),now()) RETURNING id::text`, rule, account, app).Scan(&incident); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{
		`UPDATE mcp_readiness_fallback_policies SET provider='pagerduty',webhook_url='',webhook_secret_sealed='',region='eu',integration_key_sealed='sealed',fingerprint=repeat('b',64) WHERE rule_id=$1`,
		`UPDATE mcp_readiness_incidents SET delivery_routing=jsonb_build_object('fallback',jsonb_build_object('provider','pagerduty','url','https://events.eu.pagerduty.com/v2/enqueue','receiver_id',repeat('b',64),'secret','c2VhbGVk')) WHERE rule_id=$1`,
		`INSERT INTO mcp_readiness_incident_events(id,incident_id,rule_id,kind,occurred_at,payload,observed_value,available_at,destination_deliveries) SELECT gen_random_uuid(),id,rule_id,'alert.fired',now(),'{}',1,now(),'{"fallback":{"provider":"pagerduty"}}' FROM mcp_readiness_incidents WHERE rule_id=$1`,
		`UPDATE mcp_readiness_pagerduty_acknowledgements SET destination='fallback',pagerduty_version=0,fallback_version=7 WHERE rule_id=$1`,
		`INSERT INTO mcp_readiness_pagerduty_ack_events(rule_id,event_id,id,incident_id,digest,receipt,received_at) SELECT rule_id,'fallback-event',gen_random_uuid(),id,repeat('a',64),'{"destination":"fallback","outcome":"stale"}',now() FROM mcp_readiness_incidents WHERE rule_id=$1`,
	} {
		exec(`SAVEPOINT protected`)
		exec(change, rule)
		if _, err = tx.Exec(ctx, parts[1]); err == nil {
			t.Fatal("rollback discarded native fallback state")
		}
		exec(`ROLLBACK TO SAVEPOINT protected`)
	}
	for _, change := range []string{
		`UPDATE mcp_readiness_fallback_policies SET provider='other' WHERE rule_id=$1`,
		`UPDATE mcp_readiness_fallback_policies SET provider='pagerduty',region='eu',integration_key_sealed='sealed',fingerprint=repeat('b',64) WHERE rule_id=$1`,
		`UPDATE mcp_readiness_fallback_policies SET provider='pagerduty',webhook_url='',webhook_secret_sealed='',region='ap',integration_key_sealed='sealed',fingerprint=repeat('b',64) WHERE rule_id=$1`,
		`UPDATE mcp_readiness_pagerduty_acknowledgements SET destination='fallback' WHERE rule_id=$1`,
		`UPDATE mcp_readiness_incidents SET delivery_routing='{"fallback":{"provider":"pagerduty","url":"https://evil.example/hook"}}' WHERE rule_id=$1`,
	} {
		exec(`SAVEPOINT invalid`)
		if _, err = tx.Exec(ctx, change, rule); err == nil {
			t.Fatal("invalid fallback state accepted")
		}
		exec(`ROLLBACK TO SAVEPOINT invalid`)
	}
}
