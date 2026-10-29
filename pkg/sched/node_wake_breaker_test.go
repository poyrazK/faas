// spec: §6.2
// adr: 20261022184531
package sched_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func breakerOf(t *testing.T, f *failoverFleet, nodeID string) state.ComputeNode {
	t.Helper()
	n, err := f.store.ComputeNodeByID(f.ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// production-us 2026-10-22: fsn-3 refused every wake. Step 1 re-placed each
// one at the cost of a vmmd round trip; after NodeWakeBreakerFailures of
// them the breaker opens and wakes stop asking fsn-3.
func TestNodeWakeBreaker_OpensAfterConsecutiveRefusals(t *testing.T) {
	refusing := true
	var f *failoverFleet
	f = newFailoverFleet(t, 2, 4096, func(nodeID string) error {
		if refusing && nodeID == f.nodes[0].ID {
			return generationRefusal()
		}
		return nil
	})
	// Scale allows 20 concurrent instances; every wake below keeps its VM.
	app := f.app(t, api.PlanScale, 256, 20)
	for i := 0; i < api.NodeWakeBreakerFailures; i++ {
		if res, err := f.e.AdmitInstance(f.ctx, app.ID, "", "", sched.TriggerGateway); err != nil || res.NodeID != f.nodes[1].ID {
			t.Fatalf("wake %d = %+v, %v; want re-placed on node 1", i, res, err)
		}
		if open := breakerOf(t, f, f.nodes[0].ID).WakeBreakerOpen(time.Now()); open != (i == api.NodeWakeBreakerFailures-1) {
			t.Fatalf("after %d refusals breaker open = %t", i+1, open)
		}
	}
	if got := counterValue(t, f.ops, "schedd_node_wake_breaker_total", "transition", "opened"); got != 1 {
		t.Fatalf("opened = %v, want 1", got)
	}
	calls := f.vmm.calls[f.nodes[0].ID]
	if _, err := f.e.AdmitInstance(f.ctx, app.ID, "", "", sched.TriggerGateway); err != nil {
		t.Fatal(err)
	}
	if f.vmm.calls[f.nodes[0].ID] != calls {
		t.Fatal("a wake asked the node whose breaker is open")
	}

	// The cooldown passes: half-open. The node still refuses, so the first
	// wake there reopens the breaker at once.
	if err := f.store.OpenComputeNodeWakeBreaker(f.ctx, f.nodes[0].ID, time.Now().Add(-time.Second), "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.AdmitInstance(f.ctx, app.ID, "", "", sched.TriggerGateway); err != nil {
		t.Fatal(err)
	}
	if !breakerOf(t, f, f.nodes[0].ID).WakeBreakerOpen(time.Now()) {
		t.Fatal("a refusal while half-open did not reopen the breaker")
	}
	if got := counterValue(t, f.ops, "schedd_node_wake_breaker_total", "transition", "reopened"); got != 1 {
		t.Fatalf("reopened = %v, want 1", got)
	}

	// The node is fixed: the first wake after the cooldown proves it and
	// closes the breaker.
	refusing = false
	if err := f.store.OpenComputeNodeWakeBreaker(f.ctx, f.nodes[0].ID, time.Now().Add(-time.Second), "test"); err != nil {
		t.Fatal(err)
	}
	if res, err := f.e.AdmitInstance(f.ctx, app.ID, "", "", sched.TriggerGateway); err != nil || res.NodeID != f.nodes[0].ID {
		t.Fatalf("probe wake = %+v, %v; want served by the recovered node", res, err)
	}
	if n := breakerOf(t, f, f.nodes[0].ID); n.WakeBreakerOpenUntil != nil {
		t.Fatalf("breaker after a successful probe = %v, want closed", n.WakeBreakerOpenUntil)
	}
	if got := counterValue(t, f.ops, "schedd_node_wake_breaker_total", "transition", "closed"); got != 1 {
		t.Fatalf("closed = %v, want 1", got)
	}
}

func TestNodeWakeBreaker_CountsOnlyPlatformFailures(t *testing.T) {
	setupFailure := sched.NewVMMWakeNotStarted(api.NewProblem(http.StatusInternalServerError, "internal", "vmmd operation failed",
		"wake i: network setup: netns add: file exists"))
	for _, tc := range []struct {
		name     string
		failures []error // node 0's answers, in order; nil serves
		wantOpen bool
	}{
		{name: "three base generation refusals", failures: []error{generationRefusal(), generationRefusal(), generationRefusal()}, wantOpen: true},
		{name: "three network setup failures before the VM started", failures: []error{setupFailure, setupFailure, setupFailure}, wantOpen: true},
		{name: "customer startup crashes never count",
			failures: []error{appCrash(), appCrash(), appCrash(), appCrash()}},
		{name: "an internal error may have started a VM",
			failures: []error{internalError(), internalError(), internalError(), internalError()}},
		// Re-placed, but this schedd's own view of the node: its
		// process-local boot breaker keeps it (ADR-20261029085904).
		{name: "a request that never reached vmmd stays this schedd's view",
			failures: []error{vmmdUnreachable(), vmmdUnreachable(), vmmdUnreachable(), vmmdUnreachable()}},
		{name: "a success on the node resets the count",
			failures: []error{generationRefusal(), generationRefusal(), nil, generationRefusal(), generationRefusal()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := 0
			var f *failoverFleet
			f = newFailoverFleet(t, 2, 4096, func(nodeID string) error {
				if nodeID != f.nodes[0].ID || next >= len(tc.failures) {
					return nil
				}
				next++
				return tc.failures[next-1]
			})
			app := f.app(t, api.PlanScale, 256, 20)
			for range tc.failures {
				_, _ = f.e.AdmitInstance(f.ctx, app.ID, "", "", sched.TriggerGateway)
			}
			if next != len(tc.failures) {
				t.Fatalf("node 0 answered %d of %d wakes; the test did not drive it", next, len(tc.failures))
			}
			if open := breakerOf(t, f, f.nodes[0].ID).WakeBreakerOpen(time.Now()); open != tc.wantOpen {
				t.Fatalf("breaker open = %t, want %t", open, tc.wantOpen)
			}
		})
	}
}

func appCrash() error {
	return api.NewProblem(422, api.CodeAppStartupCrash, "App crashed during startup", "exit 1")
}

func vmmdUnreachable() error {
	return status.Error(codes.Unavailable, `connection error: desc = "transport: Error while dialing: dial unix /run/faas/vmmd.sock: connect: connection refused"`)
}

func internalError() error {
	return api.NewProblem(500, "internal", "vmmd operation failed", "guest kernel panic")
}

// Another schedd's breaker holds the node degraded for every schedd.
func TestNodeWakeBreaker_HonouredByEverySchedd(t *testing.T) {
	f := newFailoverFleet(t, 2, 4096, nil)
	if err := f.store.OpenComputeNodeWakeBreaker(f.ctx, f.nodes[0].ID, time.Now().Add(time.Minute), "opened by a peer schedd"); err != nil {
		t.Fatal(err)
	}
	app := f.app(t, api.PlanPro, 256, 5)
	res, err := f.e.Wake(f.ctx, app.ID, "", "", sched.TriggerGateway)
	if err != nil || res.NodeID != f.nodes[1].ID || f.vmm.calls[f.nodes[0].ID] != 0 {
		t.Fatalf("Wake = %+v, %v (calls %v); want node 1 without asking node 0", res, err, f.vmm.calls)
	}
}

// vmmd marks a failure before the VM started beside the Problem; schedd lifts
// it into ErrVMMWakeNotStarted and keeps the Problem for its callers.
func TestLiftErr_WakeStagePreStart(t *testing.T) {
	problem := api.NewProblem(http.StatusInternalServerError, "internal", "vmmd operation failed", "wake i: acquire lease: no free jail uid")
	for _, tc := range []struct {
		name    string
		err     error
		wantPre bool
	}{
		{"marked", grpcerr.ToStatusWithMetadata(problem, map[string]string{grpcerr.MetadataWakeStage: grpcerr.WakeStagePreStart}), true},
		{"unmarked (an older vmmd)", grpcerr.ToStatus(problem), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lifted := sched.LiftErr(tc.err)
			if errors.Is(lifted, sched.ErrVMMWakeNotStarted) != tc.wantPre {
				t.Fatalf("pre-start = %t, want %t", errors.Is(lifted, sched.ErrVMMWakeNotStarted), tc.wantPre)
			}
			got := api.AsProblem(lifted)
			if got == nil || got.Code != "internal" || got.Detail != problem.Detail || lifted.Error() != got.Error() {
				t.Fatalf("lifted %v (%v); want the Problem unchanged", lifted, got)
			}
		})
	}
}

func TestHeartbeat_GaugeShowsOpenWakeBreaker(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "fsn-3", TargetURL: "tcp://10.0.1.3:7000",
		VPCPUs: 8, MemMB: 16384, MaxConcurrency: 50, AdmissionCeilingMB: 8593, VCPUBudget: 80, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.OpenComputeNodeWakeBreaker(ctx, node.ID, time.Now().Add(time.Minute), "3 consecutive node_setup failures"); err != nil {
		t.Fatal(err)
	}
	ops := wire.NewOpsMetrics("schedd")
	dialer := &sched.HeartbeatFakeDialer{}
	dialer.SetPingResult(node.TargetURL, nil, nil)
	if err := sched.NewHeartbeat(store, dialer, nil, nil).WithOpsMetrics(ops).WithOwnerNodeID(node.ID).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := counterOrGauge(t, ops, "schedd_compute_node_readiness", map[string]string{"node": "fsn-3", "state": "degraded"}); got != 1 {
		t.Fatalf("degraded gauge = %v, want 1 while the breaker is open", got)
	}
	if n, _ := store.ComputeNodeByID(ctx, node.ID); n.Readiness != state.NodeReadinessReady {
		t.Fatalf("stored readiness = %q; the breaker must not be written into it", n.Readiness)
	}
}
