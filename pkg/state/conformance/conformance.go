// Package conformance contains the behavioral contract shared by the two
// state.Store implementations. Every driver runs the same cases against a
// fresh store so a MemStore-only fix cannot drift from the PostgreSQL path.
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Open creates a clean store for one subtest. The callback owns any database
// pool and should register cleanup with t when the store is PostgreSQL-backed.
type Open func(t *testing.T) state.Store

// Fixture is the common account/app/deployment/node shape used by the
// conformance cases. Seed creates it through Store methods only, so both
// implementations exercise the same public boundary.
type Fixture struct {
	Store      state.Store
	Ctx        context.Context
	Account    state.Account
	App        state.App
	Deployment state.Deployment
	Node       state.ComputeNode
}

// Run executes the shared state.Store contract. Keep each case independent:
// state tests intentionally mutate rows to make divergence visible.
//
// Assert ABSOLUTE expected values, never merely that the two stores agree.
// The live-state readers were broken identically in both implementations —
// uppercase state literals in PgStore's SQL and in MemStore's
// isInstanceStateLive — so both returned zero and an agreement check would
// have passed. Only "three live instances must count as three" caught it.
func Run(t *testing.T, open Open) {
	t.Helper()
	tests := []struct {
		name string
		fn   func(*testing.T, *Fixture)
	}{
		{"app_limits_are_persisted_for_each_plan", testAppLimits},
		{"vmmd_upsert_preserves_operator_state", testVmmdUpsertPreservesOperatorState},
		{"deployment_live_pointer_swaps_atomically", testDeploymentLivePointer},
		{"usage_rollup_merges_minutes", testUsageRollup},
		{"invalid_instance_state_is_rejected", testInvalidInstanceState},
		{"live_state_readers_count_running_instances", testLiveStateReaders},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.fn(t, Seed(t, open(t)))
		})
	}
}

// Seed builds a production-shaped fixture through the Store interface.
func Seed(t *testing.T, store state.Store) *Fixture {
	t.Helper()
	ctx := context.Background()
	plan := api.PlanPro
	limits := api.MustLimitsFor(plan)
	acct, err := store.CreateAccount(ctx, "conformance-"+uuid.NewString()+"@example.com", plan)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID:      acct.ID,
		Slug:           "conformance-" + uuid.NewString(),
		Type:           state.AppTypeApp,
		Runtime:        "node22",
		RAMMB:          limits.RAMMB,
		MaxConcurrency: limits.MaxConcurrency,
		IdleTimeoutS:   limits.IdleTimeoutS,
	}, limits)
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:conformance",
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	dep, err = store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:               "conformance-" + uuid.NewString(),
		TargetURL:          "unix:///tmp/conformance-vmmd.sock",
		VPCPUs:             2,
		MemMB:              4096,
		MaxConcurrency:     20,
		AdmissionCeilingMB: 4096,
		VCPUBudget:         2,
		Lifecycle:          state.NodeLifecycleActive,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	return &Fixture{Store: store, Ctx: ctx, Account: acct, App: app, Deployment: dep, Node: node}
}

func testAppLimits(t *testing.T, fx *Fixture) {
	for _, plan := range api.Plans {
		limits := api.MustLimitsFor(plan)
		acct, err := fx.Store.CreateAccount(fx.Ctx, "limits-"+string(plan)+"-"+uuid.NewString()+"@example.com", plan)
		if err != nil {
			t.Fatalf("CreateAccount(%s): %v", plan, err)
		}
		app, err := fx.Store.CreateAppIfUnderQuota(fx.Ctx, state.App{
			AccountID:      acct.ID,
			Slug:           "limits-" + string(plan) + "-" + uuid.NewString(),
			Type:           state.AppTypeApp,
			RAMMB:          limits.RAMMB,
			MaxConcurrency: limits.MaxConcurrency,
		}, limits)
		if err != nil {
			t.Fatalf("CreateAppIfUnderQuota(%s): %v", plan, err)
		}
		if app.RAMMB != limits.RAMMB || app.MaxConcurrency != limits.MaxConcurrency {
			t.Errorf("%s limits = (ram=%d, concurrency=%d), want (%d, %d)", plan, app.RAMMB, app.MaxConcurrency, limits.RAMMB, limits.MaxConcurrency)
		}
	}
}

func testVmmdUpsertPreservesOperatorState(t *testing.T, fx *Fixture) {
	operatorNode, err := fx.Store.CreateComputeNode(fx.Ctx, state.ComputeNode{
		Name:               "operator-" + uuid.NewString(),
		TargetURL:          "unix:///run/faas/operator-vmmd.sock",
		VPCPUs:             2,
		MemMB:              4096,
		MaxConcurrency:     10,
		AdmissionCeilingMB: 4096,
		VCPUBudget:         2,
		Lifecycle:          state.NodeLifecycleDraining,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	updated, err := fx.Store.UpsertComputeNodeFromVmmd(fx.Ctx, state.ComputeNode{
		Name:               operatorNode.Name,
		TargetURL:          "unix:///run/faas/vmmd-restarted.sock",
		VPCPUs:             8,
		MemMB:              8192,
		MaxConcurrency:     40,
		AdmissionCeilingMB: 8192,
		VCPUBudget:         8,
		Lifecycle:          state.NodeLifecycleActive,
	})
	if err != nil {
		t.Fatalf("UpsertComputeNodeFromVmmd: %v", err)
	}
	if updated.ID != operatorNode.ID {
		t.Errorf("upsert changed id from %q to %q", operatorNode.ID, updated.ID)
	}
	if updated.TargetURL != operatorNode.TargetURL {
		t.Errorf("upsert changed operator target URL to %q", updated.TargetURL)
	}
	if updated.Lifecycle != state.NodeLifecycleDraining || updated.Active {
		t.Errorf("upsert changed operator lifecycle to %q (active=%v)", updated.Lifecycle, updated.Active)
	}
	if updated.MemMB != 8192 || updated.MaxConcurrency != 40 || updated.VCPUBudget != 8 {
		t.Errorf("upsert did not refresh vmmd capacity: %+v", updated)
	}
}

func testDeploymentLivePointer(t *testing.T, fx *Fixture) {
	next, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
		AppID:       fx.App.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:next",
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := fx.Store.MarkDeploymentLive(fx.Ctx, next.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	live, err := fx.Store.LiveDeployment(fx.Ctx, fx.App.ID)
	if err != nil {
		t.Fatalf("LiveDeployment: %v", err)
	}
	if live.ID != next.ID {
		t.Fatalf("live deployment = %q, want %q", live.ID, next.ID)
	}
	old, err := fx.Store.DeploymentByID(fx.Ctx, fx.Deployment.ID)
	if err != nil {
		t.Fatalf("DeploymentByID(old): %v", err)
	}
	if old.Status != state.DeploySuperseded {
		t.Errorf("old deployment status = %q, want %q", old.Status, state.DeploySuperseded)
	}
}

func testUsageRollup(t *testing.T, fx *Fixture) {
	minute := time.Date(2026, 9, 7, 12, 34, 0, 0, time.UTC)
	instanceID := uuid.NewString()
	perSecond := int64(fx.App.RAMMB + api.PerVMOverheadMB)
	firstMBSeconds := perSecond * 2
	if err := fx.Store.AppendUsage(fx.Ctx, fx.Account.ID, fx.App.ID, instanceID, minute, firstMBSeconds, 2, 3, 4, 5, 6, 1, 7); err != nil {
		t.Fatalf("AppendUsage(first): %v", err)
	}
	if err := fx.Store.AppendUsage(fx.Ctx, fx.Account.ID, fx.App.ID, instanceID, minute, 999, 999, 11, 13, 17, 19, 23, 29); err != nil {
		t.Fatalf("AppendUsage(second): %v", err)
	}
	rows, err := fx.Store.UsageByMonth(fx.Ctx, fx.Account.ID, minute)
	if err != nil {
		t.Fatalf("UsageByMonth: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("UsageByMonth rows = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.AppID != fx.App.ID || got.MBSeconds != firstMBSeconds || got.Requests != 2 {
		t.Errorf("billing rollup = %+v, want mb=%d requests=2", got, firstMBSeconds)
	}
	if got.CPUUsec != 14 || got.TXBytes != 17 || got.NetTxBytes != 22 || got.NetRxBytes != 25 || got.ColdBootCount != 24 {
		t.Errorf("telemetry rollup = %+v", got)
	}
}

func testInvalidInstanceState(t *testing.T, fx *Fixture) {
	if _, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID, "not-a-real-state", 512, fx.Node.ID, uuid.NewString()); err == nil {
		t.Fatal("CreateInstance accepted an invalid state")
	}
	ins, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID, string(state.StateRunning), 512, fx.Node.ID, uuid.NewString())
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := fx.Store.UpdateInstanceState(fx.Ctx, ins.ID, "not-a-real-state"); err == nil {
		t.Fatal("UpdateInstanceState accepted an invalid state")
	}
	got, err := fx.Store.InstanceByID(fx.Ctx, ins.ID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if got.State != string(state.StateRunning) {
		t.Errorf("invalid update changed state to %q", got.State)
	}
	if err := fx.Store.UpdateInstanceState(fx.Ctx, ins.ID, string(state.StateParked)); err != nil {
		t.Fatalf("UpdateInstanceState(valid): %v", err)
	}
}

// testLiveStateReaders pins every reader that filters on the three live
// instance states. instances.state is lowercase (machine.go, and the SQL
// CHECK since migration 00001); both stores once compared it against
// 'RUNNING' / 'WAKING' / 'COLD_BOOTING' and so counted nothing:
// PgStore in four SQL statements, MemStore via isInstanceStateLive.
//
// The per-node assertions sum across rows rather than looking a node up by
// name, because PerNodeStats.NodeName is genuinely implementation-defined:
// PgStore joins compute_nodes and returns the name, MemStore returns the
// node uuid and leaves the mapping to the caller. The counts are the
// contract; the label is not.
func testLiveStateReaders(t *testing.T, fx *Fixture) {
	live := []struct {
		st    state.State
		ramMB int
	}{
		{state.StateRunning, 512},
		{state.StateWaking, 256},
		{state.StateColdBooting, 128},
	}
	var wantRAM int64
	for _, l := range live {
		if _, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID, string(l.st), l.ramMB, fx.Node.ID, uuid.NewString()); err != nil {
			t.Fatalf("CreateInstance(%s): %v", l.st, err)
		}
		wantRAM += int64(l.ramMB) + 8
	}
	// A parked instance must not be counted by any of them.
	parked, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID, string(state.StateRunning), 1024, fx.Node.ID, uuid.NewString())
	if err != nil {
		t.Fatalf("CreateInstance(to-park): %v", err)
	}
	if err := fx.Store.UpdateInstanceState(fx.Ctx, parked.ID, string(state.StateParked)); err != nil {
		t.Fatalf("UpdateInstanceState(parked): %v", err)
	}
	const wantLive = 3

	if got, err := fx.Store.ConcurrencyForDeployment(fx.Ctx, fx.App.ID, fx.Deployment.ID); err != nil {
		t.Fatalf("ConcurrencyForDeployment: %v", err)
	} else if got != wantLive {
		t.Errorf("ConcurrencyForDeployment = %d, want %d", got, wantLive)
	}

	if got, err := fx.Store.CountLiveInstancesByDeployment(fx.Ctx, fx.Deployment.ID); err != nil {
		t.Fatalf("CountLiveInstancesByDeployment: %v", err)
	} else if got != wantLive {
		t.Errorf("CountLiveInstancesByDeployment = %d, want %d", got, wantLive)
	}

	rows, err := fx.Store.PerNodeLiveStats(fx.Ctx)
	if err != nil {
		t.Fatalf("PerNodeLiveStats: %v", err)
	}
	var gotLive, gotRunning, gotWaking, gotCold, gotRAM int64
	for _, r := range rows {
		gotLive += r.InstancesLive
		gotRunning += r.InstancesRunning
		gotWaking += r.InstancesWaking
		gotCold += r.InstancesColdBooting
		gotRAM += r.RAMUsedMB
	}
	if gotLive != wantLive {
		t.Errorf("PerNodeLiveStats live total = %d, want %d (zero here means the operator per-node pane reads empty while instances run)", gotLive, wantLive)
	}
	if gotRunning != 1 || gotWaking != 1 || gotCold != 1 {
		t.Errorf("PerNodeLiveStats per-state totals = running %d / waking %d / cold_booting %d, want 1/1/1", gotRunning, gotWaking, gotCold)
	}
	if gotRAM != wantRAM {
		t.Errorf("PerNodeLiveStats RAM total = %d, want %d (plan RAM + 8 per live instance)", gotRAM, wantRAM)
	}

	snap, err := fx.Store.OperatorCapacity(fx.Ctx)
	if err != nil {
		t.Fatalf("OperatorCapacity: %v", err)
	}
	var capLive, capRAM int64
	for _, n := range snap.Nodes {
		capLive += n.InstancesLive
		capRAM += n.RAMUsedMB
	}
	if capLive != wantLive {
		t.Errorf("OperatorCapacity live total = %d, want %d", capLive, wantLive)
	}
	if capRAM != wantRAM {
		t.Errorf("OperatorCapacity RAM total = %d, want %d", capRAM, wantRAM)
	}
}
