package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSetTriggerDelivery(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, appID, _ := seedLiveDeploy(t, s, ctx, "-trigger-delivery")
	limits, _ := api.LimitsFor(api.PlanPro)
	kafka, err := s.CreateTriggerIfUnderQuota(ctx, appID, string(api.TriggerKindKafka), "orders", true,
		[]byte(`{"brokers":["b:9092"],"topic":"orders","group":"g"}`), "", 10, 1000, 3, 6*1024*1024, api.BrokerPoisonStrategyCommit, limits)
	if err != nil {
		t.Fatal(err)
	}
	if kafka.Delivery != api.TriggerDeliveryRecord {
		t.Fatalf("new trigger delivery = %q, want record", kafka.Delivery)
	}
	updated, err := s.SetTriggerDelivery(ctx, kafka.ID.String(), api.TriggerDeliveryBatch)
	if err != nil || updated.Delivery != api.TriggerDeliveryBatch {
		t.Fatalf("SetTriggerDelivery = %+v, %v", updated, err)
	}
	for _, read := range []func() (string, error){
		func() (string, error) { t, err := s.TriggerByID(ctx, kafka.ID.String()); return t.Delivery, err },
		func() (string, error) { ts, err := s.ListTriggersForApp(ctx, appID); return ts[0].Delivery, err },
		func() (string, error) { ts, err := s.ListEnabledTriggers(ctx); return ts[0].Delivery, err },
	} {
		if got, err := read(); err != nil || got != api.TriggerDeliveryBatch {
			t.Fatalf("read back delivery = %q, %v", got, err)
		}
	}
	queue, err := s.CreateTriggerIfUnderQuota(ctx, appID, string(api.TriggerKindQueue), "jobs", false,
		[]byte(`{"mode":"queue"}`), "queue", 1, 1000, 3, 6*1024*1024, api.BrokerPoisonStrategyCommit, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetTriggerDelivery(ctx, queue.ID.String(), api.TriggerDeliveryBatch); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("queue batch delivery err = %v, want ErrInvalidArgument", err)
	}
	// The database refuses it too.
	if _, err := pool.Exec(ctx, `update triggers set delivery='batch' where id=$1`, queue.ID.String()); err == nil {
		t.Fatal("triggers_delivery_check accepted batch delivery for a queue trigger")
	}

	mem := state.NewMemStore()
	memTrigger, err := mem.CreateTriggerIfUnderQuota(ctx, mustMemApp(t, mem), string(api.TriggerKindNATS), "events", true,
		[]byte(`{}`), "", 10, 1000, 3, 6*1024*1024, api.BrokerPoisonStrategyCommit, limits)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := mem.SetTriggerDelivery(ctx, memTrigger.ID.String(), api.TriggerDeliveryBatch); err != nil || got.Delivery != api.TriggerDeliveryBatch {
		t.Fatalf("memstore SetTriggerDelivery = %+v, %v", got, err)
	}
	if _, err := mem.SetTriggerDelivery(ctx, memTrigger.ID.String(), "stream"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("memstore unknown delivery err = %v", err)
	}
}

func mustMemApp(t *testing.T, mem *state.MemStore) string {
	t.Helper()
	acct, err := mem.CreateAccount(t.Context(), "delivery@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "delivery-mem"})
	if err != nil {
		t.Fatal(err)
	}
	return app.ID
}
