// adr: 097 — warm-pool wake telemetry and resident-capacity observability.
package sched

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestWarmPoolSizeGaugeProjectsResidentRows pins issue #1056 / ADR-074:
// vmmd_warm_pool_size{plan} is the actual resident paused-row count, not the
// requested target. A parked row must disappear from the gauge on the next
// reconciliation pass.
func TestWarmPoolSizeGaugeProjectsResidentRows(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	target := 2
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{WarmPoolSize: &target, SetWarmPoolSize: true}); err != nil {
		t.Fatalf("UpdateApp warm_pool_size: %v", err)
	}
	var firstWarmID string
	for i, wakeID := range []string{"warm-1", "warm-2"} {
		ins, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateWarm), app.RAMMB, state.DefaultLocalNodeName, wakeID, string(state.InstanceModeNormal))
		if err != nil {
			t.Fatalf("Create warm instance %s: %v", wakeID, err)
		}
		if i == 0 {
			firstWarmID = ins.ID
		}
	}
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	if err := e.ReconcileWarmPool(ctx, app.ID); err != nil {
		t.Fatalf("ReconcileWarmPool: %v", err)
	}
	assertWarmPoolGauge(t, ops, string(acct.Plan), 2)

	if err := store.UpdateInstanceState(ctx, firstWarmID, string(state.StateParked)); err != nil {
		t.Fatalf("park warm row: %v", err)
	}
	if err := e.ReconcileWarmPool(ctx, app.ID); err != nil {
		t.Fatalf("ReconcileWarmPool after park: %v", err)
	}
	assertWarmPoolGauge(t, ops, string(acct.Plan), 1)
}

// TestWarmPoolResumePhaseMetric pins ADR-097's bounded phase vocabulary for
// the warm-pool path and preserves the wake_id exemplar without making it a
// Prometheus label.
func TestWarmPoolResumePhaseMetric(t *testing.T) {
	ops := wire.NewOpsMetrics("schedd")
	e := &Engine{ops: ops}
	e.observeWarmResumeDuration("app-1", "wake-1", 25*time.Millisecond)
	if got := readWakeRPC(t, ops, "app-1", "resume", "count"); got != 1 {
		t.Fatalf("resume count = %v, want 1", got)
	}
	if got := readWakeRPC(t, ops, "", "resume", "count"); got != 0 {
		t.Fatalf("empty-app resume count = %v, want 0", got)
	}
}

// TestWakePromotesWarmRowRecordsResumePhase pins the end-to-end handoff from
// a resident WARM row to serving capacity: no cold/restore RPC is used, the
// ledger starts counting serving concurrency, and the resume phase is emitted.
func TestWakePromotesWarmRowRecordsResumePhase(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	target := 1
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{WarmPoolSize: &target, SetWarmPoolSize: true}); err != nil {
		t.Fatalf("UpdateApp warm_pool_size: %v", err)
	}
	warm, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateWarm), app.RAMMB, state.DefaultLocalNodeName, "warm-wake", string(state.InstanceModeNormal))
	if err != nil {
		t.Fatalf("Create warm instance: %v", err)
	}
	if err := store.SetInstanceRuntime(ctx, warm.ID, "fc-"+warm.ID, "10.100.0.2", 20001); err != nil {
		t.Fatalf("SetInstanceRuntime: %v", err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	vmm := &warmResumeFakeVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(wire.NewOpsMetrics("schedd"))
	if err := e.Ledger().Admit(Request{
		Instance: warm.ID, AppID: app.ID, DeploymentID: dep.ID, Plan: api.PlanPro,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, MaxConcurrency: app.MaxConcurrency,
		NodeID: state.DefaultLocalNodeName, Kind: KindWarmPool,
	}); err != nil {
		t.Fatalf("Admit warm reservation: %v", err)
	}
	result, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if result.InstanceID != warm.ID || vmm.resumeCalls != 1 || vmm.coldBoots != 0 || vmm.restores != 0 {
		t.Fatalf("result=%+v resume=%d cold=%d restore=%d", result, vmm.resumeCalls, vmm.coldBoots, vmm.restores)
	}
	fresh, err := store.InstanceByID(ctx, warm.ID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if fresh.State != string(state.StateRunning) || e.Ledger().Concurrency(app.ID) != 1 {
		t.Fatalf("state=%q concurrency=%d, want running/1", fresh.State, e.Ledger().Concurrency(app.ID))
	}
	if got := readWakeRPC(t, e.ops, app.ID, "resume", "count"); got != 1 {
		t.Fatalf("resume count = %v, want 1", got)
	}
	assertWarmPoolGauge(t, e.ops, string(api.PlanPro), 0)
}

type warmResumeFakeVMM struct {
	*fakeVMM
	resumeCalls int
}

func (f *warmResumeFakeVMM) ResumeWarmInstance(context.Context, string, string) error {
	f.resumeCalls++
	return nil
}

func assertWarmPoolGauge(t *testing.T, ops *wire.OpsMetrics, plan string, want int) {
	t.Helper()
	body := getMetricsBody(t, ops)
	needle := `schedd_warm_pool_size{plan="` + plan + `"}`
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, needle) {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[len(fields)-1] == strconv.Itoa(want) {
				return
			}
			t.Fatalf("gauge line = %q, want %d", line, want)
		}
	}
	t.Fatalf("missing gauge %q", needle)
}
