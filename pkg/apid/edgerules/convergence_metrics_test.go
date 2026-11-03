package edgerules

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-27 (GA hardening): a gateway left out of the
// barrier is counted by node, and a phase that does not finish is counted by
// phase, so alerts see both without reading apid's logs.
func TestEdgeRuleConvergenceMetrics(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	role, gateway := "compute-node", "tcp://10.0.0.1:8080"
	for _, name := range []string{"edge-live", "edge-stale"} {
		if _, err := mem.CreateComputeNode(ctx, state.ComputeNode{Name: name, TargetURL: "tcp://10.0.0.1:9000", VPCPUs: 8, MemMB: 8192,
			MaxConcurrency: 10, AdmissionCeilingMB: 4096, VCPUBudget: 8, Active: true, Role: &role, GatewayTargetURL: &gateway}); err != nil {
			t.Fatal(err)
		}
	}
	if err := mem.TouchEdgeListenerHeartbeat(ctx, "edge-live"); err != nil {
		t.Fatal(err)
	}
	mem.SetEdgeListenerHeartbeatForTest("edge-stale", time.Now().Add(-state.EdgeListenerStaleAfter-time.Second))
	metrics := NewMetrics(prometheus.NewRegistry(), "apid")
	svc := &Service{Host: attackModeHost{store: mem, notif: &recordingNotifier{}}, Metrics: metrics}
	if _, err := svc.ServingEdgeRuleNodes(ctx); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(metrics.skippedGateways.WithLabelValues("edge-stale")); got != 1 {
		t.Fatalf("skipped edge-stale = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.skippedGateways.WithLabelValues("edge-live")); got != 0 {
		t.Fatalf("skipped edge-live = %v, want 0", got)
	}

	// No gateway acknowledges, and the caller has already given up.
	conv := &EdgeRuleConvergence{Notif: &recordingNotifier{}, Generation: 7, Expected: map[string]struct{}{"edge-live": {}},
		Cancel: func() {}, Unlock: func(context.Context) {}, Metrics: metrics}
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if err := conv.notifyAndWait(gone, ConvergencePhaseApply); err == nil {
		t.Fatal("an unacknowledged apply reported success")
	}
	if got := testutil.ToFloat64(metrics.failures.WithLabelValues(ConvergencePhaseApply)); got != 1 {
		t.Fatalf("apply failures = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.failures.WithLabelValues(ConvergencePhasePrepare)); got != 0 {
		t.Fatalf("prepare failures = %v, want 0", got)
	}

	// A nil Metrics records nothing and does not panic.
	conv.Metrics = nil
	_ = conv.notifyAndWait(gone, ConvergencePhasePrepare)
}
