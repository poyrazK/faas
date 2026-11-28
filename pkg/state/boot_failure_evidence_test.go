package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-605 amendment 2: a boot_failures rule rolls a completed release back
// only when the release's own failed boots meet the rule and it has not
// booted or restored once since cutover.
func TestBootFailureEvidenceBoundaries(t *testing.T) {
	fired := time.Date(2026, 11, 28, 9, 10, 45, 0, time.UTC)
	cutover := fired.Add(-4 * time.Minute)
	for _, test := range []struct {
		name                string
		comparison          state.AlertComparison
		failures, successes int64
		shift               time.Duration
		available           bool
		status, code        string
	}{
		{"crash on start", state.AlertGte, 3, 0, 0, true, "pending", ""},
		{"below threshold", state.AlertGte, 2, 0, 0, true, "failed", "alert_rollback_deployment_healthy"},
		{"at gt threshold", state.AlertGt, 3, 0, 0, true, "failed", "alert_rollback_deployment_healthy"},
		{"node fault under a healthy release", state.AlertGte, 9, 1, 0, true, "failed", "alert_rollback_deployment_healthy"},
		{"no boots", state.AlertGte, 0, 0, 0, true, "failed", "alert_rollback_deployment_healthy"},
		{"corrupt count", state.AlertGte, -1, 0, 0, true, "blocked", "alert_rollback_evidence_insufficient"},
		{"unavailable", state.AlertGte, 3, 0, 0, false, "blocked", "alert_rollback_telemetry_unavailable"},
		{"future fire", state.AlertGte, 3, 0, -time.Second, true, "failed", "alert_rollback_evidence_expired"},
		{"deadline", state.AlertGte, 3, 0, api.AlertRollbackEvidenceMaxCheckDelay, true, "failed", "alert_rollback_evidence_expired"},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts := state.AlertRollbackFacts{Metric: state.AlertMetricBootFailures, Comparison: test.comparison, Threshold: 3, WindowSpec: state.AlertWindow15m}
			r := api.AlertRollback{ID: uuid.NewString(), Historical: true, CandidateDeploymentID: uuid.NewString(), FiredAt: fired, Status: "pending"}
			r.DeploymentEvidence = state.NewHistoricalAlertEvidence(r, facts, cutover)
			e := r.DeploymentEvidence
			// The 15m window reaches before the cutover, so it starts there,
			// and boot events need no ingestion lag, so it ends at the fire.
			if !e.WindowStart.Equal(cutover) || !e.WindowEnd.Equal(fired) || e.MinimumRequests != 0 {
				t.Fatalf("boot evidence window %+v", e)
			}
			got, qualified := state.QualifyBootFailureEvidence(r, test.failures, test.successes, test.available, fired.Add(test.shift))
			if got.Status != test.status || got.Code != test.code || qualified != (test.code == "") {
				t.Fatalf("decision %+v qualified=%t", got, qualified)
			}
			if test.code == "" && (got.DeploymentEvidence.BootFailures != test.failures || got.DeploymentEvidence.Status != "breached") {
				t.Fatalf("accepted evidence %+v", got.DeploymentEvidence)
			}
			if e.Status != "pending" || e.CheckedAt != nil {
				t.Fatal("qualification mutated captured receipt")
			}
		})
	}
}

func TestHistoricalAlertRollbackSupportsBootFailures(t *testing.T) {
	for _, test := range []struct {
		metric, comparison string
		threshold          float64
		want               bool
	}{
		{"boot_failures", "gte", 3, true},
		{"boot_failures", "gt", 250, true},
		{"boot_failures", "lt", 3, false},
		{"boot_failures", "gte", -1, false},
		{"error_rate_pct", "gt", 5, true},
		{"error_rate_pct", "gt", 101, false},
		{"job_failures", "gt", 0, false},
	} {
		if got := state.HistoricalAlertRollbackSupported(test.metric, test.comparison, "15m", test.threshold); got != test.want {
			t.Errorf("%s %s %v = %t, want %t", test.metric, test.comparison, test.threshold, got, test.want)
		}
	}
}
