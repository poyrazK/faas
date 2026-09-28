package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestMemAppWorkPolicies(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "policy-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "policy-test"})
	if err != nil {
		t.Fatal(err)
	}
	assertAppWorkPolicies(t, ctx, store, account.ID, app.ID)
}

func TestPgAppWorkPolicies(t *testing.T) {
	store, ctx, appID, accountID := seedInvocationPg(t)
	assertAppWorkPolicies(t, ctx, store, accountID, appID)
}

func TestMemEventWorkBinding(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "binding-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "binding-test"})
	if err != nil {
		t.Fatal(err)
	}
	assertEventWorkBinding(t, ctx, store, account.ID, app.ID)
}

func TestPgEventWorkBinding(t *testing.T) {
	store, ctx, appID, accountID := seedInvocationPg(t)
	assertEventWorkBinding(t, ctx, store, accountID, appID)
}

type eventWorkStore interface {
	state.AppWorkPolicyStore
	state.EventWorkBindingStore
	state.EventSubscriptionStore
}

func assertEventWorkBinding(t *testing.T, ctx context.Context, store eventWorkStore, accountID, appID string) {
	t.Helper()
	policy := workpolicy.Policy{Name: "order-updates", MaxRunningPerKey: 1}
	if _, err := store.UpsertAppWorkPolicy(ctx, accountID, appID, policy); err != nil {
		t.Fatal(err)
	}
	sub, _, err := store.UpsertEventSubscription(ctx, accountID, appID, "orders", "order.updated", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetEventWorkBinding(ctx, appID, sub.ID, policy.Name, "data.order_id"); err != nil {
		t.Fatal(err)
	}
	bindings, err := store.EventWorkBindingsByIDs(ctx, []string{sub.ID})
	if err != nil || bindings[sub.ID].KeySelector != "data.order_id" {
		t.Fatalf("bindings = %+v, %v", bindings, err)
	}
	if err := store.DeleteAppWorkPolicy(ctx, accountID, appID, policy.Name); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("delete bound policy = %v", err)
	}
	old, err := store.SetEventWorkBinding(ctx, appID, sub.ID, policy.Name, "data.customer_id")
	if err != nil || old == nil || old.KeySelector != "data.order_id" {
		t.Fatalf("previous binding = %+v, %v", old, err)
	}
	if _, err := store.SetEventWorkBinding(ctx, appID, sub.ID, policy.Name, "data.customer_id", state.EventWorkCancelPending); err != nil {
		t.Fatal(err)
	}
	bindings, err = store.EventWorkBindingsByIDs(ctx, []string{sub.ID})
	if err != nil || bindings[sub.ID].Action != state.EventWorkCancelPending {
		t.Fatalf("cancel action = %+v, %v", bindings, err)
	}
	if _, err := store.SetEventWorkBinding(ctx, appID, sub.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAppWorkPolicy(ctx, accountID, appID, policy.Name); err != nil {
		t.Fatal(err)
	}
}

func assertAppWorkPolicies(t *testing.T, ctx context.Context, store state.AppWorkPolicyStore, accountID, appID string) {
	t.Helper()
	policy := workpolicy.Policy{Name: "document-index", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest, Debounce: 3 * time.Second, ExpiresAfter: 10 * time.Minute}
	created, err := store.UpsertAppWorkPolicy(ctx, accountID, appID, policy)
	if err != nil || created.Revision != 1 {
		t.Fatalf("create policy = %+v, %v", created, err)
	}
	same, err := store.UpsertAppWorkPolicy(ctx, accountID, appID, policy)
	if err != nil || same.Revision != 1 {
		t.Fatalf("idempotent upsert = %+v, %v", same, err)
	}
	policy.Debounce = 5 * time.Second
	changed, err := store.UpsertAppWorkPolicy(ctx, accountID, appID, policy)
	if err != nil || changed.Revision != 2 {
		t.Fatalf("changed policy = %+v, %v", changed, err)
	}
	got, err := store.AppWorkPolicyByName(ctx, appID, policy.Name)
	if err != nil || got.Policy != policy || got.Revision != 2 {
		t.Fatalf("read policy = %+v, %v", got, err)
	}
	listed, err := store.ListAppWorkPolicies(ctx, appID)
	if err != nil || len(listed) != 1 || listed[0].Policy.Name != policy.Name {
		t.Fatalf("list policies = %+v, %v", listed, err)
	}
	if _, err := store.UpsertAppWorkPolicy(ctx, uuid.NewString(), appID, policy); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account upsert = %v", err)
	}
	if err := store.DeleteAppWorkPolicy(ctx, accountID, appID, policy.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppWorkPolicyByName(ctx, appID, policy.Name); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted policy lookup = %v", err)
	}
}
