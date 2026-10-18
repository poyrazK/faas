package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessPagerDutyRegionsMigration(t *testing.T) {
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
	raw, err := os.ReadFile("20261017181315128_mcp_readiness_pagerduty_regions.sql")
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
	if err = tx.QueryRow(ctx, `INSERT INTO accounts(email,plan) VALUES('pagerduty-regions-migration@example.com','pro') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO apps(account_id,slug,ram_mb) VALUES($1,'pagerduty-regions-migration',256) RETURNING id::text`, account).Scan(&app); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO alert_rules(account_id,app_id,name,metric,comparison,threshold,window_spec,action,webhook_url,webhook_secret_sealed) VALUES($1,$2,'regions','mcp_readiness_unhealthy','gt',0,'5m','webhook','https://example.com/hook','fixture') RETURNING id::text`, account, app).Scan(&rule); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO mcp_readiness_incidents(id,rule_id,account_id,app_id,scope,coverage,opened_at) VALUES(gen_random_uuid(),$1,$2,$3,'staging',repeat('a',64),now()) RETURNING id::text`, rule, account, app).Scan(&incident); err != nil {
		t.Fatal(err)
	}
	// A legacy US row is backfilled without altering its version or integration.
	exec(parts[1])
	exec(`INSERT INTO mcp_readiness_pagerduty_policies(rule_id,version,enabled,integration_key_sealed,fingerprint) VALUES($1,7,true,'sealed',repeat('a',64))`, rule)
	exec(parts[0])
	var region, secret string
	var version int64
	if err = tx.QueryRow(ctx, `SELECT region,version,convert_from(integration_key_sealed,'UTF8') FROM mcp_readiness_pagerduty_policies WHERE rule_id=$1`, rule).Scan(&region, &version, &secret); err != nil || region != "us" || version != 7 || secret != "sealed" {
		t.Fatal(region, version, secret, err)
	}
	for _, bad := range []string{"EU", "ap", "", "https://example.com"} {
		exec(`SAVEPOINT invalid_region`)
		if _, err = tx.Exec(ctx, `UPDATE mcp_readiness_pagerduty_policies SET region=$2 WHERE rule_id=$1`, rule, bad); err == nil {
			t.Fatal("invalid region accepted", bad)
		}
		exec(`ROLLBACK TO SAVEPOINT invalid_region`)
	}
	exec(`SAVEPOINT disabled_eu`)
	if _, err = tx.Exec(ctx, `UPDATE mcp_readiness_pagerduty_policies SET enabled=false,integration_key_sealed='',fingerprint='',region='eu' WHERE rule_id=$1`, rule); err == nil {
		t.Fatal("disabled EU accepted")
	}
	exec(`ROLLBACK TO SAVEPOINT disabled_eu`)
	for _, endpoint := range []string{"https://events.pagerduty.com/v2/enqueue", "https://events.eu.pagerduty.com/v2/enqueue"} {
		exec(`UPDATE mcp_readiness_incidents SET delivery_routing=jsonb_build_object('primary',jsonb_build_object('provider','pagerduty','url',$2::text,'receiver_id',repeat('a',64),'secret','c2VhbGVk')) WHERE id=$1`, incident, endpoint)
	}
	for _, endpoint := range []string{"https://events.eu.pagerduty.com/v2/enqueue?leak=1", "http://events.eu.pagerduty.com/v2/enqueue", "https://events.eu.pagerduty.com.evil.example/v2/enqueue"} {
		exec(`SAVEPOINT invalid_route`)
		if _, err = tx.Exec(ctx, `UPDATE mcp_readiness_incidents SET delivery_routing=jsonb_build_object('primary',jsonb_build_object('provider','pagerduty','url',$2::text,'receiver_id',repeat('a',64),'secret','c2VhbGVk')) WHERE id=$1`, incident, endpoint); err == nil {
			t.Fatal("invalid endpoint accepted", endpoint)
		}
		exec(`ROLLBACK TO SAVEPOINT invalid_route`)
	}
	// Even a resolved EU incident retains its regional recovery history on rollback.
	exec(`UPDATE mcp_readiness_incidents SET resolved_at=now() WHERE id=$1`, incident)
	exec(`SAVEPOINT eu_history`)
	if _, err = tx.Exec(ctx, parts[1]); err == nil {
		t.Fatal("rollback discarded EU history")
	}
	exec(`ROLLBACK TO SAVEPOINT eu_history`)
	exec(`UPDATE mcp_readiness_incidents SET delivery_routing='{}' WHERE id=$1`, incident)
	exec(`UPDATE mcp_readiness_pagerduty_policies SET region='eu' WHERE rule_id=$1`, rule)
	exec(`SAVEPOINT eu_config`)
	if _, err = tx.Exec(ctx, parts[1]); err == nil {
		t.Fatal("rollback discarded EU config")
	}
	exec(`ROLLBACK TO SAVEPOINT eu_config`)
	exec(`UPDATE mcp_readiness_pagerduty_policies SET region='us' WHERE rule_id=$1`, rule)
	exec(parts[1])
	exec(parts[0])
	exec(parts[0])
}
