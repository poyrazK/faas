//go:build !no_pg

// adr: 20261107190000
package migrations_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestAutomationsBecomeStoredWorkflowsMigration checks the stored values the
// rename rewrites in both directions: webhook filters, outbox event kinds and
// the pause payload key, the capability's platform flag row and the alert
// presets.
func TestAutomationsBecomeStoredWorkflowsMigration(t *testing.T) {
	pool := pgtest.Open(t)
	defer pool.Close()
	ctx := t.Context()
	const version = 20261107145315064
	migrateUpTo(t, ctx, pool, 20261107093030512) // the migration before this one

	account := seedAccount(t, ctx, pool)
	app := seedApp(t, ctx, pool, account)
	var hook string
	if err := pool.QueryRow(ctx, `INSERT INTO app_webhooks(app_id,account_id,target_url,secret_sealed,event_filter)
 VALUES($1,$2,'https://example.com/hook',$3,ARRAY['automation.paused','workflow.finished']) RETURNING id`, app, account, []byte{0}).Scan(&hook); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO app_webhook_event_outbox(account_id,app_id,event,source_id,payload,recipient_webhook_ids)
 VALUES($1,$2,'automation.paused',gen_random_uuid(),'{"automation_name":"nightly","generation":1}',ARRAY[$3::uuid])`, account, app, hook); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO runtime_config_entries(id,config_key,desired_value,apply_mode)
 VALUES(gen_random_uuid(),'platform_flag.automations','{"enabled":true}','hot')`); err != nil {
		t.Fatal(err)
	}

	migrateUpTo(t, ctx, pool, version)
	assertVocabulary(t, pool, hook, "stored_workflow")

	raw, err := migrations.FS.ReadFile("20261107145315064_automations_become_stored_workflows.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing down migration")
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatalf("down: %v", err)
	}
	assertVocabulary(t, pool, hook, "automation")
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatalf("up after down: %v", err)
	}
	assertVocabulary(t, pool, hook, "stored_workflow")
}

// assertVocabulary checks every rewritten value uses word: "automation"
// before the migration, "stored_workflow" after it.
func assertVocabulary(t *testing.T, pool *pgxpool.Pool, hook, word string) {
	t.Helper()
	ctx := t.Context()
	var filter []string
	if err := pool.QueryRow(ctx, `SELECT event_filter FROM app_webhooks WHERE id=$1`, hook).Scan(&filter); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(filter, []string{word + ".paused", "workflow.finished"}) {
		t.Fatalf("event filter = %v, want %s.paused first", filter, word)
	}
	var event string
	var payload []byte
	if err := pool.QueryRow(ctx, `SELECT event,payload FROM app_webhook_event_outbox WHERE $1::uuid = ANY(recipient_webhook_ids)`, hook).Scan(&event, &payload); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatal(err)
	}
	other := map[string]string{"automation": "stored_workflow", "stored_workflow": "automation"}[word]
	if event != word+".paused" || body[word+"_name"] != "nightly" || body[other+"_name"] != nil || body["generation"] != float64(1) {
		t.Fatalf("outbox row = %s %s", event, payload)
	}
	flag := map[string]string{"automation": "automations", "stored_workflow": "stored_workflows"}[word]
	var flags int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM runtime_config_entries WHERE config_key=$1`, "platform_flag."+flag).Scan(&flags); err != nil || flags != 1 {
		t.Fatalf("platform flag %s rows = %d %v", flag, flags, err)
	}
	var presets int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM alert_presets WHERE name IN ($1, $2)`, word+"_backlog", word+"_failures").Scan(&presets); err != nil || presets != 2 {
		t.Fatalf("%s alert presets = %d %v", word, presets, err)
	}
	table := map[string]string{"automation": "workflow_automation_definitions", "stored_workflow": "stored_workflows"}[word]
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
		t.Fatalf("table %s missing: %v", table, err)
	}
}
