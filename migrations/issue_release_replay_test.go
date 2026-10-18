//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 1490, 1498. Issues upgrades retain other products' webhook events, and
// replay must preserve a captured override channel even without retained delivery rows.
func TestIssueReleaseWebhookVocabularyAndRoutingReplay(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	// OpenMigrated falls back to an empty private schema when the template
	// optimization is disabled in the migration-history tier.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var definition string
	if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk'`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"realtime.channel.published", "automation.recovery_polling_status", "issue.frequency_threshold_reached", "issue.tracker_sync.attention", "issue.tracker_sync.recovered"} {
		if !strings.Contains(definition, event) {
			t.Fatalf("Issues migrations removed webhook event %q", event)
		}
	}
	account, app, team, destination, incident := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO accounts(id,email,plan) VALUES($1,$2,'pro')`, account, account+"@example.com")
	exec(`INSERT INTO apps(id,account_id,slug,type,ram_mb,max_concurrency,idle_timeout_s) VALUES($1,$2,$3,'app',256,2,60)`, app, account, "replay-"+app[:8])
	exec(`INSERT INTO app_issue_teams(id,app_id,team_key,name) VALUES($1,$2,'responders','Responders')`, team, app)
	exec(`INSERT INTO issue_team_slack_destinations(id,app_id,account_id,team_id,channel_id,token_sealed) VALUES($1,$2,$3,$4,'C0123456789','sealed'::bytea)`, destination, app, account, team)
	exec(`INSERT INTO issue_team_slack_threads(destination_id,destination_revision,incident_id,channel_id,root_ts) VALUES($1,1,$2,'C1111111111','123.456')`, destination, incident)
	before := replayConstraintFingerprint(t, pool)
	exec(`DELETE FROM goose_db_version WHERE version_id=20261016185908063`)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var channel, root string
	if err := pool.QueryRow(ctx, `SELECT channel_id,root_ts FROM issue_team_slack_threads WHERE destination_id=$1 AND incident_id=$2`, destination, incident).Scan(&channel, &root); err != nil {
		t.Fatal(err)
	}
	if channel != "C1111111111" || root != "123.456" {
		t.Fatalf("replay changed a captured Slack thread: channel=%s root=%s", channel, root)
	}
	if after := replayConstraintFingerprint(t, pool); after != before {
		t.Fatal("routing replay changed existing constraints")
	}
}
