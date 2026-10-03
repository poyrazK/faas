package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPublishedEventWorkRecoversAndRetainsIdentity(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "fanout-receipt@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-24 * time.Hour).UTC()
	payload := []byte(`{"id":"evt-1","source":"orders","type":"created","data":{"amount":1}}`)
	if err := store.AppendEventAt(ctx, "apid", "event.published", &account.ID, payload, old); err != nil {
		t.Fatal(err)
	}
	work, err := store.ClaimDuePublishedEvent(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if work.ID != 1 || work.Attempts != 1 {
		t.Fatalf("first claim = %+v", work)
	}
	if err := store.FinishPublishedEvent(ctx, work.ID, "wrong-token", nil); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("wrong token = %v", err)
	}
	if err := store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, errors.New("temporary failure")); err != nil {
		t.Fatal(err)
	}
	work, err = store.ClaimDuePublishedEvent(ctx, time.Now().Add(6*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if work.Attempts != 2 {
		t.Fatalf("attempts = %d", work.Attempts)
	}
	if err := store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimDuePublishedEvent(ctx, time.Now().Add(time.Hour)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("delivered claim = %v", err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, []byte(`{"id":"evt-1","source":"orders","type":"created","data":{"amount":2}}`)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed identity = %v", err)
	}
	pruned, err := store.PruneDeliveredPublishedEvents(ctx, time.Now().Add(state.PublishedEventIdentityRetention+time.Second), 10)
	if err != nil || pruned != 1 {
		t.Fatalf("pruned = %d, %v", pruned, err)
	}
}
