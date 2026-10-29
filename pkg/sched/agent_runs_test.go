// adr: 20261027090010, 20261027090014
package sched_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// agentRouter records the run and checks the reservation while it runs.
type agentRouter struct {
	ledger   *sched.NodeLedger
	nodeID   string
	spec     sched.AgentRunSpec
	resident bool
	err      error
	// method is how vmmd reports the boot; empty: a cold boot.
	method string

	mu              sync.Mutex
	captures        int
	captureErr      error
	captureResident bool
	captured        chan struct{}
}

func (r *agentRouter) RunAgent(ctx context.Context, nodeID string, spec sched.AgentRunSpec, _ agentrunproto.Request, hooks sched.AgentRunHooks, _ <-chan struct{}) (agentrunproto.Result, error) {
	r.nodeID, r.spec = nodeID, spec
	r.resident = r.ledger.ResidentFor(spec.Instance)
	if r.err != nil {
		return agentrunproto.Result{}, r.err
	}
	method := r.method
	if method == "" {
		method = "WAKE_COLD_BOOT"
	}
	if hooks.Booted != nil {
		_ = hooks.Booted(ctx, method, 10)
	}
	return agentrunproto.Result{Status: agentrunproto.StatusSucceeded}, nil
}

func (r *agentRouter) CaptureAgentSnapshot(_ context.Context, _ string, spec sched.AgentRunSpec, namespace, captureID string) (*sched.AgentRunSnapshot, error) {
	r.mu.Lock()
	defer func() {
		r.mu.Unlock()
		if r.captured != nil {
			r.captured <- struct{}{}
		}
	}()
	r.captures++
	r.captureResident = r.ledger.ResidentFor(spec.Instance)
	if r.captureErr != nil {
		return nil, r.captureErr
	}
	return &sched.AgentRunSnapshot{StorageKey: "snap/" + namespace + "/captures/" + captureID + "/v2/mem",
		VMStateStorageKey: "snap/" + namespace + "/captures/" + captureID + "/v2/vmstate", FCVersion: "1.7.0", MemBytes: 1 << 30}, nil
}

func agentRunnerFixture(t *testing.T) (*sched.AgentRunner, *agentRouter, *sched.NodeLedger, string) {
	t.Helper()
	return agentRunnerFixtureWith(t, false)
}

func agentRunnerFixtureWith(t *testing.T, snapshots bool) (*sched.AgentRunner, *agentRouter, *sched.NodeLedger, string) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	node := func(name string) string {
		n, err := store.UpsertComputeNode(ctx, state.ComputeNode{Name: name, TargetURL: "tcp://" + name + ":50051",
			VPCPUs: 20, MemMB: 64000, MaxConcurrency: 200, AdmissionCeilingMB: 47600, VCPUBudget: 40, Active: true})
		if err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	node("compute-1")
	agentNode := node("agents-1")
	ledger := sched.NewNodeLedger()
	engine, err := sched.NewEngine(ctx, store, ledger, nil, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	router := &agentRouter{ledger: ledger}
	runner, err := sched.NewAgentRunner(engine, router, sched.AgentRunConfig{BaseKey: "base/agent-base.ext4", LayerKey: "layer/agent.ext4",
		Nodes: []string{"agents-1"}, Snapshots: snapshots})
	if err != nil {
		t.Fatal(err)
	}
	return runner, router, ledger, agentNode
}

func agentAdmission() sched.AgentRunAdmission {
	return sched.AgentRunAdmission{RunID: "11111111-2222-3333-4444-555555555555", AccountID: "acct", Plan: api.PlanPro,
		RAMMB: 2048, VCPU: 2, CPUMillicores: 2000}
}

func agentRun() agentrunproto.Request {
	return agentrunproto.Request{Version: agentrunproto.Version, RunID: "run", Command: []string{"codex"}, TimeoutSec: 30,
		MaxLogBytes: 1024, MaxResultBytes: 1024, MaxOutputFiles: 1, MaxOutputBytes: 1024}
}

func TestAgentRunnerPlacesOnThePoolAndReleases(t *testing.T) {
	runner, router, ledger, agentNode := agentRunnerFixture(t)
	var placed sched.AgentRunPlacement
	result, err := runner.Run(context.Background(), agentAdmission(), agentRun(), func(p sched.AgentRunPlacement) error {
		placed = p
		return nil
	}, sched.AgentRunHooks{}, nil)
	if err != nil || result.Status != agentrunproto.StatusSucceeded {
		t.Fatalf("run %+v %v", result, err)
	}
	if router.nodeID != agentNode || placed.NodeID != agentNode || placed.Instance != "agent-11111111-2222-3333-4444-555555555555" {
		t.Fatalf("placed on %q (%+v); the pool is %q", router.nodeID, placed, agentNode)
	}
	if !router.resident || router.spec.BaseKey != "base/agent-base.ext4" || router.spec.MemSizeMiB != 2048 {
		t.Fatalf("reservation held %v, spec %+v", router.resident, router.spec)
	}
	if ledger.ResidentFor(placed.Instance) || ledger.ResidentRAMForNode(agentNode) != 0 {
		t.Fatal("the reservation outlived the run")
	}
}

func TestAgentRunnerReleasesOnFailure(t *testing.T) {
	runner, router, ledger, _ := agentRunnerFixture(t)
	router.err = errors.New("vmmd unavailable")
	if _, err := runner.Run(context.Background(), agentAdmission(), agentRun(), nil, sched.AgentRunHooks{}, nil); err == nil {
		t.Fatal("router failure not reported")
	}
	if ledger.ResidentFor(sched.AgentRunInstanceID(agentAdmission().RunID)) {
		t.Fatal("reservation kept after a failed run")
	}
	// A placement the caller refuses (agentd lost its lease) boots nothing.
	router.err, router.nodeID = nil, ""
	_, err := runner.Run(context.Background(), agentAdmission(), agentRun(), func(sched.AgentRunPlacement) error {
		return errors.New("lease lost")
	}, sched.AgentRunHooks{}, nil)
	if err == nil || router.nodeID != "" {
		t.Fatalf("a refused placement still ran on %q: %v", router.nodeID, err)
	}
	if ledger.ResidentFor(sched.AgentRunInstanceID(agentAdmission().RunID)) {
		t.Fatal("reservation kept after a refused placement")
	}
}

func TestAgentRunnerRefusesOutsideThePlan(t *testing.T) {
	runner, _, _, _ := agentRunnerFixture(t)
	for name, mutate := range map[string]func(*sched.AgentRunAdmission){
		"free plan":     func(a *sched.AgentRunAdmission) { a.Plan = api.PlanFree },
		"too much ram":  func(a *sched.AgentRunAdmission) { a.RAMMB = 8192 },
		"odd ram":       func(a *sched.AgentRunAdmission) { a.RAMMB = 700 },
		"too many cpus": func(a *sched.AgentRunAdmission) { a.VCPU = 4 },
		"no account":    func(a *sched.AgentRunAdmission) { a.AccountID = "" },
	} {
		a := agentAdmission()
		mutate(&a)
		if _, err := runner.Run(context.Background(), a, agentRun(), nil, sched.AgentRunHooks{}, nil); err == nil {
			t.Errorf("%s: admitted", name)
		}
	}
	if _, err := sched.NewAgentRunner(nil, nil, sched.AgentRunConfig{}); !errors.Is(err, sched.ErrAgentRunsDisabled) {
		t.Fatalf("empty config: %v", err)
	}
}

func TestAgentRunnerNeedsAPoolNode(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	if _, err := store.UpsertComputeNode(ctx, state.ComputeNode{Name: "compute-1", TargetURL: "tcp://c:1", VPCPUs: 20, MemMB: 64000,
		MaxConcurrency: 200, AdmissionCeilingMB: 47600, VCPUBudget: 40, Active: true}); err != nil {
		t.Fatal(err)
	}
	ledger := sched.NewNodeLedger()
	engine, err := sched.NewEngine(ctx, store, ledger, nil, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	router := &agentRouter{ledger: ledger}
	runner, err := sched.NewAgentRunner(engine, router, sched.AgentRunConfig{BaseKey: "b", LayerKey: "l", Nodes: []string{"agents-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, agentAdmission(), agentRun(), nil, sched.AgentRunHooks{}, nil); err == nil || router.nodeID != "" {
		t.Fatalf("placed outside the pool: node %q, %v", router.nodeID, err)
	}
}

// The first cold run of a shape captures its snapshot under the run's own
// reservation; later runs restore from it; a run vmmd cold-boots instead
// drops it, and the next cold run captures again.
func TestAgentRunnerCapturesAndRestoresSnapshots(t *testing.T) {
	runner, router, ledger, _ := agentRunnerFixtureWith(t, true)
	router.captured = make(chan struct{}, 4)
	ctx := context.Background()
	run := func() {
		t.Helper()
		if _, err := runner.Run(ctx, agentAdmission(), agentRun(), nil, sched.AgentRunHooks{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	waitCapture := func() {
		t.Helper()
		select {
		case <-router.captured:
		case <-time.After(5 * time.Second):
			t.Fatal("no capture")
		}
		for deadline := time.Now().Add(5 * time.Second); ledger.ResidentFor(router.spec.Instance); {
			if time.Now().After(deadline) {
				t.Fatal("the capture kept its reservation")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	run()
	if router.spec.Snapshot != nil {
		t.Fatal("the first run restored a snapshot")
	}
	waitCapture()
	if !router.captureResident || router.captures != 1 {
		t.Fatalf("capture ran outside the run's reservation: resident %v captures %d", router.captureResident, router.captures)
	}
	router.method = "WAKE_RESTORE"
	run()
	first := router.spec.Snapshot
	if first == nil || first.FCVersion != "1.7.0" {
		t.Fatalf("the second run did not restore: %+v", router.spec.Snapshot)
	}
	router.method = "WAKE_COLD_BOOT"
	run() // vmmd fell back: the snapshot is dropped and captured again
	waitCapture()
	if router.captures != 2 {
		t.Fatalf("captures %d after a fallback", router.captures)
	}
	router.method = "WAKE_RESTORE"
	run()
	if router.spec.Snapshot == nil || router.spec.Snapshot.StorageKey != first.StorageKey {
		t.Fatalf("a recapture moved the keys: %+v vs %+v", router.spec.Snapshot, first)
	}
}

func TestAgentRunnerSnapshotsOffOrFailing(t *testing.T) {
	runner, router, _, _ := agentRunnerFixtureWith(t, false)
	if _, err := runner.Run(context.Background(), agentAdmission(), agentRun(), nil, sched.AgentRunHooks{}, nil); err != nil {
		t.Fatal(err)
	}
	if router.captures != 0 {
		t.Fatal("captured with snapshots off")
	}
	runner, router, ledger, _ := agentRunnerFixtureWith(t, true)
	router.captured, router.captureErr = make(chan struct{}, 4), errors.New("capture failed")
	for i := 0; i < 2; i++ {
		if _, err := runner.Run(context.Background(), agentAdmission(), agentRun(), nil, sched.AgentRunHooks{}, nil); err != nil {
			t.Fatal(err)
		}
		<-router.captured
		for ledger.ResidentFor(router.spec.Instance) {
			time.Sleep(10 * time.Millisecond)
		}
		if router.spec.Snapshot != nil {
			t.Fatal("a failed capture was used")
		}
	}
}
