package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestMemTriggerWorkBinding(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "trigger-binding-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID,
		Slug: "trigger-binding-" + uuid.NewString(), RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	assertTriggerWorkBinding(t, ctx, store, account.ID, app.ID)
}

func TestPgTriggerWorkBinding(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	assertTriggerWorkBinding(t, ctx, store, accountID, appID)
}

func assertTriggerWorkBinding(t *testing.T, ctx context.Context, store state.Store, accountID, appID string) {
	t.Helper()
	policies := store.(state.AppWorkPolicyStore)
	bindingStore := store.(state.TriggerWorkBindingStore)
	if _, err := policies.UpsertAppWorkPolicy(ctx, accountID, appID,
		workpolicy.Policy{Name: "orders", MaxRunningPerKey: 1}); err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, appID, "kafka", "orders", false,
		[]byte(`{}`), "", 10, 1000, 3, 1<<20, "commit", limits)
	if err != nil {
		t.Fatal(err)
	}
	triggerID := trigger.ID.String()
	if _, err := bindingStore.SetTriggerWorkBinding(ctx, appID, triggerID, "orders", "data.order_id", "data.tenant_id"); err != nil {
		t.Fatal(err)
	}
	got, err := bindingStore.TriggerWorkBindingByID(ctx, triggerID)
	if err != nil || got == nil || got.PolicyName != "orders" ||
		got.KeySelector != "data.order_id" || got.FairnessSelector != "data.tenant_id" {
		t.Fatalf("binding = %+v, err=%v", got, err)
	}
	if err := policies.DeleteAppWorkPolicy(ctx, accountID, appID, "orders"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("delete referenced policy = %v, want conflict", err)
	}
	if _, err := bindingStore.SetTriggerWorkBinding(ctx, appID, triggerID, "orders", "data.*", ""); err == nil {
		t.Fatal("accepted wildcard work key selector")
	}
	enabled, err := store.CreateTriggerIfUnderQuota(ctx, appID, "kafka", "enabled", true,
		[]byte(`{}`), "", 10, 1000, 3, 1<<20, "commit", limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bindingStore.SetTriggerWorkBinding(ctx, appID, enabled.ID.String(), "orders", "data.order_id", ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("binding enabled trigger = %v, want conflict", err)
	}
	legacy, err := store.CreateTriggerIfUnderQuota(ctx, appID, "kafka", "legacy", false,
		[]byte(`{}`), "", 10, 1000, 3, 1<<20, "commit", limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertTriggerRecord(ctx, legacy.ID.String(), "old-handle", []byte(`{}`), nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := bindingStore.SetTriggerWorkBinding(ctx, appID, legacy.ID.String(), "orders", "data.order_id", ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("binding trigger with legacy receipt = %v, want conflict", err)
	}
	if _, err := bindingStore.SetTriggerWorkBinding(ctx, appID, triggerID, "", "", ""); err != nil {
		t.Fatal(err)
	}
	got, err = bindingStore.TriggerWorkBindingByID(ctx, triggerID)
	if err != nil || got != nil {
		t.Fatalf("removed binding = %+v, err=%v", got, err)
	}
	if err := policies.DeleteAppWorkPolicy(ctx, accountID, appID, "orders"); err != nil {
		t.Fatal(err)
	}
	queue, err := store.CreateTriggerIfUnderQuota(ctx, appID, "queue", "queue", true,
		[]byte(`{}`), "queue", 10, 1000, 3, 1<<20, "commit", limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bindingStore.SetTriggerWorkBinding(ctx, appID, queue.ID.String(), "orders", "data.order_id", ""); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("queue trigger binding = %v, want invalid argument", err)
	}
}
