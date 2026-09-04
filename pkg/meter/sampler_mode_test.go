package meter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// seedHobbyAppWithInstance creates a fresh MemStore with one Hobby
// account, one app, one deployment, and one instance in StateRunning
// with the requested mode. Returns (store, app, appID, instanceID,
// minute) so tests can drive SampleAndRoll at a fixed clock and
// assert on the resulting RolledRow.
func seedHobbyAppWithInstance(t *testing.T, mode state.InstanceMode) (*state.MemStore, state.App, string /*appID*/, string /*instID*/, time.Time) {
	t.Helper()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "u@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "u-" + string(mode), RAMMB: 256, Type: state.AppTypeApp,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	ins, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateRunning), 256, state.DefaultLocalNodeName, "", string(mode))
	if err != nil {
		t.Fatalf("CreateInstanceWithMode: %v", err)
	}
	return store, app, app.ID, ins.ID, time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
}

// TestSampler_PlanStampedOnRow pins the M-2 contract that every
// RolledRow carries its owning account's plan string so the
// meterd emit closure can label
// metered_mb_seconds_total{mode,plan} without re-reading the
// store. A regression here would force the closure to do
// ListAllAccounts per row.
func TestSampler_PlanStampedOnRow(t *testing.T) {
	store, _, appID, instID, minute := seedHobbyAppWithInstance(t, state.InstanceModeNormal)
	clock := func() time.Time { return minute }
	sampler := NewSampler(store, nil, clock)
	rows, err := sampler.SampleAndRoll(context.Background())
	if err != nil {
		t.Fatalf("SampleAndRoll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows; want 1", len(rows))
	}
	if rows[0].Plan != string(api.PlanHobby) {
		t.Errorf("Plan=%q want %q", rows[0].Plan, api.PlanHobby)
	}
	// Sanity: row traces the instance we created.
	if rows[0].AppID != appID || rows[0].InstanceID != instID {
		t.Errorf("row identity mismatch: got (%s,%s) want (%s,%s)",
			rows[0].AppID, rows[0].InstanceID, appID, instID)
	}
}

// TestSampler_WorkerModeRowNotSkipped pins the per-mode skip
// predicate widening (commit 9): worker / service / job modes
// are NOT mirror, so they pass IsMeteredSkippableMode and reach
// the row constructor. Mirror is still skipped upstream. A
// regression here would silence worker billing entirely.
func TestSampler_WorkerModeRowNotSkipped(t *testing.T) {
	cases := []struct {
		name string
		mode state.InstanceMode
		want bool // whether the row should be emitted
	}{
		{"worker_not_skipped", state.InstanceModeWorker, true},
		{"service_not_skipped", state.InstanceModeService, true},
		{"job_not_skipped", state.InstanceModeJob, true},
		{"mirror_skipped", state.InstanceModeMirror, false},
		{"normal_not_skipped", state.InstanceModeNormal, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, _, _, _, minute := seedHobbyAppWithInstance(t, tc.mode)
			clock := func() time.Time { return minute }
			sampler := NewSampler(store, nil, clock)
			rows, err := sampler.SampleAndRoll(context.Background())
			if err != nil {
				t.Fatalf("SampleAndRoll: %v", err)
			}
			got := len(rows) > 0
			if got != tc.want {
				t.Errorf("mode=%s row emitted=%v want=%v (rows=%d)",
					tc.mode, got, tc.want, len(rows))
			}
			if got {
				if rows[0].Mode != string(tc.mode) {
					t.Errorf("row.Mode=%q want %q", rows[0].Mode, tc.mode)
				}
			}
		})
	}
}

// TestEmitMeteredMB_AccumulatesByModeAndPlan pins the wire-level
// outcome: rows tagged worker + hobby must accumulate onto
// metered_mb_seconds_total{mode="worker",plan="hobby"}; rows
// tagged normal + pro must accumulate onto the pro tuple; mirror
// is filtered, empty mode falls back to "normal". A regression
// here would either merge unrelated plan/mode tuples or fail to
// record the worker billing — both are silent customer-trust bugs.
func TestEmitMeteredMB_AccumulatesByModeAndPlan(t *testing.T) {
	ops := wire.NewOpsMetrics("faas_test")
	rows := []RolledRow{
		{Mode: "worker", Plan: "hobby", MBSeconds: 1000},
		{Mode: "worker", Plan: "hobby", MBSeconds: 500},
		{Mode: "normal", Plan: "pro", MBSeconds: 250},
		{Mode: "mirror", Plan: "pro", MBSeconds: 999}, // filtered
		{Mode: "", Plan: "free", MBSeconds: 42},       // empty → "normal"
	}
	l := &Loop{ops: ops}
	l.emitMeteredMB(context.Background(), rows)

	cases := []struct {
		mode, plan string
		want       int64
	}{
		{"worker", "hobby", 1500},
		{"normal", "pro", 250},
		{"normal", "free", 42},
	}
	for _, tc := range cases {
		got := readMeteredCounter(t, ops, tc.mode, tc.plan)
		if got != tc.want {
			t.Errorf("metered_mb_seconds_total{mode=%q,plan=%q} = %d; want %d",
				tc.mode, tc.plan, got, tc.want)
		}
	}
	// Mirror must not have any series registered. The helper
	// returns 0 when no series matches — so a (mode=mirror,
	// plan=pro) tuple that has been WithLabelValues'd (via the
	// pre-instantiation) would still return its initial 0. We
	// instead check the metric explicitly: the pre-instantiation
	// table does NOT include "mirror" (it's filtered upstream), so
	// the gather below must NOT surface a mirror series. If a future
	// refactor accidentally adds "mirror" to the instantiator
	// list, this assertion will trip.
	mfs, err := ops.Registry().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if !strings.HasSuffix(mf.GetName(), "_metered_mb_seconds_total") {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "mode" && l.GetValue() == "mirror" {
					t.Errorf("unexpected mirror-mode series registered; mirror is filtered upstream and must not be pre-instantiated")
				}
			}
		}
	}
}

// TestEmitMeteredMB_NilOps_NoOp pins the nil-receiver contract:
// wiring the loop without ops (unit tests, recovery paths) does
// not panic.
func TestEmitMeteredMB_NilOps_NoOp(t *testing.T) {
	l := &Loop{ops: nil}
	rows := []RolledRow{{Mode: "worker", Plan: "pro", MBSeconds: 100}}
	// Must not panic.
	l.emitMeteredMB(context.Background(), rows)
}

// TestEmitMeteredMB_NoRows_NoOp pins the empty-input branch.
func TestEmitMeteredMB_NoRows_NoOp(t *testing.T) {
	ops := wire.NewOpsMetrics("faas_test")
	l := &Loop{ops: ops}
	l.emitMeteredMB(context.Background(), nil)
	l.emitMeteredMB(context.Background(), []RolledRow{})
}

// readMeteredCounter returns the current value of
// metered_mb_seconds_total{mode,plan} via ops.Gather. Returns
// 0 for un-registered tuples (which can include "free" plan
// when no row hit it — the helper can't distinguish 0-valued
// from never-emitted, but the tests construct both tuples at
// least once, so the assertion's expected value > 0 is the
// distinguishing signal).
func readMeteredCounter(t *testing.T, ops *wire.OpsMetrics, mode, plan string) int64 {
	t.Helper()
	mfs, err := ops.Registry().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if !strings.HasSuffix(mf.GetName(), "_metered_mb_seconds_total") {
			continue
		}
		for _, m := range mf.GetMetric() {
			modeMatch, planMatch := "", ""
			for _, l := range m.GetLabel() {
				switch l.GetName() {
				case "mode":
					modeMatch = l.GetValue()
				case "plan":
					planMatch = l.GetValue()
				}
			}
			if modeMatch == mode && planMatch == plan {
				return int64(m.GetCounter().GetValue())
			}
		}
	}
	return 0
}
