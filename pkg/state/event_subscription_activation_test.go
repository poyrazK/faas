package state_test

// adr: 20261101030512

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func enabledSubscriptionIDs(t *testing.T, store recipientClaimTestStore, appID string) map[string]bool {
	t.Helper()
	subs, err := store.ListEventSubscriptionsForApp(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	enabled := map[string]bool{}
	for _, sub := range subs {
		if sub.Enabled {
			enabled[sub.ID] = true
		}
	}
	return enabled
}

// A production deploy's declared subscriptions take effect only when its
// deployment serves all production traffic: until then every subscription
// keeps delivering; once live, the undeclared ones are disabled. A dark
// deployment never applies its intent.
func TestEventSubscriptionIntentAppliesWhenDeploymentIsLive(t *testing.T) {
	forRecipientClaimStores(t, func(t *testing.T, store recipientClaimTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		activation, ok := any(store).(state.EventSubscriptionActivationStore)
		if !ok {
			t.Fatal("store has no event subscription activation")
		}
		account, err := store.CreateAccount(ctx, "activation-"+uuid.NewString()+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		accountID := uuid.MustParse(account.ID).String()
		app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: accountID, Slug: "activation-" + uuid.NewString(), Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MaxConcurrency: 5})
		if err != nil {
			t.Fatal(err)
		}
		kept, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "order.created", nil)
		if err != nil {
			t.Fatal(err)
		}
		dropped, _, err := store.UpsertEventSubscription(ctx, accountID, app.ID, "orders", "order.paid", nil)
		if err != nil {
			t.Fatal(err)
		}
		both := map[string]bool{kept.ID: true, dropped.ID: true}

		// A dark deploy records the intent but never applies it.
		dark, err := store.CreateDeployment(state.ContextWithDeploymentEventSubscriptions(ctx, []string{kept.ID}),
			state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, TrafficPercent: 0, TrafficPercentExplicit: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(ctx, dark.ID, state.DeployLive, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := activation.ReconcileEventSubscriptionActivations(ctx); err != nil {
			t.Fatal(err)
		}
		if got := enabledSubscriptionIDs(t, store, app.ID); len(got) != 2 {
			t.Fatalf("after a dark deploy enabled = %v, want both %v", got, both)
		}

		// A production deploy applies only once live at full traffic.
		release, err := store.CreateDeployment(state.ContextWithDeploymentEventSubscriptions(ctx, []string{kept.ID}),
			state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, TrafficPercent: 100})
		if err != nil {
			t.Fatal(err)
		}
		if changes, err := activation.ReconcileEventSubscriptionActivations(ctx); err != nil || len(changes) != 0 {
			t.Fatalf("before the release is live: changes = %+v, %v; want none", changes, err)
		}
		if got := enabledSubscriptionIDs(t, store, app.ID); len(got) != 2 {
			t.Fatalf("before the release is live enabled = %v, want both", got)
		}
		if err := store.UpdateDeploymentStatus(ctx, release.ID, state.DeployLive, ""); err != nil {
			t.Fatal(err)
		}
		changes, err := activation.ReconcileEventSubscriptionActivations(ctx)
		if err != nil || len(changes) != 1 || len(changes[0].Disabled) != 1 || changes[0].Disabled[0] != dropped.ID {
			t.Fatalf("live release: changes = %+v, %v; want %s disabled", changes, err, dropped.ID)
		}
		if got := enabledSubscriptionIDs(t, store, app.ID); len(got) != 1 || !got[kept.ID] {
			t.Fatalf("after the release is live enabled = %v, want only %s", got, kept.ID)
		}
		// Settled intents are not applied twice.
		if changes, err := activation.ReconcileEventSubscriptionActivations(ctx); err != nil || len(changes) != 0 {
			t.Fatalf("second reconcile: changes = %+v, %v; want none", changes, err)
		}
	})
}
