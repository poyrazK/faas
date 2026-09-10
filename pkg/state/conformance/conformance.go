// Package conformance contains the behavioral contract shared by the two
// state.Store implementations. Every driver runs the same cases against a
// fresh store so a MemStore-only fix cannot drift from the PostgreSQL path.
package conformance

import (
	"context"
	"errors"
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
		{"beta_first_success_includes_parked_instances", testBetaFirstSuccess},
		{"account_credits_issue_list_and_consume", testAccountCredits},
		{"overage_cap_distinguishes_zero_from_unset", testOverageCap},
		{"cron_quota_trips_at_the_per_app_limit", testCronQuota},
		{"export_history_pagination_is_stable", testExportHistoryPagination},
		{"latest_deployment_per_app_is_scoped_and_stable", testLatestDeploymentPerApp},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.fn(t, Seed(t, open(t)))
		})
	}
}

func testBetaFirstSuccess(t *testing.T, fx *Fixture) {
	firstInstance, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID,
		string(state.StateParked), 128, fx.Node.ID, uuid.NewString())
	if err != nil {
		t.Fatalf("CreateInstance(parked): %v", err)
	}
	secondInstance, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID,
		string(state.StateRunning), 128, fx.Node.ID, uuid.NewString())
	if err != nil {
		t.Fatalf("CreateInstance(running): %v", err)
	}
	firstAt := fx.Account.CreatedAt.Add(10 * time.Second)
	secondAt := fx.Account.CreatedAt.Add(30 * time.Second)
	if _, err := fx.Store.TouchInstancesLastSeen(fx.Ctx, []state.InstanceTouch{
		{InstanceID: secondInstance.ID, LastRequest: secondAt},
		{InstanceID: firstInstance.ID, LastRequest: firstAt},
	}); err != nil {
		t.Fatalf("TouchInstancesLastSeen: %v", err)
	}
	rows, err := fx.Store.ListFirstSuccessfulRequestsForAccountsCreatedSince(
		fx.Ctx, fx.Account.CreatedAt.Add(-time.Second))
	if err != nil {
		t.Fatalf("ListFirstSuccessfulRequestsForAccountsCreatedSince: %v", err)
	}
	if len(rows) != 1 || rows[0].AccountID != fx.Account.ID || !rows[0].At.Equal(firstAt) {
		t.Fatalf("first-success rows = %+v, want account=%s at=%s", rows, fx.Account.ID, firstAt)
	}
	rows, err = fx.Store.ListFirstSuccessfulRequestsForAccountsCreatedSince(
		fx.Ctx, fx.Account.CreatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("ListFirstSuccessfulRequestsForAccountsCreatedSince(excluded): %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("excluded cohort rows = %+v, want none", rows)
	}
}

func testLatestDeploymentPerApp(t *testing.T, fx *Fixture) {
	stamp := time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC)
	lowID := "40000000-0000-0000-0000-000000000001"
	highID := "40000000-0000-0000-0000-000000000002"
	for _, id := range []string{lowID, highID} {
		if _, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
			ID: id, AppID: fx.App.ID, CreatedAt: stamp,
			Kind: state.DeploymentKindImage, Status: state.DeployFailed,
		}); err != nil {
			t.Fatalf("CreateDeployment(%s): %v", id, err)
		}
	}

	limits := api.MustLimitsFor(api.PlanPro)
	secondApp, err := fx.Store.CreateAppIfUnderQuota(fx.Ctx, state.App{
		AccountID: fx.Account.ID, Slug: "latest-second-" + uuid.NewString(),
		Type: state.AppTypeApp, RAMMB: limits.RAMMB,
		MaxConcurrency: limits.MaxConcurrency, IdleTimeoutS: limits.IdleTimeoutS,
	}, limits)
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota(second): %v", err)
	}
	secondDeployment, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
		AppID: secondApp.ID, CreatedAt: stamp.Add(time.Minute),
		Kind: state.DeploymentKindImage, Status: state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(second): %v", err)
	}

	foreignAccount, err := fx.Store.CreateAccount(fx.Ctx, "latest-foreign-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount(foreign): %v", err)
	}
	foreignApp, err := fx.Store.CreateAppIfUnderQuota(fx.Ctx, state.App{
		AccountID: foreignAccount.ID, Slug: "latest-foreign-" + uuid.NewString(),
		Type: state.AppTypeApp, RAMMB: limits.RAMMB,
		MaxConcurrency: limits.MaxConcurrency, IdleTimeoutS: limits.IdleTimeoutS,
	}, limits)
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota(foreign): %v", err)
	}
	if _, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
		AppID: foreignApp.ID, CreatedAt: stamp.Add(2 * time.Minute),
		Kind: state.DeploymentKindImage, Status: state.DeployPending,
	}); err != nil {
		t.Fatalf("CreateDeployment(foreign): %v", err)
	}

	got, err := fx.Store.ListLatestDeploymentPerApp(fx.Ctx, fx.Account.ID)
	if err != nil {
		t.Fatalf("ListLatestDeploymentPerApp: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("latest map = %d rows, want 2: %+v", len(got), got)
	}
	if got[fx.App.ID].ID != highID {
		t.Errorf("fixture app latest = %q, want tie-break winner %q", got[fx.App.ID].ID, highID)
	}
	if got[secondApp.ID].ID != secondDeployment.ID {
		t.Errorf("second app latest = %q, want %q", got[secondApp.ID].ID, secondDeployment.ID)
	}
	if _, ok := got[foreignApp.ID]; ok {
		t.Error("foreign account deployment leaked into latest map")
	}
}

func testExportHistoryPagination(t *testing.T, fx *Fixture) {
	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	depIDs := []string{
		"20000000-0000-0000-0000-000000000001",
		"20000000-0000-0000-0000-000000000002",
		"20000000-0000-0000-0000-000000000003",
	}
	for _, id := range depIDs {
		if _, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
			ID: id, AppID: fx.App.ID, CreatedAt: at,
			Kind: state.DeploymentKindImage, Status: state.DeployFailed,
		}); err != nil {
			t.Fatalf("CreateDeployment(%s): %v", id, err)
		}
	}
	depPage, err := fx.Store.ListDeploymentsForAccountPage(fx.Ctx, fx.Account.ID, time.Time{}, "", 2)
	if err != nil {
		t.Fatalf("ListDeploymentsForAccountPage(first): %v", err)
	}
	if len(depPage) != 2 || depPage[0].ID != depIDs[2] || depPage[1].ID != depIDs[1] {
		t.Fatalf("deployment first page = %+v, want ids 3,2", depPage)
	}
	depNext, err := fx.Store.ListDeploymentsForAccountPage(fx.Ctx, fx.Account.ID, depPage[1].CreatedAt, depPage[1].ID, 2)
	if err != nil {
		t.Fatalf("ListDeploymentsForAccountPage(next): %v", err)
	}
	if len(depNext) == 0 || depNext[0].ID != depIDs[0] {
		t.Fatalf("deployment next page = %+v, want id 1 first", depNext)
	}

	gdprIDs := []string{
		"30000000-0000-0000-0000-000000000001",
		"30000000-0000-0000-0000-000000000002",
		"30000000-0000-0000-0000-000000000003",
	}
	for _, id := range gdprIDs {
		if err := fx.Store.AppendGdprRequest(fx.Ctx, state.GdprRequest{
			ID: id, AccountID: fx.Account.ID, AccountEmail: fx.Account.Email,
			Action: state.GdprActionDelete, RequestedAt: at,
		}); err != nil {
			t.Fatalf("AppendGdprRequest(%s): %v", id, err)
		}
	}
	gdprPage, err := fx.Store.ListGdprRequestsForAccountPage(fx.Ctx, fx.Account.ID, time.Time{}, "", 2)
	if err != nil {
		t.Fatalf("ListGdprRequestsForAccountPage(first): %v", err)
	}
	if len(gdprPage) != 2 || gdprPage[0].ID != gdprIDs[2] || gdprPage[1].ID != gdprIDs[1] {
		t.Fatalf("GDPR first page = %+v, want ids 3,2", gdprPage)
	}
	gdprNext, err := fx.Store.ListGdprRequestsForAccountPage(fx.Ctx, fx.Account.ID, gdprPage[1].RequestedAt, gdprPage[1].ID, 2)
	if err != nil {
		t.Fatalf("ListGdprRequestsForAccountPage(next): %v", err)
	}
	if len(gdprNext) != 1 || gdprNext[0].ID != gdprIDs[0] {
		t.Fatalf("GDPR next page = %+v, want id 1", gdprNext)
	}

	for i := 0; i < 3; i++ {
		if err := fx.Store.AppendEvent(fx.Ctx, "conformance", "export.page", &fx.Account.ID, []byte(`{}`)); err != nil {
			t.Fatalf("AppendEvent(%d): %v", i, err)
		}
	}
	eventPage, err := fx.Store.ListEventsPage(fx.Ctx, fx.Account.ID, time.Time{}, 0, 2)
	if err != nil {
		t.Fatalf("ListEventsPage(first): %v", err)
	}
	if len(eventPage) != 2 || eventPage[0].ID <= eventPage[1].ID {
		t.Fatalf("event first page = %+v, want descending ids", eventPage)
	}
	eventNext, err := fx.Store.ListEventsPage(fx.Ctx, fx.Account.ID, eventPage[1].At, eventPage[1].ID, 2)
	if err != nil {
		t.Fatalf("ListEventsPage(next): %v", err)
	}
	if len(eventNext) != 1 || eventNext[0].ID >= eventPage[1].ID {
		t.Fatalf("event next page = %+v, want final lower id", eventNext)
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

// testAccountCredits pins the credit ledger: what is issued, what is
// visible for consumption, what a consumption deducts, and — the part that
// costs real money if the two stores disagree — that re-consuming the same
// provider invoice is a no-op rather than a second deduction.
func testAccountCredits(t *testing.T, fx *Fixture) {
	mk := func(cents int64, reason string, expires *time.Time) state.AccountCredit {
		c, err := fx.Store.CreateAccountCredit(fx.Ctx, state.AccountCredit{
			AccountID:      fx.Account.ID,
			CentsRemaining: cents,
			Reason:         reason,
			ExpiresAt:      expires,
		})
		if err != nil {
			t.Fatalf("CreateAccountCredit(%s): %v", reason, err)
		}
		if c.ID == "" {
			t.Fatalf("CreateAccountCredit(%s) returned no ID", reason)
		}
		return c
	}
	past := time.Now().Add(-time.Hour)
	mk(500, "goodwill", nil)
	mk(300, "promo", nil)
	mk(900, "expired", &past)

	// An expired credit must not be consumable. If one store filters on
	// expires_at and the other does not, a customer is billed against
	// money that is gone.
	active, err := fx.Store.ListActiveCreditsForConsumption(fx.Ctx, fx.Account.ID)
	if err != nil {
		t.Fatalf("ListActiveCreditsForConsumption: %v", err)
	}
	var activeTotal int64
	for _, c := range active {
		activeTotal += c.CentsRemaining
		if c.Reason == "expired" {
			t.Errorf("expired credit is consumable: %+v", c)
		}
	}
	if activeTotal != 800 {
		t.Errorf("active credit total = %d, want 800 (500 + 300; the 900 is expired)", activeTotal)
	}

	const invoice = "in_conformance_1"
	res, err := fx.Store.ConsumeAccountCredit(fx.Ctx, state.ConsumeAccountCreditParams{
		AccountID:         fx.Account.ID,
		TargetCents:       600,
		Provider:          "stripe",
		ProviderInvoiceID: invoice,
		InvoiceID:         invoice,
		Reason:            "conformance",
		Actor:             "apid",
	})
	if err != nil {
		t.Fatalf("ConsumeAccountCredit: %v", err)
	}
	if res.ConsumedCents != 600 {
		t.Errorf("ConsumedCents = %d, want 600", res.ConsumedCents)
	}
	if res.RemainingCreditsCents != 200 {
		t.Errorf("RemainingCreditsCents = %d, want 200 (800 - 600)", res.RemainingCreditsCents)
	}
	if res.AlreadyConsumedForInvoice {
		t.Error("first consumption reported AlreadyConsumedForInvoice")
	}

	// Replaying the same provider invoice must not deduct a second time.
	//
	// The contract is deliberately NOT "ConsumedCents == 0" on replay: the
	// per-(invoice, credit) partial unique index on credit_ledger blocks
	// the second deduction, and the reducer then re-derives ConsumedCents
	// from the existing ledger rows so an operator inspecting either call
	// sees the same total. The money property is the BALANCE, so that is
	// what this asserts.
	again, err := fx.Store.ConsumeAccountCredit(fx.Ctx, state.ConsumeAccountCreditParams{
		AccountID:         fx.Account.ID,
		TargetCents:       600,
		Provider:          "stripe",
		ProviderInvoiceID: invoice,
		InvoiceID:         invoice,
		Reason:            "conformance-replay",
		Actor:             "apid",
	})
	if err != nil {
		t.Fatalf("ConsumeAccountCredit(replay): %v", err)
	}
	if !again.AlreadyConsumedForInvoice {
		t.Error("replaying the same provider invoice was not reported as already consumed")
	}
	if again.ConsumedCents != res.ConsumedCents {
		t.Errorf("replay ConsumedCents = %d, want %d (re-derived from the ledger so both callers see one total)",
			again.ConsumedCents, res.ConsumedCents)
	}

	post, err := fx.Store.ListActiveCreditsForConsumption(fx.Ctx, fx.Account.ID)
	if err != nil {
		t.Fatalf("ListActiveCreditsForConsumption(post): %v", err)
	}
	var postTotal int64
	for _, c := range post {
		postTotal += c.CentsRemaining
	}
	if postTotal != 200 {
		t.Errorf("active total after the replay = %d, want 200 — the duplicate invoice deducted real money a second time", postTotal)
	}
	if again.RemainingCreditsCents != 200 {
		t.Errorf("replay RemainingCreditsCents = %d, want 200", again.RemainingCreditsCents)
	}
}

// testOverageCap pins the three-way distinction the reader's (cents, ok)
// shape exists for: no cap at all, a cap of exactly zero meaning "no
// overage allowed", and a positive cap. A store that collapses SQL NULL
// into a Go zero would report "no overage allowed" for an account that
// never set a cap, and stop billing that should happen.
func testOverageCap(t *testing.T, fx *Fixture) {
	if _, ok, err := fx.Store.GetAccountOverageCapCents(fx.Ctx, fx.Account.ID); err != nil {
		t.Fatalf("GetAccountOverageCapCents(unset): %v", err)
	} else if ok {
		t.Error("a fresh account reported a cap; want ok=false (no cap)")
	}

	zero := int64(0)
	if err := fx.Store.UpdateAccountOverageCapCents(fx.Ctx, fx.Account.ID, &zero); err != nil {
		t.Fatalf("UpdateAccountOverageCapCents(0): %v", err)
	}
	cents, ok, err := fx.Store.GetAccountOverageCapCents(fx.Ctx, fx.Account.ID)
	if err != nil {
		t.Fatalf("GetAccountOverageCapCents(0): %v", err)
	}
	if !ok || cents != 0 {
		t.Errorf("cap of 0 read back as (%d, %v), want (0, true) — 0 means no overage allowed and must not read as unset", cents, ok)
	}

	fifty := int64(5000)
	if err := fx.Store.UpdateAccountOverageCapCents(fx.Ctx, fx.Account.ID, &fifty); err != nil {
		t.Fatalf("UpdateAccountOverageCapCents(5000): %v", err)
	}
	if cents, ok, err := fx.Store.GetAccountOverageCapCents(fx.Ctx, fx.Account.ID); err != nil {
		t.Fatalf("GetAccountOverageCapCents(5000): %v", err)
	} else if !ok || cents != 5000 {
		t.Errorf("cap read back as (%d, %v), want (5000, true)", cents, ok)
	}

	if err := fx.Store.UpdateAccountOverageCapCents(fx.Ctx, fx.Account.ID, nil); err != nil {
		t.Fatalf("UpdateAccountOverageCapCents(nil): %v", err)
	}
	if _, ok, err := fx.Store.GetAccountOverageCapCents(fx.Ctx, fx.Account.ID); err != nil {
		t.Fatalf("GetAccountOverageCapCents(cleared): %v", err)
	} else if ok {
		t.Error("cap still reported after clearing with nil; want ok=false")
	}
}

// testCronQuota pins that the per-app cron cap trips at the limit and
// reports it as a *CronQuotaError naming the scope, limit and observed
// count. The limit is a parameter, so the case uses a tiny one rather than
// creating twenty rows.
func testCronQuota(t *testing.T, fx *Fixture) {
	limits := api.MustLimitsFor(api.PlanPro)
	limits.CronLimitPerApp = 2
	limits.CronLimitPerAccount = 100

	for i := 0; i < limits.CronLimitPerApp; i++ {
		if _, err := fx.Store.CreateCronIfUnderQuota(fx.Ctx, fx.App.ID, "*/5 * * * *", "/cron/"+uuid.NewString(), true, limits); err != nil {
			t.Fatalf("CreateCronIfUnderQuota(%d of %d): %v", i+1, limits.CronLimitPerApp, err)
		}
	}

	_, err := fx.Store.CreateCronIfUnderQuota(fx.Ctx, fx.App.ID, "*/5 * * * *", "/cron/"+uuid.NewString(), true, limits)
	if err == nil {
		t.Fatalf("cron %d was accepted; the per-app cap is %d", limits.CronLimitPerApp+1, limits.CronLimitPerApp)
	}
	var qe *state.CronQuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("quota breach returned %v (%T), want *state.CronQuotaError", err, err)
	}
	if qe.Scope != state.CronQuotaScopeApp {
		t.Errorf("Scope = %q, want %q", qe.Scope, state.CronQuotaScopeApp)
	}
	if qe.Limit != limits.CronLimitPerApp {
		t.Errorf("Limit = %d, want %d", qe.Limit, limits.CronLimitPerApp)
	}
	if qe.Observed != limits.CronLimitPerApp {
		t.Errorf("Observed = %d, want %d", qe.Observed, limits.CronLimitPerApp)
	}
}
