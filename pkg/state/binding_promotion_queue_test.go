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
	queue, err := store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: acct.ID, AppID: app.ID, Name: "events", QueueName: "events", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "queue", "events", true, []byte(`{"queue_binding_id":"`+queue.ID+`"}`), "queue", 1, 1000, 3, 1024, api.BrokerPoisonStrategyCommit, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.UUID(trigger.ID.Bytes).String()
	health := store.(state.TriggerConsumerHealthStore)
	if err := health.RecordTriggerConsumerHealth(ctx, id, state.TriggerConsumerHealthObservation{LastPollAt: time.Now(), Success: true}); err != nil {
		t.Fatal(err)
	}
	guarded := store.(state.BindingPromotionStore)
	for _, mutate := range []func() error{
		func() error {
			return health.RecordTriggerConsumerHealth(ctx, id, state.TriggerConsumerHealthObservation{LastPollAt: time.Now(), Error: "consumer failure"})
		},
		func() error {
			disabled := false
			_, err := store.UpdateTrigger(ctx, id, &disabled, nil, nil, nil, nil, nil, nil, nil, nil)
			return err
		},
		func() error { return store.DeleteTrigger(ctx, id, app.ID) },
		func() error {
			disabled := false
			_, err := store.UpdateQueueBinding(ctx, acct.ID, app.ID, queue.ID, state.UpdateQueueBindingParams{Enabled: &disabled})
			return err
		},
	} {
		fence := bindingPromotionFence(t, guarded, acct, app, candidate)
		if err := mutate(); err != nil {
			t.Fatal(err)
		}
		if _, err := guarded.PromoteDeploymentWithBindings(ctx, candidate.ID, fence, serving.ID); !errors.Is(err, state.ErrBindingPromotionChanged) {
			t.Fatalf("queue mutation did not invalidate promotion: %v", err)
		}
		for id, percent := range map[string]int{serving.ID: 100, candidate.ID: 0} {
			dep, err := store.DeploymentByID(ctx, id)
			if err != nil || dep.TrafficPercent != percent {
				t.Fatalf("queue race changed traffic: %+v %v", dep, err)
			}
		}
	}
}
