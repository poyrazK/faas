package state

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestHistoricalAlertEvidenceBoundaries(t *testing.T) {
	fired := time.Date(2026, 10, 5, 18, 10, 45, 0, time.UTC)
	cutover := fired.Add(-4 * time.Minute)
	facts := alertRollbackFacts{Metric: AlertMetricErrorRate, Comparison: AlertGt, Threshold: 5, WindowSpec: AlertWindow5m}
	for _, test := range []struct {
		name               string
		requests, failures int64
		shift              time.Duration
		available          bool
		status, code       string
	}{
		{"breach", 20, 2, 0, true, "pending", ""},
		{"at gt threshold", 20, 1, 0, true, "failed", "alert_rollback_deployment_healthy"},
		{"healthy", 20, 0, 0, true, "failed", "alert_rollback_deployment_healthy"},
		{"too few", 19, 2, 0, true, "blocked", "alert_rollback_evidence_insufficient"},
		{"zero samples", 0, 0, 0, true, "blocked", "alert_rollback_evidence_insufficient"},
		{"corrupt count", 20, 21, 0, true, "blocked", "alert_rollback_evidence_insufficient"},
		{"unavailable", 20, 2, 0, false, "blocked", "alert_rollback_telemetry_unavailable"},
		{"future fire", 20, 2, -time.Second, true, "failed", "alert_rollback_evidence_expired"},
		{"deadline", 20, 2, api.AlertRollbackEvidenceMaxCheckDelay, true, "failed", "alert_rollback_evidence_expired"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := api.AlertRollback{ID: uuid.NewString(), Historical: true, CandidateDeploymentID: uuid.NewString(), FiredAt: fired, Status: "pending"}
			r.DeploymentEvidence = newHistoricalAlertEvidence(r, facts, cutover)
			e := r.DeploymentEvidence
			if !e.WindowStart.Equal(cutover.Truncate(time.Minute).Add(time.Minute)) || !e.WindowEnd.Equal(fired.Add(-30*time.Second).Truncate(time.Minute)) {
				t.Fatalf("unclosed or cutover minutes included: %+v", e)
			}
			last := e.WindowEnd.Add(-time.Minute)
			got, qualified := qualifyHistoricalAlertEvidence(r, test.requests, test.failures, &last, test.available, fired.Add(test.shift))
			if got.Status != test.status || got.Code != test.code || qualified != (test.code == "") {
				t.Fatalf("decision %+v qualified=%t", got, qualified)
			}
			if e.Status != "pending" || e.CheckedAt != nil {
				t.Fatal("qualification mutated captured receipt")
			}
		})
	}
	for _, change := range []string{"gte", "negative threshold", "nan threshold", "lower comparison", "unknown window", "other metric", "partial minute"} {
		t.Run(change, func(t *testing.T) {
			r := api.AlertRollback{Historical: true, FiredAt: fired, CandidateDeploymentID: uuid.NewString()}
			e := newHistoricalAlertEvidence(r, facts, cutover)
			r.DeploymentEvidence = e
			switch change {
			case "gte":
				e.Comparison = "gte"
			case "negative threshold":
				e.Threshold = -1
			case "nan threshold":
				e.Threshold = math.NaN()
			case "lower comparison":
				e.Comparison = "lt"
			case "unknown window":
				e.WindowSpec = "5s"
			case "other metric":
				e.Metric = "request_count"
			case "partial minute":
				r.DeploymentEvidence = newHistoricalAlertEvidence(r, facts, fired.Add(-10*time.Second))
			}
			// NaN cannot be serialized into a receipt, and is refused by rule validation.
			if change == "nan threshold" {
				if historicalAlertEvidenceSupported(e) {
					t.Fatal("NaN supported")
				}
				return
			}
			last := e.WindowEnd.Add(-time.Minute)
			got, qualified := qualifyHistoricalAlertEvidence(r, 20, 1, &last, true, fired)
			if change == "gte" {
				if !qualified {
					t.Fatalf("gte boundary rejected %+v", got)
				}
				return
			}
			if qualified || change != "partial minute" && got.Code != "alert_rollback_metric_unsupported" || change == "partial minute" && got.Code != "alert_rollback_evidence_insufficient" {
				t.Fatalf("invalid qualification %+v", got)
			}
		})
	}
}
