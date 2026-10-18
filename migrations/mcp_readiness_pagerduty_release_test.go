package migrations_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessPagerDutyReleaseMigration(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	raw, err := os.ReadFile("20261017181315189_mcp_readiness_pagerduty_release.sql")
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
	exec(parts[1])
	var account, app, rule, incident string
	if err = tx.QueryRow(ctx, `INSERT INTO accounts(email,plan) VALUES('release-migration@example.com','pro') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO apps(account_id,slug,ram_mb) VALUES($1,'release-migration',256) RETURNING id::text`, account).Scan(&app); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO alert_rules(account_id,app_id,name,metric,comparison,threshold,window_spec,action,webhook_url,webhook_secret_sealed) VALUES($1,$2,'release','mcp_readiness_unhealthy','gt',0,'5m','webhook','https://example.com/hook','fixture') RETURNING id::text`, account, app).Scan(&rule); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO mcp_readiness_incidents(id,rule_id,account_id,app_id,scope,coverage,opened_at) VALUES(gen_random_uuid(),$1,$2,$3,'staging',repeat('a',64),now()) RETURNING id::text`, rule, account, app).Scan(&incident); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO mcp_readiness_pagerduty_acknowledgements(rule_id,version,enabled,signing_secret_sealed,pagerduty_version,region,fingerprint,service_id,legacy_receiver) VALUES($1,7,true,'sealed',4,'eu',repeat('a',64),'PSVC001',true)`, rule)
	exec(`INSERT INTO mcp_readiness_pagerduty_ack_events(rule_id,event_id,id,incident_id,digest,receipt,received_at) VALUES($1,'legacy',gen_random_uuid(),$2,repeat('a',64),'{"outcome":"acknowledged"}',now())`, rule, incident)
	exec(`INSERT INTO mcp_readiness_incident_attention(incident_id,snapshot) VALUES($1,'{"version":1,"count":0,"acknowledgement":{"source":"pagerduty"}}')`, incident)
	exec(parts[0])
	exec(parts[0])
	var sync bool
	var delivery, digest string
	var version int64
	if err = tx.QueryRow(ctx, `SELECT sync_unacknowledgements,version FROM mcp_readiness_pagerduty_acknowledgements WHERE rule_id=$1`, rule).Scan(&sync, &version); err != nil || sync || version != 7 {
		t.Fatal(sync, version, err)
	}
	if err = tx.QueryRow(ctx, `SELECT delivery_destination,digest FROM mcp_readiness_pagerduty_ack_events WHERE rule_id=$1`, rule).Scan(&delivery, &digest); err != nil || delivery != "" || digest != strings.Repeat("a", 64) {
		t.Fatal(delivery, digest, err)
	}
	exec(parts[1])
	exec(parts[0]) // Default-off upgrade and downgrade preserve legacy data.
	hold := map[string]any{"destination": "primary", "provider_incident_id": "PINC001", "provider_event_id": "provider-ack", "service_id": "PSVC001", "region": "eu", "occurred_at": "2026-10-17T00:00:00Z", "received_at": "2026-10-17T00:00:01Z", "policy_version": 7, "release_sync_enabled": true}
	state := map[string]any{"active": true, "fingerprint": strings.Repeat("a", 64), "last_occurred_at": "2026-10-17T00:00:00Z", "hold": hold}
	snapshot, _ := json.Marshal(map[string]any{"version": 2, "count": 0, "acknowledgement": map[string]any{"source": "pagerduty"}, "pagerduty_tracking": true, "pagerduty_states": map[string]any{"primary": state}})
	for _, change := range []struct {
		sql string
		arg any
	}{
		{`UPDATE mcp_readiness_pagerduty_acknowledgements SET sync_unacknowledgements=true WHERE rule_id=$1`, rule},
		{`UPDATE mcp_readiness_incident_attention SET snapshot=$2 WHERE incident_id=$1`, incident},
		{`INSERT INTO mcp_readiness_pagerduty_ack_events(rule_id,event_id,id,incident_id,digest,receipt,received_at,delivery_destination) SELECT rule_id,'release',gen_random_uuid(),id,repeat('b',64),'{"action":"release","outcome":"released","destination":"primary"}',now(),'primary' FROM mcp_readiness_incidents WHERE id=$1`, incident},
	} {
		exec(`SAVEPOINT guard`)
		if strings.Contains(change.sql, "$2") {
			exec(change.sql, change.arg, snapshot)
		} else {
			exec(change.sql, change.arg)
		}
		if _, err = tx.Exec(ctx, parts[1]); err == nil {
			t.Fatal("release state discarded by rollback")
		}
		exec(`ROLLBACK TO SAVEPOINT guard`)
	}
	for _, invalid := range []string{
		`UPDATE mcp_readiness_pagerduty_acknowledgements SET enabled=false,signing_secret_sealed='',pagerduty_version=0,region='',fingerprint='',service_id='',sync_unacknowledgements=true WHERE rule_id=$1`,
		`UPDATE mcp_readiness_pagerduty_ack_events SET delivery_destination='primary' WHERE rule_id=$1`,
		`UPDATE mcp_readiness_pagerduty_ack_events SET delivery_destination='other' WHERE rule_id=$1`,
	} {
		exec(`SAVEPOINT invalid`)
		if _, err = tx.Exec(ctx, invalid, rule); err == nil {
			t.Fatal("invalid release state accepted")
		}
		exec(`ROLLBACK TO SAVEPOINT invalid`)
	}
	for _, raw := range []string{`{"pagerduty_tracking":"yes"}`, `{"pagerduty_tracking":true,"pagerduty_states":[]}`, `{"pagerduty_tracking":true,"pagerduty_states":{"other":{}}}`, `{"pagerduty_tracking":true,"pagerduty_states":{"primary":{"active":true}}}`} {
		exec(`SAVEPOINT invalid`)
		if _, err = tx.Exec(ctx, `UPDATE mcp_readiness_incident_attention SET snapshot=$2 WHERE incident_id=$1`, incident, raw); err == nil {
			t.Fatal("invalid ownership snapshot accepted", raw)
		}
		exec(`ROLLBACK TO SAVEPOINT invalid`)
	}
	for _, d := range []string{"primary", "fallback"} {
		exec(`INSERT INTO mcp_readiness_pagerduty_ack_events(rule_id,event_id,id,incident_id,digest,receipt,received_at,delivery_destination) VALUES($1,'shared-release',gen_random_uuid(),$2,repeat('b',64),jsonb_build_object('action','release','outcome','retained','destination',$3::text),now(),$3)`, rule, incident, d)
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM mcp_readiness_pagerduty_ack_events WHERE rule_id=$1 AND event_id='shared-release'`, rule).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}
