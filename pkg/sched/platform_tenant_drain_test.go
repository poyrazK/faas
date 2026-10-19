package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 376
// Suspended tenants cannot dispatch work; resumed retries retain the original identity.
func TestDrain_PlatformTenantSuspensionAndRetry(t *testing.T) {
	ctx := context.Background()
	drain, store, vmm, _, synth := sched.NewDrainHarness(t, api.PlanHobby, true)
	apps, err := store.ListAllApps(ctx)
	if err != nil || len(apps) != 1 {
		t.Fatalf("fixture apps: %v", err)
	}
	app := apps[0]
	tenants := store.(state.PlatformTenantStore)
	tenant, _, err := tenants.CreatePlatformTenant(ctx, app.AccountID, "alice", "Alice", 250)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: app.AccountID, PlatformTenantID: tenant.ID,
		Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/documents", DueAt: time.Now().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	(*vmm.Mu()).Lock()
	boots := (*vmm.ColdBoots())
	(*vmm.Mu()).Unlock()
	if _, err := tenants.SetPlatformTenantStatus(ctx, app.AccountID, tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	drain.Tick(ctx)
	(*vmm.Mu()).Lock()
	heldBoots := (*vmm.ColdBoots())
	(*vmm.Mu()).Unlock()
	held, _ := store.InvocationByID(ctx, inv.ID)
	if (*synth.Calls()).Load() != 0 || heldBoots != boots || held.Attempts != 0 || held.State != state.InvocationPending {
		t.Fatalf("suspended tenant woke or consumed work: boots=%d/%d calls=%d inv=%+v", heldBoots, boots, (*synth.Calls()).Load(), held)
	}
	if _, err := tenants.SetPlatformTenantStatus(ctx, app.AccountID, tenant.ID, state.PlatformTenantActive); err != nil {
		t.Fatal(err)
	}
	(*synth.Transient()).Store(true)
	drain.Tick(ctx)
	retried, _ := store.InvocationByID(ctx, inv.ID)
	if (*synth.Calls()).Load() != 1 || retried.State != state.InvocationPending || retried.Attempts != 1 || retried.PlatformTenantID != tenant.ID {
		t.Fatalf("resumed tenant retry: %+v calls=%d", retried, (*synth.Calls()).Load())
	}
	(*synth.Transient()).Store(false)
	(*drain.Now()) = func() time.Time { return time.Now().Add(time.Minute) }
	drain.Tick(ctx)
	complete, _ := store.InvocationByID(ctx, inv.ID)
	if (*synth.Calls()).Load() != 2 || complete.State != state.InvocationCompleted || complete.Attempts != 2 || complete.PlatformTenantID != tenant.ID {
		t.Fatalf("retry completion: %+v calls=%d", complete, (*synth.Calls()).Load())
	}
}
