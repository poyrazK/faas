// spec: §6.2
// adr: 20261022184531
package sched_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	dto "github.com/prometheus/client_model/go"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
)

// refusingVMM is a routed vmmd fake whose nodes can refuse a wake before
// starting the VM, as fsn-3's rc.288 vmmd did on 2026-10-22.
type refusingVMM struct {
	*sched.FakeVMM
	mu     sync.Mutex
	refuse func(nodeID string) error
	calls  map[string]int
}

func newRefusingVMM(refuse func(nodeID string) error) *refusingVMM {
	return &refusingVMM{FakeVMM: &sched.FakeVMM{}, refuse: refuse, calls: map[string]int{}}
}

func (v *refusingVMM) attempt(nodeID string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls[nodeID]++
	if v.refuse == nil {
		return nil
	}
	return v.refuse(nodeID)
}

func (v *refusingVMM) CreateColdBoot(ctx context.Context, nodeID, instance string, app sched.AppSpec) (*sched.WakeOutcome, error) {
	if err := v.attempt(nodeID); err != nil {
		return nil, err
	}
	return v.FakeVMM.CreateColdBoot(ctx, nodeID, instance, app)
}

func (v *refusingVMM) CreateFromSnapshot(ctx context.Context, nodeID, instance string, app sched.AppSpec, snap sched.SnapshotRef) (*sched.WakeOutcome, error) {
	if err := v.attempt(nodeID); err != nil {
		return nil, err
	}
	return v.FakeVMM.CreateFromSnapshot(ctx, nodeID, instance, app, snap)
}

func (v *refusingVMM) totalCalls() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	total := 0
	for _, n := range v.calls {
		total += n
	}
	return total
}

func generationRefusal() error {
	return api.NewProblem(http.StatusServiceUnavailable, api.CodeScanCritical, api.ScanCriticalTitleGenerationUnavailable,
		`runtime base "base/base-minimal-amd64.ext4" could not adopt its configured generation: refreshed scan generation does not match`)
}

// snapshotHistoryStore reports that every deployment has had snapshots.
type snapshotHistoryStore struct {
	MemStore *state.MemStore
}

func (s *snapshotHistoryStore) HasSnapshotHistory(context.Context, string) (bool, error) {
	return true, nil
}

type failoverFleet struct {
	store *state.MemStore
	ctx   context.Context
	nodes []state.ComputeNode
	vmm   *refusingVMM
	ops   *wire.OpsMetrics
	e     *sched.Engine
}

// newFailoverFleet builds nodeCount equal nodes and one schedd that owns
// node 0, so ordinary placement prefers node 0 as production's per-node
// schedds prefer their own node.
func newFailoverFleet(t *testing.T, nodeCount, ceilingMB int, refuse func(nodeID string) error) *failoverFleet {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	existing, err := store.ListComputeNodes(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range existing {
		if err := store.MarkComputeNodeInactive(ctx, n.ID); err != nil {
			t.Fatal(err)
		}
	}
	f := &failoverFleet{store: store, ctx: ctx, vmm: newRefusingVMM(refuse), ops: wire.NewOpsMetrics("schedd")}
	for i := 0; i < nodeCount; i++ {
		node, err := store.CreateComputeNode(ctx, state.ComputeNode{
			Name: fmt.Sprintf("fsn-%d", i+1), TargetURL: fmt.Sprintf("tcp://10.0.1.%d:7000", i+2),
			VPCPUs: 80, MemMB: ceilingMB * 2, MaxConcurrency: 50, AdmissionCeilingMB: ceilingMB, VCPUBudget: 80, Active: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		f.nodes = append(f.nodes, node)
	}
	f.e = sched.XNewEngine(t, store, f.vmm, &sched.FakeNotifier{}, "1.10.0").
		WithOwnerNodeID(f.nodes[0].ID).WithOpsMetrics(f.ops)
	return f
}

func (f *failoverFleet) app(t *testing.T, plan api.Plan, ramMB, maxConcurrency int, opts ...func(*state.App)) state.App {
	t.Helper()
	acct, err := f.store.CreateAccount(f.ctx, "u-"+uuid.NewString()+"@example.com", plan)
	if err != nil {
		t.Fatal(err)
	}
	app := state.App{AccountID: acct.ID, Slug: "failover-" + uuid.NewString()[:8],
		RAMMB: ramMB, MaxConcurrency: maxConcurrency, IdleTimeoutS: 60, NodeID: f.nodes[0].ID, Status: state.AppActive}
	for _, opt := range opts {
		opt(&app)
	}
	app, err = f.store.CreateApp(f.ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateDeployment(f.ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + uuid.NewString(), Status: state.DeployLive}); err != nil {
		t.Fatal(err)
	}
	return app
}

func (f *failoverFleet) failoverCount(t *testing.T, reason, outcome string) float64 {
	t.Helper()
	families, err := f.ops.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "schedd_wake_failover_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			if labelValue(metric, "reason") == reason && labelValue(metric, "outcome") == outcome {
				return metric.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("schedd_wake_failover_total{reason=%q,outcome=%q} not exported", reason, outcome)
	return 0
}

func counterValue(t *testing.T, ops *wire.OpsMetrics, name, label, value string) float64 {
	t.Helper()
	families, err := ops.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if labelValue(metric, label) == value {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func labelValue(metric *dto.Metric, name string) string {
	for _, label := range metric.GetLabel() {
		if label.GetName() == name {
			return label.GetValue()
		}
	}
	return ""
}

// liveByNode sums Σ(ram_mb + 8) of WAKING/COLD_BOOTING/RUNNING rows per node,
// and counts them per app, from the store rather than the ledger under test.
func (f *failoverFleet) liveByNode(t *testing.T, apps []state.App) (map[string]int, map[string]int) {
	t.Helper()
	ramByNode, perApp := map[string]int{}, map[string]int{}
	for _, app := range apps {
		rows, err := f.store.ListInstancesForApp(f.ctx, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			switch state.State(row.State) {
			case state.StateWaking, state.StateColdBooting, state.StateRunning:
				ramByNode[row.NodeID] += api.BillableRAMMB(row.RAMMB)
				perApp[app.ID]++
			}
		}
	}
	return ramByNode, perApp
}

func TestWakeFailover_ReplacesOnlyNodeRefusals(t *testing.T) {
	refusal := func(code, title string) error {
		return api.NewProblem(http.StatusServiceUnavailable, code, title, "detail")
	}
	for _, tc := range []struct {
		name string
		// refusal is what node 0 (the owner, which placement prefers)
		// returns; every other node serves.
		refusal     error
		wantNodeIdx int // -1: the wake fails
		wantCalls   int
		wantReason  string
	}{
		{name: "base generation the node cannot adopt", refusal: generationRefusal(), wantNodeIdx: 1, wantCalls: 2, wantReason: wire.WakeFailoverReasonBaseGeneration},
		{name: "older peer folds the detail into the title",
			refusal:     api.NewProblem(http.StatusServiceUnavailable, api.CodeScanCritical, api.ScanCriticalTitleGenerationUnavailable+`: runtime base "b" could not adopt its configured generation`, ""),
			wantNodeIdx: 1, wantCalls: 2, wantReason: wire.WakeFailoverReasonBaseGeneration},
		{name: "scan sidecar names another generation", refusal: refusal(api.CodeScanCritical, api.ScanCriticalTitleGenerationMismatch), wantNodeIdx: 1, wantCalls: 2, wantReason: wire.WakeFailoverReasonBaseGeneration},
		{name: "scan sidecar missing on the node", refusal: refusal(api.CodeScanCritical, api.ScanCriticalTitleSidecarMissing), wantNodeIdx: 1, wantCalls: 2, wantReason: wire.WakeFailoverReasonScanSidecarMissing},
		{name: "fixable critical finding refuses everywhere", refusal: refusal(api.CodeScanCritical, "Fixable CRITICAL vulnerability in base ext4"), wantNodeIdx: -1, wantCalls: 1},
		{name: "customer startup crash ran customer code", refusal: api.NewProblem(422, api.CodeAppStartupCrash, "App crashed during startup", "exit 1"), wantNodeIdx: -1, wantCalls: 1},
		{name: "customer readiness timeout", refusal: api.NewProblem(422, api.CodeAppStartupTimeout, "App did not become ready", "30 s"), wantNodeIdx: -1, wantCalls: 1},
		{name: "internal error may have started a VM", refusal: api.NewProblem(500, "internal", "vmmd operation failed", "jailer: exit 1"), wantNodeIdx: -1, wantCalls: 1},
		{name: "deadline expiry may have started a VM", refusal: context.DeadlineExceeded, wantNodeIdx: -1, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var f *failoverFleet
			f = newFailoverFleet(t, 3, 4096, func(nodeID string) error {
				if nodeID == f.nodes[0].ID {
					return tc.refusal
				}
				return nil
			})
			app := f.app(t, api.PlanPro, 256, 5)
			res, err := f.e.Wake(f.ctx, app.ID, "", "", sched.TriggerGateway)
			if got := f.vmm.totalCalls(); got != tc.wantCalls {
				t.Errorf("vmmd wake calls = %d, want %d (%v)", got, tc.wantCalls, f.vmm.calls)
			}
			if tc.wantNodeIdx < 0 {
				if err == nil || !errors.Is(err, tc.refusal) && err.Error() != tc.refusal.Error() {
					t.Fatalf("Wake = %+v, %v; want the refusal %v", res, err, tc.refusal)
				}
				if got := f.e.Ledger().Concurrency(app.ID); got != 0 {
					t.Fatalf("failed wake kept %d reservations", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Wake: %v", err)
			}
			if res.NodeID != f.nodes[tc.wantNodeIdx].ID {
				t.Fatalf("wake served by %s, want %s", res.NodeID, f.nodes[tc.wantNodeIdx].ID)
			}
			rows, err := f.store.ListInstancesForApp(f.ctx, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			states := map[string]string{}
			for _, row := range rows {
				states[row.NodeID] = row.State
			}
			if states[f.nodes[0].ID] != string(state.StateFailed) || states[res.NodeID] != string(state.StateRunning) || len(rows) != 2 {
				t.Fatalf("instance rows = %+v, want FAILED on the refusing node and RUNNING on the alternate", rows)
			}
			if got := f.e.Ledger().Concurrency(app.ID); got != 1 {
				t.Fatalf("ledger holds %d reservations, want 1", got)
			}
			if got := f.failoverCount(t, tc.wantReason, wire.WakeFailoverOutcomeSucceeded); got != 1 {
				t.Fatalf("succeeded failovers = %v, want 1", got)
			}
		})
	}
}

func TestWakeFailover_Bounds(t *testing.T) {
	everyNode := func(error) func(string) error {
		return func(string) error { return generationRefusal() }
	}
	t.Run("every node refuses: two alternates, then the refusal", func(t *testing.T) {
		f := newFailoverFleet(t, 4, 4096, everyNode(nil))
		app := f.app(t, api.PlanPro, 256, 5)
		_, err := f.e.Wake(f.ctx, app.ID, "", "", sched.TriggerGateway)
		var problem *api.Problem
		if !errors.As(err, &problem) || problem.Code != api.CodeScanCritical {
			t.Fatalf("Wake error = %v, want the scan_critical refusal", err)
		}
		if got := f.vmm.totalCalls(); got != 1+api.WakeFailoverMaxAlternates {
			t.Fatalf("vmmd wake calls = %d, want %d", got, 1+api.WakeFailoverMaxAlternates)
		}
		if got := len(f.vmm.calls); got != 1+api.WakeFailoverMaxAlternates {
			t.Fatalf("attempts reached %d distinct nodes, want %d: a refusing node was tried twice", got, 1+api.WakeFailoverMaxAlternates)
		}
		if got := f.failoverCount(t, wire.WakeFailoverReasonBaseGeneration, wire.WakeFailoverOutcomeRefusedAgain); got != api.WakeFailoverMaxAlternates {
			t.Fatalf("refused_again = %v, want %d", got, api.WakeFailoverMaxAlternates)
		}
		if got := f.failoverCount(t, wire.WakeFailoverReasonBaseGeneration, wire.WakeFailoverOutcomeNoAlternate); got != 1 {
			t.Fatalf("no_alternate = %v, want 1", got)
		}
		if got := f.e.Ledger().Concurrency(app.ID); got != 0 {
			t.Fatalf("refused wake kept %d reservations", got)
		}
	})
	t.Run("no other node: the refusal, not a capacity error", func(t *testing.T) {
		f := newFailoverFleet(t, 1, 4096, everyNode(nil))
		app := f.app(t, api.PlanPro, 256, 5)
		_, err := f.e.Wake(f.ctx, app.ID, "", "", sched.TriggerGateway)
		var problem *api.Problem
		if !errors.As(err, &problem) || problem.Code != api.CodeScanCritical {
			t.Fatalf("Wake error = %v, want the scan_critical refusal", err)
		}
		if got := f.failoverCount(t, wire.WakeFailoverReasonBaseGeneration, wire.WakeFailoverOutcomeNoAlternate); got != 1 {
			t.Fatalf("no_alternate = %v, want 1", got)
		}
	})
	t.Run("too little deadline left", func(t *testing.T) {
		var f *failoverFleet
		f = newFailoverFleet(t, 3, 4096, func(nodeID string) error {
			if nodeID == f.nodes[0].ID {
				return generationRefusal()
			}
			return nil
		})
		app := f.app(t, api.PlanPro, 256, 5)
		ctx, cancel := context.WithTimeout(f.ctx, api.WakeFailoverMinRemainingMs*time.Millisecond/2)
		defer cancel()
		if _, err := f.e.Wake(ctx, app.ID, "", "", sched.TriggerGateway); err == nil {
			t.Fatal("Wake succeeded with no time for an alternate")
		}
		if got := f.vmm.totalCalls(); got != 1 {
			t.Fatalf("vmmd wake calls = %d, want 1", got)
		}
		if got := f.failoverCount(t, wire.WakeFailoverReasonBaseGeneration, wire.WakeFailoverOutcomeOutOfTime); got != 1 {
			t.Fatalf("out_of_time = %v, want 1", got)
		}
	})
}

// The alternate attempt is the same wake: the first attempt's wake-rate
// token and scale-out cooldown stamp must not refuse it.
func TestWakeFailover_AlternateIsTheSameWake(t *testing.T) {
	// refuseOnce makes the next vmmd call refuse, wherever it is placed.
	var refuseOnce sync.Once
	armed := false
	newFleet := func(t *testing.T) *failoverFleet {
		refuseOnce, armed = sync.Once{}, false
		return newFailoverFleet(t, 2, 4096, func(string) error {
			var err error
			if armed {
				refuseOnce.Do(func() { err = generationRefusal() })
			}
			return err
		})
	}
	t.Run("wake-rate limit", func(t *testing.T) {
		f := newFleet(t)
		// Free allows one wake per app per minute.
		f.e.WithWakeRateLimiter(sched.NewWakeRateLimiter())
		app := f.app(t, api.PlanFree, 256, 1)
		armed = true
		res, err := f.e.Wake(f.ctx, app.ID, "", "", sched.TriggerGateway)
		if err != nil || res.AtCapacity || res.InstanceID == "" {
			t.Fatalf("Wake = %+v, %v; want the alternate node to serve the plan's one wake", res, err)
		}
		if got := f.vmm.totalCalls(); got != 2 {
			t.Fatalf("vmmd wake calls = %d, want 2", got)
		}
	})
	t.Run("snapshot-miss backoff", func(t *testing.T) {
		f := newFleet(t)
		// The deployment has had snapshots but has none usable now, so the
		// first attempt records a miss and arms the backoff gate.
		f.e = sched.XNewEngine(t, statetest.NewMem(&snapshotHistoryStore{MemStore: f.store}), f.vmm, &sched.FakeNotifier{}, "1.10.0").
			WithOwnerNodeID(f.nodes[0].ID).WithOpsMetrics(f.ops)
		app := f.app(t, api.PlanPro, 256, 5)
		armed = true
		res, err := f.e.Wake(f.ctx, app.ID, "", "", sched.TriggerGateway)
		if err != nil || res.InstanceID == "" {
			t.Fatalf("Wake = %+v, %v; want the alternate to cold boot past the miss its own first attempt recorded", res, err)
		}
		// The successful wake clears the backoff asynchronously, so count
		// the recordings rather than reading the deployment's counter.
		if got := counterValue(t, f.ops, "schedd_snapshot_backoff_stamp_total", "outcome", "recorded"); got != 1 {
			t.Fatalf("snapshot misses recorded = %v, want 1 for one wake", got)
		}
	})
	t.Run("scale-out cooldown", func(t *testing.T) {
		f := newFleet(t)
		app := f.app(t, api.PlanPro, 256, 5, func(app *state.App) {
			app.ScalingPolicy = &state.ScalingPolicy{ScaleOutCooldownS: 300}
		})
		dep, err := f.store.LiveDeployment(f.ctx, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		// A floor admission runs no scale-out gate and stamps nothing, so
		// the next admission is a scale-out whose first attempt passes the
		// cooldown and stamps it.
		if _, err := f.e.AdmitInstanceForDeployment(f.ctx, app.ID, dep.ID, "", sched.TriggerFloorDep); err != nil {
			t.Fatalf("floor instance: %v", err)
		}
		armed = true
		res, err := f.e.AdmitInstance(f.ctx, app.ID, "", "", sched.TriggerGateway)
		if err != nil || res.AtCapacity || res.InstanceID == "" {
			t.Fatalf("scale-out = %+v, %v; want the alternate admitted", res, err)
		}
		if got := f.vmm.totalCalls(); got != 3 {
			t.Fatalf("vmmd wake calls = %d, want 3", got)
		}
	})
}

// Property: under random refusals, concurrent wakes and either admission
// mode, failover never lets an app exceed its concurrency or a node exceed
// its RAM ceiling, and every refused attempt gives its reservation back.
func TestWakeFailover_PropertyKeepsInvariants(t *testing.T) {
	for _, measured := range []bool{false, true} {
		rePlaced, refusedFull := 0.0, int64(0)
		for seed := int64(1); seed <= 12; seed++ {
			t.Run(fmt.Sprintf("measured=%t/seed=%d", measured, seed), func(t *testing.T) {
				rng := rand.New(rand.NewSource(seed))
				var rngMu sync.Mutex
				nodeCount := 2 + rng.Intn(3)
				ceilingMB := 768 + 256*rng.Intn(4)
				refuseRate := []float64{0.2, 0.5, 0.9}[rng.Intn(3)]
				var f *failoverFleet
				f = newFailoverFleet(t, nodeCount, ceilingMB, func(nodeID string) error {
					rngMu.Lock()
					defer rngMu.Unlock()
					// The owner node, which placement prefers, refuses most.
					rate := refuseRate / 3
					if nodeID == f.nodes[0].ID {
						rate = refuseRate
					}
					if rng.Float64() < rate {
						return generationRefusal()
					}
					return nil
				})
				if measured {
					// Every node measures a small, fresh charge, so admission
					// overcommits up to ADR-909's reservation bound. (The read
					// runs under the ledger's lock and must not call back in.)
					f.e.Ledger().EnableMeasuredAdmission(func(string) (int, time.Time, bool) {
						return 64, time.Now(), true
					})
				}
				var apps []state.App
				// Enough demand to fill every node, so attempts meet full nodes.
				for i := 0; i < 5; i++ {
					apps = append(apps, f.app(t, api.PlanPro, []int{128, 256, 512}[rng.Intn(3)], 1+rng.Intn(5)))
				}
				reservationCap := ceilingMB
				if measured {
					reservationCap = ceilingMB * api.MeasuredAdmissionMaxOvercommitPercent / 100
				}
				check := func(step string) {
					t.Helper()
					ramByNode, perApp := f.liveByNode(t, apps)
					for _, app := range apps {
						if perApp[app.ID] > app.MaxConcurrency {
							t.Fatalf("%s: app has %d live instances, max_concurrency %d (invariant 1)", step, perApp[app.ID], app.MaxConcurrency)
						}
						if got := f.e.Ledger().Concurrency(app.ID); got != perApp[app.ID] {
							t.Fatalf("%s: ledger counts %d instances, store has %d live: a refused attempt kept its reservation", step, got, perApp[app.ID])
						}
					}
					for _, node := range f.nodes {
						if ramByNode[node.ID] > reservationCap || f.e.Ledger().ResidentRAMForNode(node.ID) > reservationCap {
							t.Fatalf("%s: node %s holds %d MB live, %d MB booked, cap %d MB (invariant 2)", step, node.Name,
								ramByNode[node.ID], f.e.Ledger().ResidentRAMForNode(node.ID), reservationCap)
						}
					}
				}
				var full atomic.Int64
				for round := 0; round < 6; round++ {
					var wg sync.WaitGroup
					for i := 0; i < 12; i++ {
						rngMu.Lock()
						app, wake := apps[rng.Intn(len(apps))], rng.Intn(2) == 0
						rngMu.Unlock()
						wg.Add(1)
						go func() {
							defer wg.Done()
							// Refusals, capacity and concurrency errors are all
							// legitimate outcomes; the invariants are checked after.
							var err error
							if wake {
								_, err = f.e.Wake(f.ctx, app.ID, "", "", sched.TriggerGateway)
							} else {
								_, err = f.e.AdmitInstance(f.ctx, app.ID, "", "", sched.TriggerGateway)
							}
							var problem *api.Problem
							if errors.As(err, &problem) && problem.Code == api.CodeCapacity {
								full.Add(1)
							}
						}()
					}
					wg.Wait()
					check(fmt.Sprintf("round %d", round))
				}
				alternates := 0.0
				for _, outcome := range []string{wire.WakeFailoverOutcomeSucceeded, wire.WakeFailoverOutcomeRefusedAgain, wire.WakeFailoverOutcomeFailed} {
					alternates += f.failoverCount(t, wire.WakeFailoverReasonBaseGeneration, outcome)
				}
				t.Logf("vmmd calls %d, alternate attempts %v, capacity refusals %d", f.vmm.totalCalls(), alternates, full.Load())
				rePlaced += alternates
				refusedFull += full.Load()
			})
		}
		// Small, full fleets legitimately re-place nothing; across the seeds
		// the run must have re-placed wakes into fleets that were filling.
		if rePlaced == 0 || refusedFull == 0 {
			t.Fatalf("measured=%t: %v re-placements, %d capacity refusals; the property held vacuously", measured, rePlaced, refusedFull)
		}
	}
}

func TestChoosePlacement_SkipsExcludedNodes(t *testing.T) {
	node := func(id string) state.ComputeNode {
		return state.ComputeNode{ID: id, Name: id, Active: true, Lifecycle: state.NodeLifecycleActive, LifecycleGeneration: 1,
			AdmissionCeilingMB: 4096, VCPUBudget: 80, VPCPUs: 8}
	}
	nodes := []state.ComputeNode{node("fsn-2"), node("fsn-3"), node("fsn-4")}
	// fsn-3 is idle and every hint points at it; fsn-4 is busier than fsn-2.
	used := map[string]int64{"fsn-2": 1024, "fsn-3": 0, "fsn-4": 2048}
	base := sched.Request{AppID: "a", Plan: api.PlanPro, RAMMB: 256, VCPU: 1}
	for _, tc := range []struct {
		name    string
		mutate  func(*sched.Request)
		want    string
		wantErr bool
	}{
		{name: "nothing excluded takes the idle node", want: "fsn-3"},
		{name: "excluded idle node", mutate: func(r *sched.Request) { r.ExcludeNodeIDs = []string{"fsn-3"} }, want: "fsn-2"},
		{name: "excluded warm hint", mutate: func(r *sched.Request) {
			r.PreferredNodeID, r.ExcludeNodeIDs = "fsn-3", []string{"fsn-3"}
		}, want: "fsn-2"},
		{name: "excluded snapshot replica", mutate: func(r *sched.Request) {
			r.PreferredNodeIDs, r.PrioritizeSnapshotLocality, r.ExcludeNodeIDs = []string{"fsn-3"}, true, []string{"fsn-3"}
		}, want: "fsn-2"},
		{name: "every node excluded", mutate: func(r *sched.Request) { r.ExcludeNodeIDs = []string{"fsn-2", "fsn-3", "fsn-4"} }, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			if tc.mutate != nil {
				tc.mutate(&r)
			}
			p, err := sched.ChoosePlacement(nodes, used, nil, r)
			if tc.wantErr {
				var problem *api.Problem
				if !errors.As(err, &problem) || problem.Code != api.CodeCapacity {
					t.Fatalf("ChoosePlacement = %+v, %v; want a capacity refusal", p, err)
				}
				return
			}
			if err != nil || p.NodeID != tc.want {
				t.Fatalf("ChoosePlacement = %q, %v; want %q", p.NodeID, err, tc.want)
			}
		})
	}
}
