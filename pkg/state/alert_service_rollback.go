// adr: 603 — alert fires address the existing exact service abort handoff.
package state

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func queueAlertServiceRollback(r api.AlertRollback, target Deployment) (api.AlertRollback, Deployment, error) {
	if target.ServiceRolloutHandoff.ActiveAbort() {
		return r, target, ErrAlertRollbackChanged
	}
	target = queueServiceBindingCheck(target, ServiceRolloutActionAbort, r.PredecessorDeploymentID, r.PredecessorDeploymentID, r.Reason)
	target.ServiceRolloutHandoff.BindingsCheck.RequestID = r.ID
	r = alertRollbackProgress(r, "pending", "", nil)
	r.ServiceRequestID, r.ServicePhase = r.ID, ServiceRolloutPhasePending
	return r, target, nil
}

func alertServiceRollbackAudit(ctx context.Context, r api.AlertRollback, phase string) DeploymentAudit {
	audit := alertRollbackAudit(ctx, r)
	var fields map[string]any
	_ = json.Unmarshal(audit.Data, &fields)
	fields["service_request_id"], fields["service_phase"] = r.ServiceRequestID, phase
	if r.ServiceRoutingAuditID != "" {
		fields["service_routing_audit_id"] = r.ServiceRoutingAuditID
	}
	audit.Actor = "apid:alert_service_rollback"
	audit.Data, _ = json.Marshal(fields)
	return audit
}

// Refresh only projects the existing handoff. It never moves traffic, selects
// another predecessor, or asks for fresh evidence after routing has committed.
func projectAlertServiceRollback(r api.AlertRollback, candidate, predecessor Deployment) api.AlertRollback {
	h := candidate.ServiceRolloutHandoff
	gate := h.BindingsCheck
	validPair := candidate.AppID == r.AppID && predecessor.AppID == r.AppID && normalizedDeploymentScope(candidate.Scope) == r.Scope && normalizedDeploymentScope(predecessor.Scope) == r.Scope
	if !validPair || predecessor.Status != DeployLive || gate == nil || gate.RequestID != r.ServiceRequestID || gate.Action != ServiceRolloutActionAbort || gate.DeploymentID != r.PredecessorDeploymentID || h.Action != ServiceRolloutActionAbort || h.PredecessorDeploymentID != r.PredecessorDeploymentID {
		return alertRollbackProgress(r, "failed", "alert_rollback_deployment_changed", nil)
	}
	r.ServicePhase, r.ServiceRoutingAuditID = h.Phase, gate.AuditID
	if h.Phase == ServiceRolloutPhasePending && candidate.Status == DeployLive && IsServiceRollout(candidate) {
		if gate.Status == "blocked" {
			return alertRollbackProgress(r, "blocked", gate.Code, gate.Blockers)
		}
		return alertRollbackProgress(r, "pending", "", nil)
	}
	if gate.Status != "passed" || gate.AuditID == "" || candidate.TrafficPercent != 0 || predecessor.TrafficPercent != 100 {
		return alertRollbackProgress(r, "failed", "alert_rollback_deployment_changed", nil)
	}
	switch h.Phase {
	case ServiceRolloutPhaseRouting, ServiceRolloutPhaseDraining:
		if candidate.Status == DeployLive && IsServiceRollout(candidate) {
			return alertRollbackProgress(r, "pending", h.LastError, nil)
		}
	case ServiceRolloutPhaseComplete:
		if candidate.Status == DeploySuperseded && candidate.RolloutState == "aborted" && h.CompletedAt != nil && h.AcknowledgedAt != nil && len(h.MissingGateways) == 0 {
			return alertRollbackProgress(r, "complete", "", nil)
		}
	}
	return alertRollbackProgress(r, "failed", "alert_rollback_handoff_incomplete", nil)
}

func finishAlertServiceRollback(r api.AlertRollback, auditID int64) api.AlertRollback {
	r.AuditID = strconv.FormatInt(auditID, 10)
	now := time.Now().UTC()
	r.CompletedAt, r.UpdatedAt = &now, now
	return r
}
