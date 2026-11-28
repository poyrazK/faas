package sched_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Maintenance mode answers requests 503 without a wake, but queue, async and
// cron deliveries still woke the app (prod hunt r81). The drain holds them,
// without spending an attempt, until maintenance is turned off.
func TestDrainHoldsDeliveriesDuringMaintenance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, store, _, _, ds := sched.NewDrainHarness(t, api.PlanHobby, true)
	apps, err := store.ListAllApps(ctx)
	if err != nil || len(apps) == 0 {
		t.Fatalf("ListAllApps: %v / %d apps", err, len(apps))
	}
	on := true
	if _, err := store.UpdateApp(ctx, apps[0].ID, state.UpdateAppParams{MaintenanceMode: &on, SetMaintenanceMode: true}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	inv := sched.SeedDrainInvocation(t, store, state.InvocationQueue)

	d.Tick(ctx)

	if got := ds.Calls().Load(); got != 0 {
		t.Fatalf("synth calls = %d, want 0 during maintenance", got)
	}
	got, err := store.InvocationByID(ctx, inv.ID)
	if err != nil {
		t.Fatalf("InvocationByID: %v", err)
	}
	if got.State != state.InvocationPending || got.Attempts != inv.Attempts {
		t.Fatalf("state/attempts = %q/%d, want pending/%d (held, no attempt spent)", got.State, got.Attempts, inv.Attempts)
	}
	if !got.DueAt.After(time.Now().Add(30*time.Second)) || !strings.Contains(got.LastError, "maintenance") {
		t.Fatalf("due_at %s last_error %q, want a held row naming maintenance", got.DueAt, got.LastError)
	}
}
