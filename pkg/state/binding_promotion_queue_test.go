// adr: 532 — queue owner mutations invalidate promotion evidence without changing traffic.
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBindingPromotionQueueChangesMem(t *testing.T) {
	bindingPromotionQueueChanges(t, state.NewMemStore())
}
func TestBindingPromotionQueueChangesPG(t *testing.T) {
	store, _ := pgStore(t)
	bindingPromotionQueueChanges(t, store)
}

func bindingPromotionQueueChanges(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	acct, app, serving, candidate := bindingPromotionFixture(t, store)
	app, err := store.SetAppWorkloadClass(ctx, app.ID, state.WorkloadClassWorker, "manual")
	if err != nil {
		t.Fatal(err)
	}
	queues := store.(state.QueueBindingConsumerStore)
	result, err := queues.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: acct.ID, AppID: app.ID, Name: "events", QueueName: "events", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatalf("publish queue consumer: %v", err)
	}
	if len(result.Changes) != 1 {
		t.Fatalf("queue consumer publication=%+v", result)
	}
	queue, id := result.Binding, result.Changes[0].TriggerID
	disabled := false
	if _, err := store.UpdateTrigger(ctx, id, &disabled, nil, nil, nil, nil, nil, nil, nil, nil); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("generic trigger update bypassed queue ownership: %v", err)
	}
	if err := store.DeleteTrigger(ctx, id, app.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("generic trigger deletion bypassed queue ownership: %v", err)
	}
	trigger, err := store.TriggerByID(ctx, id)
	if err != nil || !trigger.Enabled || trigger.QueueBindingID.String() != uuid.MustParse(queue.ID).String() {
		t.Fatalf("rejected generic mutation changed consumer: %+v %v", trigger, err)
	}
	health := store.(state.TriggerConsumerHealthStore)
	if err := health.RecordTriggerConsumerHealth(ctx, id, state.TriggerConsumerHealthObservation{LastPollAt: time.Now(), Success: true}); err != nil {
		t.Fatal(err)
	}
	guarded := store.(state.BindingPromotionStore)
	for _, change := range []struct {
		name   string
		mutate func() error
	}{
		{"consumer error", func() error {
			return health.RecordTriggerConsumerHealth(ctx, id, state.TriggerConsumerHealthObservation{LastPollAt: time.Now(), Error: "consumer failure"})
		}},
		{"disabled binding", func() error {
			_, err := queues.UpdateQueueBindingWithConsumer(ctx, acct.ID, app.ID, queue.ID, state.UpdateQueueBindingParams{Enabled: &disabled})
			return err
		}},
		{"pull mode", func() error {
			mode := "pull"
			_, err := queues.UpdateQueueBindingWithConsumer(ctx, acct.ID, app.ID, queue.ID, state.UpdateQueueBindingParams{Mode: &mode})
			return err
		}},
		{"retired binding", func() error {
			_, err := queues.DeleteQueueBindingWithConsumer(ctx, acct.ID, app.ID, queue.ID)
			return err
		}},
	} {
		fence := bindingPromotionFence(t, guarded, acct, app, candidate)
		if err := change.mutate(); err != nil {
			t.Fatalf("%s: %v", change.name, err)
		}
		if _, err := guarded.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, serving.ID); !errors.Is(err, state.ErrBindingPromotionChanged) {
			t.Fatalf("%s did not invalidate promotion: %v", change.name, err)
		}
		for id, percent := range map[string]int{serving.ID: 100, candidate.ID: 0} {
			dep, err := store.DeploymentByID(ctx, id)
			if err != nil || dep.TrafficPercent != percent {
				t.Fatalf("queue race changed traffic: %+v %v", dep, err)
			}
		}
	}
}
