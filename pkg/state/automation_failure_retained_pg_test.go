package state_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestAutomationFailureRetainedEventsCountsOnlyEligibleRecipientsPG(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	app, _ := state.SeedWorkflowSchedule(t, store, "allow")
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	recipients := make([]state.PublishedEventRecipient, 0, len(ids))
	for _, id := range ids {
		recipients = append(recipients, state.PublishedEventRecipient{ID: id, AppID: app.ID, Workflow: json.RawMessage(`{"name":"paid"}`)})
	}
	snapshot, err := json.Marshal(recipients)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := json.Marshal(map[string]state.PublishedEventRecipientProgress{
		ids[1]: {State: "filtered"}, ids[2]: {State: "failed"}, ids[3]: {State: "enqueued"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var outboxID int64
	err = pool.QueryRow(ctx, `INSERT INTO event_fanout_outbox(account_id,source,event_id,event_type,event_data,payload,recipient_snapshot,recipient_progress)
        VALUES ($1,'gregale.test',$2,'paid','{}','{}',$3,$4) RETURNING id`, app.AccountID, uuid.NewString(), snapshot, progress).Scan(&outboxID)
	if err != nil {
		t.Fatal(err)
	}
	count := func(want int64) {
		t.Helper()
		got, err := sqlc.New().AutomationFailureRetainedEvents(ctx, pool, sqlc.AutomationFailureRetainedEventsParams{AppID: app.ID, Name: "paid"})
		if err != nil || got != want {
			t.Fatalf("retained=%d want=%d err=%v", got, want, err)
		}
	}
	count(1)
	if _, err := pool.Exec(ctx, `UPDATE event_fanout_outbox SET recipient_claims=true WHERE id=$1`, outboxID); err != nil {
		t.Fatal(err)
	}
	for i, status := range []string{"pending", "pending", "filtered", "failed"} {
		raw, err := json.Marshal(recipients[i])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO event_fanout_recipients(outbox_id,subscription_id,app_id,recipient,state) VALUES($1,$2,$3,$4,$5)`, outboxID, ids[i], app.ID, raw, status); err != nil {
			t.Fatal(err)
		}
	}
	count(2)
}
