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

// TestOrchestrator_IncOps_BumpsAllSixCounters walks the stub store
// with two seeded rows (one pending+ladder → start, one rolling_out
// + terminal step → complete) and asserts that IncOps bumps every
// orchestrator counter by exactly one. The accessor pattern returns
// the prometheus.Counter directly (LogsDropped precedent) so the test
// reads the value with testutil.ToFloat64 without walking the
// registry's Gather() output.
//
// The current design bumps all 6 counters per tick regardless of
// Stats values — the journal line at the cmd/meterd call site
// carries the per-tick numbers, and PR-B's
// safedeploy_orchestrator_*_total rate() queries roll the counter
// into per-second rates. Pinning this so a future refactor that
// gates on Stats doesn't silently break the alert tripwires.
func TestOrchestrator_IncOps_BumpsAllSixCounters(t *testing.T) {
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

	stats, err := orch.Once(context.Background())
	if err != nil {
		t.Fatalf("orchestrator.Once: %v", err)
	}
	orch.IncOps(ops, stats)

	if got := stats.Started; got != 1 {
		t.Errorf("stats.Started = %d, want 1", got)
	}
	if got := stats.Completed; got != 1 {
		t.Errorf("stats.Completed = %d, want 1", got)
	}

	// IncOps bumps every orchestrator counter by 1 per tick.
	cases := []struct {
		name string
		c    prometheus.Counter
		want float64
	}{
		{"SafedeployOrchestratorStartedTotal", ops.SafedeployOrchestratorStartedTotal(), 1},
		{"SafedeployOrchestratorCompletedTotal", ops.SafedeployOrchestratorCompletedTotal(), 1},
		{"SafedeployOrchestratorAbortedTotal", ops.SafedeployOrchestratorAbortedTotal(), 1},
		{"SafedeployOrchestratorStuckDetectedTotal", ops.SafedeployOrchestratorStuckDetectedTotal(), 1},
		{"SafedeployOrchestratorAuditEmitFailedTotal", ops.SafedeployOrchestratorAuditEmitFailedTotal(), 1},
		{"SafedeployOrchestratorStuckCheckMissingTimestampTotal", ops.SafedeployOrchestratorStuckCheckMissingTimestampTotal(), 1},
	}
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

	if _, err := orch.Once(context.Background()); err != nil {
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

	stats, err := orch.Once(context.Background())
	if err != nil {
		t.Fatalf("orchestrator.Once: %v", err)
	}
	if stats.AuditEmitFailed != 1 {
		t.Errorf("stats.AuditEmitFailed = %d, want 1", stats.AuditEmitFailed)
	}
	orch.IncOps(ops, stats)
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
	stats, err := orch.Once(context.Background())
	if err != nil {
		t.Fatalf("orchestrator.Once: %v", err)
	}
	orch.IncOps(nil, stats) // must not panic
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
