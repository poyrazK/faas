// adr: 604 — alerts link exact completed-release intent to checked rollback.
package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type HistoricalAlertRollbackStore interface {
	RefreshHistoricalAlertRollback(context.Context, api.AlertRollback) (api.AlertRollback, error)
}

func validAlertRollbackWindow(r AlertRule) bool {
	if !validEventConsumerAlertRule(r) {
		return false
	}
	n := r.PostDeployRollbackWindowSeconds
	return n >= 0 && n <= api.AlertRollbackMaxWindowSeconds && (n == 0 || r.Action == AlertActionRollback && r.AppID != "")
}
func completedAlertDeployment(d alertRollbackDeployment) bool {
	return d.Status == DeployLive && d.TrafficPercent == 100 && d.RolloutState == "complete" && (d.CanaryTotalSteps == 0 || d.CanaryStep >= d.CanaryTotalSteps)
}
func captureHistoricalAlertRollback(r api.AlertRollback, f alertRollbackFacts) api.AlertRollback {
	fail := func(code string) api.AlertRollback { return alertRollbackProgress(r, "failed", code, nil) }
	if f.WindowSeconds <= 0 || f.WindowSeconds > api.AlertRollbackMaxWindowSeconds {
		return fail("alert_rollback_target_unavailable")
	}
	var current alertRollbackDeployment
	for _, d := range f.Deployments {
		if d.AppID != r.AppID || !completedAlertDeployment(d) {
			continue
		}
		if current.ID != "" {
			return fail("alert_rollback_target_ambiguous")
		}
		current = d
	}
	if current.ID == "" {
		return fail("alert_rollback_target_unavailable")
	}
	r.Historical, r.CandidateDeploymentID, r.Scope = true, current.ID, normalizedDeploymentScope(current.Scope)
	if current.Recovered {
		return fail("alert_rollback_chain_prevented")
	}
	if current.CompletedAt == nil || r.FiredAt.Before(*current.CompletedAt) || !r.FiredAt.Before(current.CompletedAt.Add(time.Duration(f.WindowSeconds)*time.Second)) {
		return fail("alert_rollback_window_expired")
	}
	r.DeploymentEvidence = newHistoricalAlertEvidence(r, f, *current.CompletedAt)
	if current.RecoveryPredecessorID == "" {
		return fail("alert_rollback_lineage_missing")
	}
	for _, d := range f.Deployments {
		if d.AppID != r.AppID || normalizedDeploymentScope(d.Scope) != r.Scope || d.ID == current.ID {
			continue
		}
		if d.Status == DeployLive && (d.TrafficPercent > 0 || alertRollbackActive(d) || d.CanaryTotalSteps == 0 && d.RolloutState == "rolling_out") {
			return fail("alert_rollback_target_ambiguous")
		}
		if d.ID != current.RecoveryPredecessorID {
			continue
		}
		if (d.Status != DeploySuperseded && (d.Status != DeployLive || d.TrafficPercent != 0)) || !d.CreatedAt.Before(current.CreatedAt) || d.RolloutState != "complete" {
			return fail("alert_rollback_target_unavailable")
		}
		r.PredecessorDeploymentID = d.ID
	}
	if r.PredecessorDeploymentID == "" {
		return fail("alert_rollback_target_unavailable")
	}
	return r
}
func historicalAlertWindowOpen(r api.AlertRollback, f alertRollbackFacts, at time.Time) bool {
	for _, d := range f.Deployments {
		if d.ID == r.CandidateDeploymentID && d.CompletedAt != nil {
			return f.WindowSeconds > 0 && !at.Before(*d.CompletedAt) && at.Before(d.CompletedAt.Add(time.Duration(f.WindowSeconds)*time.Second))
		}
	}
	return false
}
func historicalAlertAudit(ctx context.Context, r api.AlertRollback, phase string) DeploymentAudit {
	a := alertRollbackAudit(ctx, r)
	var fields map[string]any
	_ = json.Unmarshal(a.Data, &fields)
	fields["rollback_operation_id"], fields["rollback_phase"] = r.RollbackOperationID, phase
	fields["rollback_routing_audit_id"] = r.RollbackRoutingAuditID
	fields["deployment_evidence"] = r.DeploymentEvidence
	a.Actor, a.Data = "apid:alert_historical_rollback", nil
	a.Data, _ = json.Marshal(fields)
	return a
}
func projectHistoricalAlertRollback(r api.AlertRollback, op api.RollbackOperation, candidate, target Deployment) api.AlertRollback {
	if op.ID != r.ID || op.ID != r.RollbackOperationID || op.AppID != r.AppID || op.Scope != r.Scope || op.CurrentDeploymentID != r.CandidateDeploymentID || op.TargetDeploymentID != r.PredecessorDeploymentID || op.Service != r.Service {
		return alertRollbackProgress(r, "failed", "alert_rollback_deployment_changed", nil)
	}
	r.RollbackPhase, r.RollbackRoutingAuditID = op.Status, op.AuditID
	if op.Status == "failed" {
		return alertRollbackProgress(r, "failed", op.Code, op.Blockers)
	}
	if op.Status != "complete" {
		if op.Status == "blocked" {
			return alertRollbackProgress(r, "blocked", op.Code, op.Blockers)
		}
		return alertRollbackProgress(r, "pending", op.Code, op.Blockers)
	}
	if op.AuditID == "" || op.CompletedAt == nil || candidate.AppID != r.AppID || target.AppID != r.AppID || candidate.TrafficPercent != 0 || target.Status != DeployLive || target.TrafficPercent != 100 || target.RolloutState != "complete" {
		return alertRollbackProgress(r, "failed", "alert_rollback_handoff_incomplete", nil)
	}
	if r.Service {
		h := target.ServiceRolloutHandoff
		if h.Action != ServiceRolloutActionPromote || h.Phase != ServiceRolloutPhaseComplete || h.PredecessorDeploymentID != r.CandidateDeploymentID || h.AcknowledgedAt == nil || h.CompletedAt == nil || len(h.MissingGateways) > 0 || h.BindingsCheck == nil || h.BindingsCheck.RequestID != op.ID || h.BindingsCheck.AuditID != op.AuditID {
			return alertRollbackProgress(r, "failed", "alert_rollback_handoff_incomplete", nil)
		}
	}
	return alertRollbackProgress(r, "complete", "", nil)
}
