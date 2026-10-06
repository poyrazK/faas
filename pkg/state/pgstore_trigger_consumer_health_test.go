package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgTriggerConsumerHealthPreservesPollHistory(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx, "consumer-health")
	triggerID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO triggers (id, account_id, app_id, kind, slug)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'queue', 'health-test')`, triggerID, accountID, appID); err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	lag := int64(4)
	age := 1.25
	for _, observation := range []state.TriggerConsumerHealthObservation{
		{LastPollAt: first, Success: true, LagMessages: &lag, LagAgeSeconds: &age},
		{LastPollAt: first.Add(time.Second), Error: "broker unavailable"},
		{LastPollAt: first.Add(2 * time.Second), Success: true, LagMessages: &lag, LagAgeSeconds: &age},
	} {
		if err := store.RecordTriggerConsumerHealth(ctx, triggerID, observation); err != nil {
			t.Fatalf("record poll health: %v", err)
		}
		got, err := store.TriggerConsumerHealth(ctx, triggerID)
		if err != nil || got.LastPollAt == nil || !got.LastPollAt.Equal(observation.LastPollAt) {
			t.Fatalf("poll timestamp: %+v, error %v", got, err)
		}
		if observation.Success {
			if got.LastSuccessAt == nil || !got.LastSuccessAt.Equal(observation.LastPollAt) || got.LagMessages == nil || *got.LagMessages != lag || got.LagAgeSeconds == nil || *got.LagAgeSeconds != age {
				t.Fatalf("success and lag observation: %+v", got)
			}
		} else if got.LastSuccessAt == nil || !got.LastSuccessAt.Equal(first) || got.LagMessages != nil || got.LagAgeSeconds != nil {
			t.Fatalf("failed poll must retain success time and clear unavailable lag: %+v", got)
		}
		if observation.LastPollAt.After(first) && (got.LastErrorAt == nil || !got.LastErrorAt.Equal(first.Add(time.Second)) || got.LastError != "broker unavailable") {
			t.Fatalf("last error must survive recovery: %+v", got)
		}
	}
	if _, err := store.TriggerConsumerHealth(ctx, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing trigger health: %v", err)
	}
}
