package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessPagerDutyIndependentBindingsMigration(t *testing.T) {
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
	raw, err := os.ReadFile("20261017181315175_mcp_readiness_pagerduty_independent_bindings.sql")
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
	exec(parts[1])
	var account, app, rule string
	if err = tx.QueryRow(ctx, `INSERT INTO accounts(email,plan) VALUES('independent-bindings@example.com','pro') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO apps(account_id,slug,ram_mb) VALUES($1,'independent-bindings',256) RETURNING id::text`, account).Scan(&app); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO alert_rules(account_id,app_id,name,metric,comparison,threshold,window_spec,action,webhook_url,webhook_secret_sealed) VALUES($1,$2,'independent-bindings','mcp_readiness_unhealthy','gt',0,'5m','webhook','https://example.com/hook','fixture') RETURNING id::text`, account, app).Scan(&rule); err != nil {
		t.Fatal(err)
	}
	// Preserve an existing fallback receiver URL, signing key, and CAS version.
	exec(`INSERT INTO mcp_readiness_pagerduty_acknowledgements(rule_id,version,enabled,signing_secret_sealed,pagerduty_version,region,fingerprint,service_id,destination,fallback_version) VALUES($1,7,true,'legacy-secret',0,'eu',repeat('a',64),'PFALLBACK','fallback',4)`, rule)
	exec(parts[0])
	exec(parts[0])
	var destination, secret string
	var version int64
	var legacy bool
	if err = tx.QueryRow(ctx, `SELECT destination,version,legacy_receiver,convert_from(signing_secret_sealed,'UTF8') FROM mcp_readiness_pagerduty_acknowledgements WHERE rule_id=$1`, rule).Scan(&destination, &version, &legacy, &secret); err != nil || destination != "fallback" || version != 7 || !legacy || secret != "legacy-secret" {
		t.Fatal(destination, version, legacy, err)
	}
	exec(parts[1])
	exec(parts[0])
	exec(`INSERT INTO mcp_readiness_pagerduty_acknowledgements(rule_id,version,enabled,signing_secret_sealed,pagerduty_version,region,fingerprint,service_id,destination) VALUES($1,1,true,'primary-secret',1,'us',repeat('b',64),'PPRIMARY','primary')`, rule)
	for _, bad := range []string{
		`UPDATE mcp_readiness_pagerduty_acknowledgements SET legacy_receiver=true WHERE rule_id=$1 AND destination='primary'`,
		`UPDATE mcp_readiness_pagerduty_acknowledgements SET destination='other' WHERE rule_id=$1 AND destination='primary'`,
		`UPDATE mcp_readiness_pagerduty_acknowledgements SET destination='fallback' WHERE rule_id=$1 AND destination='primary'`,
	} {
		exec(`SAVEPOINT invalid`)
		if _, err = tx.Exec(ctx, bad, rule); err == nil {
			t.Fatal("invalid independent binding accepted")
		}
		exec(`ROLLBACK TO SAVEPOINT invalid`)
	}
	exec(`SAVEPOINT rollback_guard`)
	if _, err = tx.Exec(ctx, parts[1]); err == nil {
		t.Fatal("rollback discarded independent bindings")
	}
	exec(`ROLLBACK TO SAVEPOINT rollback_guard`)
	exec(`UPDATE mcp_readiness_pagerduty_acknowledgements SET enabled=false,version=8,signing_secret_sealed='',fallback_version=0,region='',fingerprint='',service_id='' WHERE rule_id=$1 AND destination='fallback'`, rule)
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM mcp_readiness_pagerduty_acknowledgements WHERE rule_id=$1`, rule).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	// Even the lone legacy fallback tombstone cannot be represented by the old schema.
	exec(`DELETE FROM mcp_readiness_pagerduty_acknowledgements WHERE rule_id=$1 AND destination='primary'`, rule)
	exec(`SAVEPOINT disabled_guard`)
	if _, err = tx.Exec(ctx, parts[1]); err == nil {
		t.Fatal("rollback discarded disabled destination version")
	}
	exec(`ROLLBACK TO SAVEPOINT disabled_guard`)
}
