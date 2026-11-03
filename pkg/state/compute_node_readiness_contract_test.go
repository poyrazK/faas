// adr: 20261022184531
package state_test

import (
	"context"
	"testing"
	"time"

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

func TestMemComputeNodeWakeBreaker(t *testing.T) {
	testComputeNodeWakeBreaker(t, state.NewMemStore())
}

// A node's wake breaker opens until a time, reads degraded until then and
// half-open after, closes only when set, and survives vmmd re-registration.
func testComputeNodeWakeBreaker(t *testing.T, store computeNodeReadinessTestStore) {
	ctx := context.Background()
	node, err := store.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{Name: "fsn-breaker", TargetURL: "tcp://10.0.9.4:7000",
		VPCPUs: 8, MemMB: 16384, MaxConcurrency: 50, AdmissionCeilingMB: 8593, VCPUBudget: 80, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	read := func() state.ComputeNode {
		t.Helper()
		got, err := store.ComputeNodeByID(ctx, node.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	now := time.Now()
	if got := read(); got.WakeBreakerOpenUntil != nil || got.EffectiveReadinessAt(now) != state.NodeReadinessReady {
		t.Fatalf("new node breaker = %v, readiness %q", got.WakeBreakerOpenUntil, got.EffectiveReadinessAt(now))
	}
	if closed, err := store.CloseComputeNodeWakeBreaker(ctx, node.ID); err != nil || closed {
		t.Fatalf("closing a closed breaker = %t, %v", closed, err)
	}
	until := now.Add(30 * time.Second).Truncate(time.Microsecond)
	const reason = "3 consecutive base_generation refusals"
	if err := store.OpenComputeNodeWakeBreaker(ctx, node.ID, until, reason); err != nil {
		t.Fatal(err)
	}
	got := read()
	if got.WakeBreakerOpenUntil == nil || !got.WakeBreakerOpenUntil.Equal(until) || got.WakeBreakerReason == nil || *got.WakeBreakerReason != reason {
		t.Fatalf("opened breaker = %v (%v), want %v (%q)", got.WakeBreakerOpenUntil, got.WakeBreakerReason, until, reason)
	}
	if r := got.EffectiveReadinessAt(now); r != state.NodeReadinessDegraded {
		t.Fatalf("readiness while open = %q, want degraded", r)
	}
	if r := got.EffectiveReadinessAt(until.Add(time.Second)); r != state.NodeReadinessReady {
		t.Fatalf("readiness once half-open = %q, want ready", r)
	}
	if _, err := store.UpsertComputeNodeFromVmmd(ctx, state.ComputeNode{Name: "fsn-breaker", TargetURL: "tcp://10.0.9.4:7000",
		VPCPUs: 8, MemMB: 16384, MaxConcurrency: 50, AdmissionCeilingMB: 8593, VCPUBudget: 80, Active: true}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.WakeBreakerOpenUntil == nil {
		t.Fatal("vmmd re-registration closed the wake breaker")
	}
	if closed, err := store.CloseComputeNodeWakeBreaker(ctx, node.ID); err != nil || !closed {
		t.Fatalf("closing an open breaker = %t, %v", closed, err)
	}
	if got := read(); got.WakeBreakerOpenUntil != nil || got.WakeBreakerReason != nil {
		t.Fatalf("closed breaker = %v (%v)", got.WakeBreakerOpenUntil, got.WakeBreakerReason)
	}
}

func TestMemComputeNodeCPUPressure(t *testing.T) {
	testComputeNodeCPUPressure(t, state.NewMemStore())
}

// adr: 20261030100950 — a node's CPU pressure hold is set, read back,
// expires on its own without changing readiness, and survives vmmd
// re-registration and the heartbeat's readiness writes.
func testComputeNodeCPUPressure(t *testing.T, store computeNodeReadinessTestStore) {
	ctx := context.Background()
	node, err := store.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{Name: "fsn-pressure", TargetURL: "tcp://10.0.9.5:7000",
		VPCPUs: 8, MemMB: 16384, MaxConcurrency: 50, AdmissionCeilingMB: 8593, VCPUBudget: 80, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	read := func() state.ComputeNode {
		t.Helper()
		got, err := store.ComputeNodeByID(ctx, node.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	now := time.Now()
	if got := read(); got.CPUPressureUntil != nil || got.CPUPressured(now) {
		t.Fatalf("new node hold = %v", got.CPUPressureUntil)
	}
	until := now.Add(30 * time.Second).Truncate(time.Microsecond)
	for range 2 { // the second write is the same value
		if err := store.HoldComputeNodeCPUPressure(ctx, node.ID, until); err != nil {
			t.Fatal(err)
		}
	}
	got := read()
	if got.CPUPressureUntil == nil || !got.CPUPressureUntil.Equal(until) || !got.CPUPressured(now) || got.CPUPressured(until) {
		t.Fatalf("hold = %v, want pressured until %v", got.CPUPressureUntil, until)
	}
	if r := got.EffectiveReadinessAt(now); r != state.NodeReadinessReady {
		t.Fatalf("readiness while held = %q, want ready: the hold is not readiness", r)
	}
	if _, err := store.SetComputeNodeReadiness(ctx, node.ID, state.NodeReadinessReady, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertComputeNodeFromVmmd(ctx, state.ComputeNode{Name: "fsn-pressure", TargetURL: "tcp://10.0.9.5:7000",
		VPCPUs: 8, MemMB: 16384, MaxConcurrency: 50, AdmissionCeilingMB: 8593, VCPUBudget: 80, Active: true}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.CPUPressureUntil == nil || !got.CPUPressureUntil.Equal(until) {
		t.Fatalf("hold after readiness and re-registration writes = %v, want %v", got.CPUPressureUntil, until)
	}
	if err := store.HoldComputeNodeCPUPressure(ctx, "00000000-0000-0000-0000-000000000000", until); err != nil {
		t.Fatalf("hold on an unknown node: %v", err)
	}
}
