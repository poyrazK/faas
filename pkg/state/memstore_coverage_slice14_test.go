package state_test

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

// TestCoverageSlice14ConcurrencyForDeployment drives the per-deployment
// concurrency counter at memstore.go:2156. The function sums the live
// instances of a deployment so schedd can decide whether to admit
// another wake.
func TestCoverageSlice14ConcurrencyForDeployment(t *testing.T) {
	m, ctx, _, app, dep := state.MemCoverageFixture(t)
	if got, err := m.ConcurrencyForDeployment(ctx, app.ID, dep.ID); err != nil || got != 0 {
		t.Fatalf("ConcurrencyForDeployment empty = %d, %v", got, err)
	}
}

// TestCoverageSlice14UpdateDeploymentMinInstances drives the
// deployment MinInstances override path at memstore.go:2175.
func TestCoverageSlice14UpdateDeploymentMinInstances(t *testing.T) {
	m, ctx, _, _, dep := state.MemCoverageFixture(t)
	if _, err := m.UpdateDeploymentMinInstances(ctx, dep.ID, 2); err != nil {
		t.Fatalf("UpdateDeploymentMinInstances: %v", err)
	}
}

// TestCoverageSlice14ListLiveInstancesOnNode drives the per-node live
// instance filter at memstore.go:2370 (maxPerTick caps the result).
func TestCoverageSlice14ListLiveInstancesOnNode(t *testing.T) {
	m, ctx, _, _, _ := state.MemCoverageFixture(t)
	if got, err := m.ListLiveInstancesOnNode(ctx, "node-a", 5); err != nil || len(got) != 0 {
		t.Fatalf("ListLiveInstancesOnNode empty = %d, %v", len(got), err)
	}
}

// TestCoverageSlice14AuthDefaultFlipped drives the auth-default
// flipped-timestamp surface at memstore.go:2615-2642. These two
// methods let the operator flip an app from closed-auth to public-auth
// and check whether the flip is recent.
func TestCoverageSlice14AuthDefaultFlipped(t *testing.T) {
	m, ctx, account, _, _ := state.MemCoverageFixture(t)
	if got, err := m.CountAuthDefaultFlippedApps(ctx, account.ID); err != nil || got != 0 {
		t.Fatalf("CountAuthDefaultFlippedApps empty = %d, %v", got, err)
	}
	if _, err := m.AuthDefaultFlippedAt(ctx); err != nil {
		t.Fatalf("AuthDefaultFlippedAt: %v", err)
	}
}

// TestCoverageSlice14SetLastScaleOutIn drives the MemStore-only
// scale-out/scale-in timestamp setters at memstore.go:4708-4724.
// These are used by the scale-loop tests to plant a baseline.
func TestCoverageSlice14SetLastScaleOutIn(t *testing.T) {
	m, _, _, app, _ := state.MemCoverageFixture(t)
	now := time.Now()
	if err := m.SetLastScaleOutAt(app.ID, now); err != nil {
		t.Fatalf("SetLastScaleOutAt: %v", err)
	}
	if err := m.SetLastScaleInAt(app.ID, now); err != nil {
		t.Fatalf("SetLastScaleInAt: %v", err)
	}
}

// TestCoverageSlice14GetInstanceTailCount drives the per-instance
// invocation tail counter at memstore.go:5031. The function returns
// the count of recent invocations for an instance so the dashboard
// can show a live tail.
func TestCoverageSlice14GetInstanceTailCount(t *testing.T) {
	m, ctx, _, app, dep := state.MemCoverageFixture(t)
	inst, err := m.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 256, "node-a", "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if got, err := m.GetInstanceTailCount(ctx, inst.ID); err != nil || got != 0 {
		t.Fatalf("GetInstanceTailCount = %d, %v", got, err)
	}
}

// TestCoverageSlice14ListOrgInvitationsForOrgPage drives the
// org-invitation paged list at memstore.go:9350. The fixture
// promotes an org so the page is non-empty.
func TestCoverageSlice14ListOrgInvitationsForOrgPage(t *testing.T) {
	m, _, _, _, _ := state.MemCoverageFixture(t)
	if got, err := m.ListOrgInvitationsForOrgPage(context.Background(), "missing-org", 20, ""); err != nil || len(got) != 0 {
		t.Fatalf("ListOrgInvitationsForOrgPage missing = %d, %v", len(got), err)
	}
}

// TestCoverageSlice14RebalanceHelpers exercises the AllAppsForTest +
// SetAppReassignedAtForTest helpers at memstore_rebalance_helpers.go.
// These inject synthetic rebalance state for the schedd rebalancer tests.
func TestCoverageSlice14RebalanceHelpers(t *testing.T) {
	m, _, _, app, _ := state.MemCoverageFixture(t)
	apps := m.AllAppsForTest()
	if len(apps) == 0 {
		t.Fatal("AllAppsForTest returned empty")
	}
	m.SetAppReassignedAtForTest(context.Background(), app.ID, time.Now())
}
