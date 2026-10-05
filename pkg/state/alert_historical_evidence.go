// adr: 605 — qualify post-release rollback with exact deployment telemetry.
package state

import (
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrAlertRollbackEvidenceExpired = errors.New("state: historical alert evidence expired before intent acceptance")

func historicalAlertCutoverMatches(r api.AlertRollback, f alertRollbackFacts) bool {
	for _, d := range f.Deployments {
		if d.ID == r.CandidateDeploymentID {
			return completedAlertDeployment(d) && normalizedDeploymentScope(d.Scope) == r.Scope && d.CompletedAt != nil &&
				r.DeploymentEvidence != nil && d.CompletedAt.Equal(r.DeploymentEvidence.CutoverAt)
		}
	}
	return false
}

func newHistoricalAlertEvidence(r api.AlertRollback, f alertRollbackFacts, cutover time.Time) *api.AlertRollbackDeploymentEvidence {
	end := r.FiredAt.Add(-api.AlertRollbackEvidenceIngestionLag).UTC().Truncate(time.Minute)
	start := end.Add(-historicalAlertMetricWindow(f.WindowSpec))
	// A bucket containing cutover includes requests from before the switch.
	first := cutover.UTC().Truncate(time.Minute)
	if first.Before(cutover) {
		first = first.Add(time.Minute)
	}
	if start.Before(first) {
		start = first
	}
	return &api.AlertRollbackDeploymentEvidence{Version: 1, DeploymentID: r.CandidateDeploymentID,
		Metric: string(f.Metric), Comparison: string(f.Comparison), Threshold: f.Threshold, WindowSpec: string(f.WindowSpec),
		CutoverAt: cutover.UTC(), WindowStart: start, WindowEnd: end, MinimumRequests: api.AlertRollbackEvidenceMinRequests, Status: "pending"}
}

func historicalAlertMetricWindow(spec AlertWindowSpec) time.Duration {
	switch spec {
	case AlertWindow5m:
		return 5 * time.Minute
	case AlertWindow15m:
		return 15 * time.Minute
	case AlertWindow1h:
		return time.Hour
	case AlertWindow6h:
		return 6 * time.Hour
	case AlertWindow24h:
		return 24 * time.Hour
	case AlertWindow7d:
		return 7 * 24 * time.Hour
	case AlertWindow15d:
		return 15 * 24 * time.Hour
	default:
		return 0
	}
}

func historicalAlertEvidenceMatches(fresh, current api.AlertRollback) bool {
	if !current.Historical {
		return true
	}
	a, b := fresh.DeploymentEvidence, current.DeploymentEvidence
	return a != nil && b != nil && a.Version == b.Version && a.DeploymentID == b.DeploymentID &&
		a.Metric == b.Metric && a.Comparison == b.Comparison && a.Threshold == b.Threshold && a.WindowSpec == b.WindowSpec &&
		a.CutoverAt.Equal(b.CutoverAt) && a.WindowStart.Equal(b.WindowStart) && a.WindowEnd.Equal(b.WindowEnd)
}

func historicalAlertEvidenceSupported(e *api.AlertRollbackDeploymentEvidence) bool {
	return e != nil && e.Version == 1 && e.Metric == string(AlertMetricErrorRate) &&
		(e.Comparison == string(AlertGt) || e.Comparison == string(AlertGte)) && api.IsFiniteFloat(e.Threshold) &&
		e.Threshold >= 0 && e.Threshold <= 100 && historicalAlertMetricWindow(AlertWindowSpec(e.WindowSpec)) > 0
}

// qualifyHistoricalAlertEvidence returns whether an intent may be accepted.
// A blocked observation can be retried within its fixed window and deadline;
// neither a healthy observation nor an expired fire may acquire new samples.
func qualifyHistoricalAlertEvidence(r api.AlertRollback, requests, errors int64, last *time.Time, available bool, now time.Time) (api.AlertRollback, bool) {
	r = cloneAlertRollback(r)
	e := r.DeploymentEvidence
	if e == nil {
		return alertRollbackProgress(r, "failed", "alert_rollback_evidence_missing", nil), false
	}
	now = now.UTC()
	e.CheckedAt, e.LastSampleAt = &now, last
	e.Requests, e.ServerErrors, e.ErrorRatePct = requests, errors, 0
	if requests > 0 && errors >= 0 && errors <= requests {
		e.ErrorRatePct = float64(errors) / float64(requests) * 100
	}
	finish := func(status, evidenceStatus, code string) (api.AlertRollback, bool) {
		e.Status, e.Code = evidenceStatus, code
		return alertRollbackProgress(r, status, code, nil), false
	}
	if !historicalAlertEvidenceSupported(e) {
		return finish("failed", "unsupported", "alert_rollback_metric_unsupported")
	}
	if now.Before(r.FiredAt) || !now.Before(r.FiredAt.Add(api.AlertRollbackEvidenceMaxCheckDelay)) {
		return finish("failed", "expired", "alert_rollback_evidence_expired")
	}
	if !available {
		return finish("blocked", "unavailable", "alert_rollback_telemetry_unavailable")
	}
	if !e.WindowStart.Before(e.WindowEnd) || requests < api.AlertRollbackEvidenceMinRequests || errors < 0 || errors > requests {
		return finish("blocked", "insufficient", "alert_rollback_evidence_insufficient")
	}
	if last == nil || last.Before(e.WindowStart) || !last.Before(e.WindowEnd) || last.Before(e.WindowEnd.Add(-api.AlertRollbackEvidenceMaxSampleAge)) {
		return finish("blocked", "stale", "alert_rollback_evidence_stale")
	}
	breached := errors > 0 && (e.ErrorRatePct > e.Threshold || e.Comparison == string(AlertGte) && e.ErrorRatePct == e.Threshold)
	if !breached {
		return finish("failed", "healthy", "alert_rollback_deployment_healthy")
	}
	e.Status, e.Code = "breached", ""
	return alertRollbackProgress(r, "pending", "", nil), true
}
