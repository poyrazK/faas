// spec: §6.2
// adr: 20261022184531
package sched_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

var (
	adoptableBase = sched.BaseGenerationReadiness{BaseKey: "base/base-minimal-amd64.ext4", Generation: "ghcr.io/p/base-minimal@sha256:db529ed", Adoptable: true}
	// production-us 2026-10-22: fsn-3 rolled back to rc.288 expected db529ed
	// after fsn-2's rc.290 imaged had published 5ca69ea.
	refusedBase = sched.BaseGenerationReadiness{BaseKey: "base/base-minimal-amd64.ext4", Generation: "ghcr.io/p/base-minimal@sha256:db529ed",
		Reason: "shared storage holds generation ghcr.io/p/base-minimal@sha256:5ca69ea and this node's cache does not hold the configured one"}
)

func TestEvaluateNodeReadiness(t *testing.T) {
	node := func(lifecycle state.NodeLifecycle) state.ComputeNode {
		return state.ComputeNode{ID: "n", Name: "fsn-3", Lifecycle: lifecycle, Active: lifecycle.IsAdmitting()}
	}
	ping := func(bases ...sched.BaseGenerationReadiness) *sched.PingOutcome {
		return &sched.PingOutcome{FcVersion: "1.17.0", BaseGenerations: bases}
	}
	for _, tc := range []struct {
		name       string
		node       state.ComputeNode
		ping       *sched.PingOutcome
		want       state.NodeReadiness
		wantReason string
	}{
		{"answers and adopts", node(state.NodeLifecycleActive), ping(adoptableBase), state.NodeReadinessReady, ""},
		{"vmmd too old to report", node(state.NodeLifecycleActive), ping(), state.NodeReadinessReady, ""},
		{"cannot adopt its base generation", node(state.NodeLifecycleActive), ping(adoptableBase, refusedBase), state.NodeReadinessDegraded, "cannot adopt base generation ghcr.io/p/base-minimal@sha256:db529ed"},
		{"recovering node that cannot adopt", node(state.NodeLifecycleRecovering), ping(refusedBase), state.NodeReadinessDegraded, "5ca69ea"},
		{"does not answer", node(state.NodeLifecycleActive), nil, state.NodeReadinessUnavailable, "does not answer"},
		{"operator drain wins", node(state.NodeLifecycleDraining), ping(refusedBase), state.NodeReadinessDraining, ""},
		{"maintenance reads draining", node(state.NodeLifecycleMaintenance), ping(adoptableBase), state.NodeReadinessDraining, ""},
		{"lifecycle unavailable wins", node(state.NodeLifecycleUnavailable), ping(adoptableBase), state.NodeReadinessUnavailable, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := sched.EvaluateNodeReadiness(tc.node, tc.ping)
			if got != tc.want || (tc.wantReason == "") != (reason == "") || !strings.Contains(reason, tc.wantReason) {
				t.Fatalf("readiness = %q (%q), want %q containing %q", got, reason, tc.want, tc.wantReason)
			}
		})
	}
}

func TestHeartbeat_RecordsProvenReadiness(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "fsn-3", TargetURL: "tcp://10.0.1.3:7000",
		VPCPUs: 8, MemMB: 16384, MaxConcurrency: 50, AdmissionCeilingMB: 8593, VCPUBudget: 80, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if node.Readiness != state.NodeReadinessReady {
		t.Fatalf("new node readiness = %q, want the column default ready", node.Readiness)
	}
	dialer := &sched.HeartbeatFakeDialer{}
	ops := wire.NewOpsMetrics("schedd")
	hb := sched.NewHeartbeat(store, dialer, nil, nil).WithOpsMetrics(ops).WithOwnerNodeID(node.ID)
	hb.FailureThreshold = 1
	for _, step := range []struct {
		name       string
		bases      []sched.BaseGenerationReadiness
		pingErr    error
		want       state.NodeReadiness
		wantReason string
	}{
		{name: "adopts its generation", bases: []sched.BaseGenerationReadiness{adoptableBase}, want: state.NodeReadinessReady},
		{name: "rolled back below the published generation", bases: []sched.BaseGenerationReadiness{refusedBase}, want: state.NodeReadinessDegraded, wantReason: "5ca69ea"},
		{name: "generation published again", bases: []sched.BaseGenerationReadiness{adoptableBase}, want: state.NodeReadinessReady},
		{name: "vmmd stops answering", pingErr: errors.New("connection refused"), want: state.NodeReadinessUnavailable, wantReason: "does not answer"},
	} {
		dialer.SetPingResult(node.TargetURL, step.bases, step.pingErr)
		if err := hb.Tick(ctx); err != nil {
			t.Fatalf("%s: Tick: %v", step.name, err)
		}
		got, err := store.ComputeNodeByID(ctx, node.ID)
		if err != nil {
			t.Fatal(err)
		}
		reason := ""
		if got.ReadinessReason != nil {
			reason = *got.ReadinessReason
		}
		if got.Readiness != step.want || (step.wantReason == "") != (reason == "") || !strings.Contains(reason, step.wantReason) {
			t.Fatalf("%s: stored readiness = %q (%q), want %q containing %q", step.name, got.Readiness, reason, step.want, step.wantReason)
		}
		for _, readiness := range wire.ComputeNodeReadinessStates {
			want := 0.0
			if readiness == string(step.want) {
				want = 1
			}
			if value := counterOrGauge(t, ops, "schedd_compute_node_readiness", map[string]string{"node": "fsn-3", "state": readiness}); value != want {
				t.Fatalf("%s: schedd_compute_node_readiness{state=%q} = %v, want %v", step.name, readiness, value, want)
			}
		}
	}
}

func counterOrGauge(t *testing.T, ops *wire.OpsMetrics, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := ops.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
	metrics:
		for _, metric := range family.GetMetric() {
			for label, value := range labels {
				if labelValue(metric, label) != value {
					continue metrics
				}
			}
			if gauge := metric.GetGauge(); gauge != nil {
				return gauge.GetValue()
			}
			return metric.GetCounter().GetValue()
		}
	}
	return -1
}

func TestChoosePlacement_PrefersProvenNodes(t *testing.T) {
	node := func(id string, readiness state.NodeReadiness, ceilingMB int) state.ComputeNode {
		return state.ComputeNode{ID: id, Name: id, Active: true, Lifecycle: state.NodeLifecycleActive, LifecycleGeneration: 1,
			AdmissionCeilingMB: ceilingMB, VCPUBudget: 80, VPCPUs: 8, Readiness: readiness}
	}
	base := sched.Request{AppID: "a", Plan: api.PlanPro, RAMMB: 256, VCPU: 1}
	for _, tc := range []struct {
		name   string
		nodes  []state.ComputeNode
		used   map[string]int64
		mutate func(*sched.Request)
		want   string
	}{
		{name: "idle degraded node loses to a busier ready one",
			nodes: []state.ComputeNode{node("fsn-2", state.NodeReadinessReady, 4096), node("fsn-3", state.NodeReadinessDegraded, 4096)},
			used:  map[string]int64{"fsn-2": 2048}, want: "fsn-2"},
		{name: "warm hint on a degraded node is ignored",
			nodes:  []state.ComputeNode{node("fsn-2", state.NodeReadinessReady, 4096), node("fsn-3", state.NodeReadinessDegraded, 4096)},
			mutate: func(r *sched.Request) { r.PreferredNodeID = "fsn-3" }, want: "fsn-2"},
		{name: "snapshot replica on a degraded node is ignored",
			nodes: []state.ComputeNode{node("fsn-2", state.NodeReadinessReady, 4096), node("fsn-3", state.NodeReadinessDegraded, 4096)},
			mutate: func(r *sched.Request) {
				r.PreferredNodeIDs, r.PrioritizeSnapshotLocality = []string{"fsn-3"}, true
			}, want: "fsn-2"},
		{name: "degraded node takes the wake when no ready node fits",
			nodes: []state.ComputeNode{node("fsn-2", state.NodeReadinessReady, 512), node("fsn-3", state.NodeReadinessDegraded, 4096)},
			used:  map[string]int64{"fsn-2": 400}, want: "fsn-3"},
		{name: "a row without readiness counts as ready",
			nodes: []state.ComputeNode{node("fsn-2", "", 4096), node("fsn-3", state.NodeReadinessDegraded, 4096)},
			used:  map[string]int64{"fsn-2": 2048}, want: "fsn-2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			if tc.mutate != nil {
				tc.mutate(&r)
			}
			p, err := sched.ChoosePlacement(tc.nodes, tc.used, nil, r)
			if err != nil || p.NodeID != tc.want {
				t.Fatalf("ChoosePlacement = %q, %v; want %q", p.NodeID, err, tc.want)
			}
		})
	}
}

// End to end: once the heartbeat has recorded the owner node degraded, a
// wake goes straight to a ready node and never asks the degraded vmmd.
func TestWake_AvoidsDegradedNode(t *testing.T) {
	var f *failoverFleet
	f = newFailoverFleet(t, 2, 4096, func(nodeID string) error {
		if nodeID == f.nodes[0].ID {
			return generationRefusal()
		}
		return nil
	})
	if _, err := f.store.SetComputeNodeReadiness(f.ctx, f.nodes[0].ID, state.NodeReadinessDegraded, refusedBase.Reason); err != nil {
		t.Fatal(err)
	}
	app := f.app(t, api.PlanPro, 256, 5)
	res, err := f.e.Wake(f.ctx, app.ID, "", "", sched.TriggerGateway)
	if err != nil || res.NodeID != f.nodes[1].ID {
		t.Fatalf("Wake = %+v, %v; want the ready node", res, err)
	}
	if calls := f.vmm.totalCalls(); calls != 1 {
		t.Fatalf("vmmd wake calls = %d (%v), want 1: the degraded node was tried", calls, f.vmm.calls)
	}
}
