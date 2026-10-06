//go:build !no_pg

package migrations_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 616
// adr: 617
// A schema ahead of its migration ledger must preserve compacted delivery
// evidence and an already populated routing projection when the tail replays.
func TestMigrationsEventRoutingReadModelsReplayPreservesEvidence(t *testing.T) {
	ctx, pool := t.Context(), pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	account, app, subscription := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id,email,plan) VALUES ($1,$2,'pro')`, account, account+"@example.com"); err != nil {
		t.Fatal(err)
	}
	var outboxID int64
	if err := pool.QueryRow(ctx, `INSERT INTO event_fanout_outbox
		(account_id,source,event_id,event_type,event_data,payload,recipient_snapshot)
		VALUES ($1,'orders','replay-read-models','order.created','{}','{}',
		jsonb_build_array(jsonb_build_object('id',$2::text,'app_id',$3::text))) RETURNING id`, account, subscription, app).Scan(&outboxID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO event_fanout_attempt_history
		(outbox_id,subscription_id,app_id,action,state,attempts,failure_code,retryable,last_error)
		VALUES ($1,$2,$3,'fanout_attempt','pending',1,'',true,'')`, outboxID, subscription, app); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO event_fanout_history_summaries
		(outbox_id,subscription_id,app_id,observed_outcomes,compacted_outcomes,capacity_deferrals)
		VALUES ($1,$2,$3,17,9,4)`, outboxID, subscription, app); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id IN (20261005181424759,20261005190741382)`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay populated read-model migrations: %v", err)
	}
	var observed, compacted, deferrals int64
	if err := pool.QueryRow(ctx, `SELECT observed_outcomes,compacted_outcomes,capacity_deferrals
		FROM event_fanout_history_summaries WHERE outbox_id=$1 AND subscription_id=$2`, outboxID, subscription).Scan(&observed, &compacted, &deferrals); err != nil {
		t.Fatal(err)
	}
	if observed != 17 || compacted != 9 || deferrals != 4 {
		t.Fatalf("replay reset delivery evidence: observed=%d compacted=%d deferrals=%d", observed, compacted, deferrals)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM event_routing_backlog WHERE outbox_id=$1 AND subscription_id=$2`, outboxID, subscription).Scan(&count); err != nil || count != 1 {
		t.Fatalf("replay changed the populated projection: count=%d err=%v", count, err)
	}
}
