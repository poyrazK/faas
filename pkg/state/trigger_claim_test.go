package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemTriggerRecordClaimLease(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "trigger-claim-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "trigger-claim-" + uuid.NewString(), RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	assertTriggerRecordClaimLease(t, ctx, store, app.ID)
}

func TestPgTriggerRecordClaimLease(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	_, appID, _ := seedLiveDeploy(t, store, ctx)
	triggerID, recordID := assertTriggerRecordClaimLease(t, ctx, store, appID)
	if _, err := pool.Exec(ctx, `update trigger_records set state = 'claimed',
		claim_expires_at = now() - interval '1 second' where id = $1`, recordID); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteClaimedTriggerRecord(ctx, recordID, 2); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired completion before recovery = %v, want ErrNotFound", err)
	}
	recovered, err := store.ClaimTriggerRecords(ctx, triggerID, 1)
	if err != nil || len(recovered) != 1 || recovered[0].ClaimGeneration != 3 {
		t.Fatalf("expired claim recovery = %+v, err=%v", recovered, err)
	}
	if err := store.DeadLetterClaimedTriggerRecord(ctx, recordID, 2, "stale"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale dead-letter = %v, want ErrNotFound", err)
	}
}

func assertTriggerRecordClaimLease(t *testing.T, ctx context.Context, store state.Store, appID string) (string, string) {
	t.Helper()
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, appID, "queue", "claim", true,
		[]byte(`{"mode":"queue"}`), "queue", 10, 1000, 3, 1<<20, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	triggerID := trigger.ID.String()
	recordID, err := store.InsertTriggerRecord(ctx, triggerID, "item-1", []byte(`{}`), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimTriggerRecords(ctx, triggerID, 1)
	if err != nil || len(first) != 1 || first[0].State != "claimed" || first[0].ClaimGeneration != 1 || !first[0].ClaimExpiresAt.Valid {
		t.Fatalf("first claim = %+v, err=%v", first, err)
	}
	second, err := store.ClaimTriggerRecords(ctx, triggerID, 1)
	if err != nil || len(second) != 0 {
		t.Fatalf("duplicate claim = %+v, err=%v", second, err)
	}
	finisher := store.(state.TriggerClaimFinisher)
	if err := finisher.RetryClaimedTriggerRecord(ctx, recordID, 1, "retry", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	retried, err := store.ClaimTriggerRecords(ctx, triggerID, 1)
	if err != nil || len(retried) != 1 || retried[0].ClaimGeneration != 2 || retried[0].Attempts != 1 {
		t.Fatalf("retry claim = %+v, err=%v", retried, err)
	}
	if err := finisher.CompleteClaimedTriggerRecord(ctx, recordID, 1); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale completion = %v, want ErrNotFound", err)
	}
	deadID, err := store.InsertTriggerRecord(ctx, triggerID, "item-2", []byte(`{}`), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	deadClaim, err := store.ClaimTriggerRecords(ctx, triggerID, 1)
	if err != nil || len(deadClaim) != 1 || deadClaim[0].ID.String() != deadID {
		t.Fatalf("dead-letter candidate claim = %+v, err=%v", deadClaim, err)
	}
	if err := finisher.RouteClaimedTriggerDeadLetter(ctx, deadID, 0, triggerID, "poison_record", []byte("stale")); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale DLQ route = %v, want ErrNotFound", err)
	}
	if err := finisher.RouteClaimedTriggerDeadLetter(ctx, deadID, deadClaim[0].ClaimGeneration, triggerID, "poison_record", []byte("poison")); err != nil {
		t.Fatal(err)
	}
	dlq, err := store.ListTriggerDeadLetter(ctx, triggerID, 10)
	if err != nil || len(dlq) != 1 || dlq[0].RecordID.String() != deadID {
		t.Fatalf("fenced DLQ receipt = %+v, err=%v", dlq, err)
	}
	otherID, err := store.InsertTriggerRecord(ctx, triggerID, "other-batch", []byte(`{}`), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	batchID, err := store.InsertTriggerRecord(ctx, triggerID, "this-batch", []byte(`{}`), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	batchClaimer := store.(state.TriggerBatchClaimer)
	selected, err := batchClaimer.ClaimTriggerRecordsByItems(ctx, triggerID, []string{"this-batch"})
	if err != nil || len(selected) != 1 || selected[0].ID.String() != batchID {
		t.Fatalf("batch claim selected = %+v, err=%v", selected, err)
	}
	other, err := batchClaimer.ClaimTriggerRecordsByItems(ctx, triggerID, []string{"other-batch"})
	if err != nil || len(other) != 1 || other[0].ID.String() != otherID {
		t.Fatalf("unrelated row remained due = %+v, err=%v", other, err)
	}
	return triggerID, recordID
}
