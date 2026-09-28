// orchestrator_counters_test.go — SAFE-RELEASES-OBS PR-A: pin the
// Stats→wire.OpsMetrics counter handoff so the safedeploy_orchestrator_*
// counters surface from boot and the deployment_audit_emitted_total
// counter increments on every audit emit. Mirrors the per-package test
// conventions at orchestrator_test.go (stubStore, sync.Mutex-guarded,
// in-package).
package safedeploy

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestOrchestrator_IncOpsCountsActualOutcomes walks the stub store
// with two seeded rows (one pending+ladder → start, one rolling_out
// + terminal step → complete) and asserts that only those transition
// counters rise. An idle tick must leave all event counters unchanged.
func TestOrchestrator_IncOpsCountsActualOutcomes(t *testing.T) {
	ops := wire.NewOpsMetrics("meterd_test_obs_pr_a")
	store := newStubStore()

	// pending + ladder → start
	seedDeployment(store, t, func(d *state.Deployment) {
		d.RolloutState = "pending"
		d.CanaryStep = 0
		d.CanaryTotalSteps = 3
	})
	// rolling_out + terminal step → complete
	seedDeployment(store, t, func(d *state.Deployment) {
		d.RolloutState = "rolling_out"
		d.CanaryStep = 3
		d.CanaryTotalSteps = 3
	})

	orch := NewOrchestrator(store, discardLog(), "meterd:test", "")
	orch.Ops = ops

	stats, inFlight, err := orch.Once(context.Background())
	if err != nil {
		t.Fatalf("orchestrator.Once: %v", err)
	}
	orch.IncOps(ops, stats, inFlight, err)

	if got := stats.Started; got != 1 {
		t.Errorf("stats.Started = %d, want 1", got)
	}
	if got := stats.Completed; got != 1 {
		t.Errorf("stats.Completed = %d, want 1", got)
	}

	cases := []struct {
		name string
		c    prometheus.Counter
		want float64
	}{
		{"SafedeployOrchestratorStartedTotal", ops.SafedeployOrchestratorStartedTotal(), 1},
		{"SafedeployOrchestratorCompletedTotal", ops.SafedeployOrchestratorCompletedTotal(), 1},
		{"SafedeployOrchestratorAbortedTotal", ops.SafedeployOrchestratorAbortedTotal(), 0},
		{"SafedeployOrchestratorStuckDetectedTotal", ops.SafedeployOrchestratorStuckDetectedTotal(), 0},
		{"SafedeployOrchestratorAuditEmitFailedTotal", ops.SafedeployOrchestratorAuditEmitFailedTotal(), 0},
		{"SafedeployOrchestratorStuckCheckMissingTimestampTotal", ops.SafedeployOrchestratorStuckCheckMissingTimestampTotal(), 0},
	}
	idle := NewOrchestrator(newStubStore(), discardLog(), "meterd:test", "")
	idleStats, idleInFlight, idleErr := idle.Once(context.Background())
	if idleErr != nil {
		t.Fatalf("idle orchestrator.Once: %v", idleErr)
	}
	idle.IncOps(ops, idleStats, idleInFlight, idleErr)
	for _, tc := range cases {
		if tc.c == nil {
			t.Errorf("%s: nil counter", tc.name)
			continue
		}
		if got := testutil.ToFloat64(tc.c); got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestOrchestrator_IncOpsCountsMultipleRecoveries(t *testing.T) {
	ops := wire.NewOpsMetrics("meterd_test_obs_recovery_counts")
	orch := NewOrchestrator(newStubStore(), discardLog(), "meterd:test", "")
	orch.IncOps(ops, Stats{
		StuckDetected: 2, StuckCheckMissingTimestamp: 1,
		AutoAborted: 2, AutoAbortFailed: 1,
	}, 2, nil)
	for _, tc := range []struct {
		name string
		c    prometheus.Counter
		want float64
	}{
		{"stuck", ops.SafedeployOrchestratorStuckDetectedTotal(), 2},
		{"missing_timestamp", ops.SafedeployOrchestratorStuckCheckMissingTimestampTotal(), 1},
		{"aborted", ops.SafedeployOrchestratorAbortedTotal(), 2},
		{"auto_aborted", ops.SafedeployOrchestratorAutoAbortedTotal(), 2},
		{"auto_abort_failed", ops.SafedeployOrchestratorAutoAbortFailedTotal(), 1},
	} {
		if got := testutil.ToFloat64(tc.c); got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestOrchestrator_AuditEmittedTotal_BumpsOnSuccess pins the
// happy-path counter increment: a successful AppendDeploymentAudit
// call bumps deployment_audit_emitted_total{kind, "ok"} by 1.
func TestOrchestrator_AuditEmittedTotal_BumpsOnSuccess(t *testing.T) {
	ops := wire.NewOpsMetrics("meterd_test_obs_pr_a_ok")
	store := newStubStore()
	seedDeployment(store, t, func(d *state.Deployment) {
		d.RolloutState = "pending"
		d.CanaryStep = 0
		d.CanaryTotalSteps = 3
	})
	orch := NewOrchestrator(store, discardLog(), "meterd:test", "")
	orch.Ops = ops

	if _, _, err := orch.Once(context.Background()); err != nil {
		t.Fatalf("orchestrator.Once: %v", err)
	}
	c := ops.DeploymentAuditEmittedTotal("deploy.rollout_started", "ok")
	if c == nil {
		t.Fatal("DeploymentAuditEmittedTotal returned nil counter for valid kind+outcome")
	}
	if got := testutil.ToFloat64(c); got != 1 {
		t.Errorf("DeploymentAuditEmittedTotal{deploy.rollout_started, ok} = %v, want 1", got)
	}
}

// TestOrchestrator_AuditEmittedTotal_BumpsOnFailure pins the
// failure-path counter increment: when stubStore.auditErr is set,
// the audit emit fails AND the deployment_audit_emitted_total counter
// ticks up with outcome="failed" so the dashboard's audit-write-fidelity
// panel can split per-kind emit rate from failure rate.
func TestOrchestrator_AuditEmittedTotal_BumpsOnFailure(t *testing.T) {
	ops := wire.NewOpsMetrics("meterd_test_obs_pr_a_fail")
	store := newStubStore()
	store.auditErr = errors.New("simulated postgres outage")
	seedDeployment(store, t, func(d *state.Deployment) {
		d.RolloutState = "pending"
		d.CanaryStep = 0
		d.CanaryTotalSteps = 3
	})
	orch := NewOrchestrator(store, discardLog(), "meterd:test", "")
	orch.Ops = ops

	stats, _, err := orch.Once(context.Background())
	if err != nil {
		t.Fatalf("orchestrator.Once: %v", err)
	}
	if stats.AuditEmitFailed != 1 {
		t.Errorf("stats.AuditEmitFailed = %d, want 1", stats.AuditEmitFailed)
	}
	orch.IncOps(ops, stats, 0, err)
	if got := testutil.ToFloat64(ops.SafedeployOrchestratorAuditEmitFailedTotal()); got != 1 {
		t.Errorf("SafedeployOrchestratorAuditEmitFailedTotal = %v, want 1", got)
	}
	c := ops.DeploymentAuditEmittedTotal("deploy.rollout_started", "failed")
	if c == nil {
		t.Fatal("DeploymentAuditEmittedTotal returned nil counter for valid kind+outcome=failed")
	}
	if got := testutil.ToFloat64(c); got != 1 {
		t.Errorf("DeploymentAuditEmittedTotal{deploy.rollout_started, failed} = %v, want 1", got)
	}
	// The "ok" series stays at 0.
	cOk := ops.DeploymentAuditEmittedTotal("deploy.rollout_started", "ok")
	if got := testutil.ToFloat64(cOk); got != 0 {
		t.Errorf("DeploymentAuditEmittedTotal{deploy.rollout_started, ok} = %v, want 0", got)
	}
}

// TestOrchestrator_NilOps_Safe pins that IncOps + emitAudit are
// nil-safe (Ops == nil means the test seam without a Prometheus
// registry). No panic; Stats still flow through the journal line.
func TestOrchestrator_NilOps_Safe(t *testing.T) {
	store := newStubStore()
	seedDeployment(store, t, nil)
	orch := NewOrchestrator(store, discardLog(), "meterd:test", "")
	// Ops intentionally left nil.
	stats, _, err := orch.Once(context.Background())
	if err != nil {
		t.Fatalf("orchestrator.Once: %v", err)
	}
	orch.IncOps(nil, stats, 0, err) // must not panic
}

// TestOpsMetrics_DeploymentAuditEmittedTotal_UnknownKindDrops pins
// the closed-vocabulary admission gate at the accessor level. An
// unknown kind (e.g. a typo) returns nil so Prometheus cardinality
// stays bounded AND so testutil.ToFloat64 surfaces a clean 0
// instead of panicking on a nil deref. Mirrors the
// AlertActionExecutedTotal(unknown) precedent.
func TestOpsMetrics_DeploymentAuditEmittedTotal_UnknownKindDrops(t *testing.T) {
	ops := wire.NewOpsMetrics("meterd_test_obs_pr_a_drop")
	if c := ops.DeploymentAuditEmittedTotal("deploy.bogus_kind", "ok"); c != nil {
		t.Errorf("expected nil counter for unknown kind; got %v", testutil.ToFloat64(c))
	}
	if c := ops.DeploymentAuditEmittedTotal("deploy.rollout_started", "bogus_outcome"); c != nil {
		t.Errorf("expected nil counter for unknown outcome; got %v", testutil.ToFloat64(c))
	}
}

// TestOrchestrator_IncOps_SetsInFlightGauge (PR-B) pins the gauge
// behaviour: IncOps(ops, stats, inFlight, err) sets the
// safedeploy_in_flight_rollouts gauge to the inFlight value. The
// orchestrator hands the row count from SafedeployListPendingRollouts
// to the gauge only after a successful tick.
func TestOrchestrator_IncOps_SetsInFlightGauge(t *testing.T) {
	store := newStubStore()
	seedDeployment(store, t, nil)
	orch := NewOrchestrator(store, discardLog(), "meterd:test", "")
	ops := wire.NewOpsMetrics("meterd_test_obs_pr_b_gauge")
	gauge := ops.SafedeployInFlightRollouts()
	if gauge == nil {
		t.Fatalf("expected non-nil in-flight gauge")
	}
	if got := testutil.ToFloat64(gauge); got != 0 {
		t.Fatalf("expected zero-init gauge, got %v", got)
	}
	stats, inFlight, err := orch.Once(context.Background())
	if err != nil {
		t.Fatalf("orchestrator.Once: %v", err)
	}
	if inFlight < 1 {
		t.Fatalf("expected inFlight>=1 (seedDeployment inserts a row), got %d", inFlight)
	}
	orch.IncOps(ops, stats, inFlight, err)
	if got := testutil.ToFloat64(gauge); got != float64(inFlight) {
		t.Fatalf("expected gauge=%d after IncOps(%d), got %v", inFlight, inFlight, got)
	}
	store.listErr = errors.New("listing failed")
	stats, failedCount, err := orch.Once(context.Background())
	if !errors.Is(err, store.listErr) || failedCount != 0 {
		t.Fatalf("failed listing: count=%d err=%v", failedCount, err)
	}
	orch.IncOps(ops, stats, failedCount, err)
	if got := testutil.ToFloat64(gauge); got != float64(inFlight) {
		t.Fatalf("failed listing overwrote last known count: got %v, want %d", got, inFlight)
	}
	store.listErr = nil
	store.rollouts = map[string]state.Deployment{}
	stats, emptyCount, err := orch.Once(context.Background())
	if err != nil || emptyCount != 0 {
		t.Fatalf("empty listing: count=%d err=%v", emptyCount, err)
	}
	orch.IncOps(ops, stats, emptyCount, err)
	if got := testutil.ToFloat64(gauge); got != 0 {
		t.Fatalf("successful empty listing should clear gauge: got %v", got)
	}
}
