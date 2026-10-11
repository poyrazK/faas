// spec: §6 — a failed restart request leaves the app active.

package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// production-us hunt #5 (H5-35): `secrets set --restart` hit its deadline
// queueing the runtime-config restart, and the claim release ran on the same
// expired request context. The app stayed evicted_cold and the reaper stopped
// both instances serving a live soak.
func TestReleaseAppRestartClaimSurvivesTheRequestDeadline(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "restart-claim@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "restart-claim", RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := claimAppRestart(ctx, store, app.ID)
	if err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	expired, cancel := context.WithCancel(ctx)
	cancel()
	if err := releaseAppRestartClaim(expired, store, app.ID); err != nil {
		t.Fatalf("release on an expired request context: %v", err)
	}
	got, err := store.AppByID(ctx, app.ID)
	if err != nil || got.Status != state.AppActive {
		t.Fatalf("app status = %q (%v), want active after the release", got.Status, err)
	}
}
