// adr: 20261022184531
package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type computeNodeReadinessTestStore = contractTestStore

func TestMemComputeNodeReadiness(t *testing.T) {
	testComputeNodeReadiness(t, state.NewMemStore())
}

// A node's readiness defaults to ready, changes only through
// SetComputeNodeReadiness, reports whether a write changed it, and survives
// vmmd's re-registration.
func testComputeNodeReadiness(t *testing.T, store computeNodeReadinessTestStore) {
	ctx := context.Background()
	node, err := store.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{Name: "fsn-readiness", TargetURL: "tcp://10.0.9.3:7000",
		VPCPUs: 8, MemMB: 16384, MaxConcurrency: 50, AdmissionCeilingMB: 8593, VCPUBudget: 80, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	read := func(step string) (state.NodeReadiness, string) {
		t.Helper()
		got, err := store.ComputeNodeByID(ctx, node.ID)
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		reason := ""
		if got.ReadinessReason != nil {
			reason = *got.ReadinessReason
		}
		return got.Readiness, reason
	}
	if readiness, reason := read("new node"); readiness != state.NodeReadinessReady || reason != "" {
		t.Fatalf("new node readiness = %q (%q), want ready", readiness, reason)
	}
	const reason = "cannot adopt base generation ghcr.io/p/base-minimal@sha256:db529ed"
	for _, step := range []struct {
		name        string
		readiness   state.NodeReadiness
		reason      string
		wantChanged bool
	}{
		{"degrade", state.NodeReadinessDegraded, reason, true},
		{"same verdict again", state.NodeReadinessDegraded, reason, false},
		{"new reason", state.NodeReadinessDegraded, reason + " (again)", true},
		{"recover", state.NodeReadinessReady, "", true},
		{"stay ready", state.NodeReadinessReady, "", false},
		{"drain", state.NodeReadinessDraining, "", true},
	} {
		changed, err := store.SetComputeNodeReadiness(ctx, node.ID, step.readiness, step.reason)
		if err != nil || changed != step.wantChanged {
			t.Fatalf("%s: SetComputeNodeReadiness = %t, %v; want changed=%t", step.name, changed, err, step.wantChanged)
		}
		if readiness, gotReason := read(step.name); readiness != step.readiness || gotReason != step.reason {
			t.Fatalf("%s: stored %q (%q), want %q (%q)", step.name, readiness, gotReason, step.readiness, step.reason)
		}
	}
	if _, err := store.SetComputeNodeReadiness(ctx, node.ID, "flapping", ""); err == nil {
		t.Fatal("an unknown readiness was stored")
	}
	if changed, err := store.SetComputeNodeReadiness(ctx, "00000000-0000-0000-0000-000000000000", state.NodeReadinessDegraded, reason); err != nil || changed {
		t.Fatalf("unknown node: changed=%t, %v", changed, err)
	}
	if _, err := store.SetComputeNodeReadiness(ctx, node.ID, state.NodeReadinessDegraded, reason); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertComputeNodeFromVmmd(ctx, state.ComputeNode{Name: "fsn-readiness", TargetURL: "tcp://10.0.9.3:7000",
		VPCPUs: 8, MemMB: 16384, MaxConcurrency: 50, AdmissionCeilingMB: 8593, VCPUBudget: 80, Active: true}); err != nil {
		t.Fatal(err)
	}
	if readiness, gotReason := read("vmmd re-registration"); readiness != state.NodeReadinessDegraded || gotReason != reason {
		t.Fatalf("vmmd re-registration reset readiness to %q (%q)", readiness, gotReason)
	}
	nodes, err := store.ListComputeNodes(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.ID == node.ID && n.Readiness != state.NodeReadinessDegraded {
			t.Fatalf("listed readiness = %q, want degraded", n.Readiness)
		}
	}
}
