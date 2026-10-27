// adr: 20261027090010
package state

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestSetTriggerAgent(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		ctx := t.Context()
		account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "agent-trigger-" + uuid.NewString()[:8], Type: AppTypeApp, RAMMB: 256})
		if err != nil {
			t.Fatal(err)
		}
		limits := api.MustLimitsFor(api.PlanPro)
		kafka, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "kafka", "orders", false, []byte(`{}`), "", 10, 1000, 3, 1<<20, "commit", limits)
		if err != nil {
			t.Fatal(err)
		}
		id := kafka.ID.String()
		agents := store.(TriggerAgentStore)
		if _, err := agents.SetTriggerAgent(ctx, id, "Not-A-Name"); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("invalid name = %v", err)
		}
		pointed, err := agents.SetTriggerAgent(ctx, id, "order-review")
		if err != nil || !pointed.Agent.Valid || pointed.Agent.String != "order-review" {
			t.Fatalf("SetTriggerAgent() = %+v, %v", pointed.Agent, err)
		}
		if _, err := store.(TriggerDeliveryStore).SetTriggerDelivery(ctx, id, api.TriggerDeliveryBatch); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("batch delivery on an agent trigger = %v", err)
		}
		enabled := true
		if _, err := store.UpdateTrigger(ctx, id, &enabled, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		byID, err := store.TriggerByID(ctx, id)
		if err != nil || byID.Agent.String != "order-review" {
			t.Fatalf("TriggerByID() = %+v, %v", byID.Agent, err)
		}
		listed, err := store.ListTriggersForApp(ctx, app.ID)
		if err != nil || len(listed) != 1 || listed[0].Agent.String != "order-review" {
			t.Fatalf("ListTriggersForApp() = %+v, %v", listed, err)
		}
		live, err := store.ListEnabledTriggers(ctx)
		found := false
		for _, trigger := range live {
			found = found || (trigger.ID == kafka.ID && trigger.Agent.String == "order-review")
		}
		if err != nil || !found {
			t.Fatalf("ListEnabledTriggers() = %+v, %v", live, err)
		}

		queue, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "queue", "jobs", false, []byte(`{"mode":"queue"}`), "queue", 10, 1000, 3, 1<<20, "commit", limits)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := agents.SetTriggerAgent(ctx, queue.ID.String(), "order-review"); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("queue trigger agent = %v", err)
		}
		if pg, ok := store.(*PgStore); ok {
			if _, err := pg.pool.Exec(ctx, `UPDATE triggers SET delivery = 'batch' WHERE id = $1`, kafka.ID); err == nil {
				t.Fatal("the CHECK let an agent trigger switch to batch delivery")
			}
		}
	})
}
