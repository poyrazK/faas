package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
)

var ErrServiceRolloutNotReady = errors.New("service rollout recipient is not ready")

type ServiceRolloutBindingStore interface {
	UpdateServiceRolloutBindingStatus(context.Context, string, string, string, []api.BindingCheckFinding) error
	RequestServiceRolloutAbort(context.Context, string, string, string, string) (Deployment, int64, error)
}

type serviceBindingRequestKey struct{}

// Only the APID worker supplies this exact durable request selection.
func WithServiceRolloutBindingRequest(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, serviceBindingRequestKey{}, requestID)
}

func serviceBindingRequestMatches(ctx context.Context, d Deployment, action, recipientID, predecessorID string) error {
	if d.ServiceRolloutHandoff.PredecessorDeploymentID != "" && predecessorID == "" {
		return ErrServiceRolloutInvalid
	}
	requestID, _ := ctx.Value(serviceBindingRequestKey{}).(string)
	gate := d.ServiceRolloutHandoff.BindingsCheck
	if requestID != "" && (gate == nil || gate.RequestID != requestID || gate.Action != action || gate.DeploymentID != recipientID || d.ServiceRolloutHandoff.PredecessorDeploymentID != predecessorID || gate.Status == "passed") {
		return ErrServiceRolloutInvalid
	}
	if gate != nil && gate.Status != "passed" && gate.Action == action && d.ServiceRolloutHandoff.PredecessorDeploymentID != predecessorID {
		return ErrServiceRolloutInvalid
	}
	return nil
}

func queueServiceBindingCheck(d Deployment, action, recipientID, predecessorID, reason string) Deployment {
	h := d.ServiceRolloutHandoff
	if h.Action != action {
		h.Generation, h.AcknowledgedAt, h.CompletedAt = 0, nil, nil
		h.ExpectedGateways, h.AcknowledgedGateways, h.MissingGateways = nil, nil, nil
	}
	gate := h.BindingsCheck
	if gate == nil || gate.Action != action || gate.DeploymentID != recipientID || h.PredecessorDeploymentID != predecessorID || gate.Status == "passed" {
		gate = &api.ServiceRolloutBindingGate{RequestID: uuid.NewString(), Action: action, DeploymentID: recipientID, Status: "pending"}
	}
	now := time.Now().UTC()
	h.Action, h.Phase, h.PredecessorDeploymentID = action, ServiceRolloutPhasePending, predecessorID
	h.BindingsCheck, h.UpdatedAt, h.LastError = gate, &now, api.CodeBindingReleaseRequired
	if h.StartedAt == nil {
		h.StartedAt = &now
	}
	if reason != "" {
		h.Reason = normalizeRolloutReason(reason)
	}
	d.ServiceRolloutHandoff = h
	return d
}

func serviceBindingAudit(ctx context.Context, d Deployment) []byte {
	payload, _ := json.Marshal(map[string]any{
		"action":                    d.ServiceRolloutHandoff.Action,
		"request_id":                d.ServiceRolloutHandoff.BindingsCheck.RequestID,
		"deployment_id":             d.ServiceRolloutHandoff.BindingsCheck.DeploymentID,
		"predecessor_deployment_id": d.ServiceRolloutHandoff.PredecessorDeploymentID,
		"binding_fences":            bindingReleaseFences(ctx),
	})
	return payload
}

func serviceBindingIntentAudit(d Deployment, reason string) []byte {
	payload, _ := json.Marshal(map[string]string{
		"action": "abort_requested", "reason": normalizeRolloutReason(reason),
		"deployment_id":             d.ID,
		"predecessor_deployment_id": d.ServiceRolloutHandoff.PredecessorDeploymentID,
		"request_id":                d.ServiceRolloutHandoff.BindingsCheck.RequestID,
	})
	return payload
}

func passedServiceBindingGate(ctx context.Context, d Deployment, auditID string) *api.ServiceRolloutBindingGate {
	if requestID, _ := ctx.Value(serviceBindingRequestKey{}).(string); requestID == "" {
		return d.ServiceRolloutHandoff.BindingsCheck
	}
	gate := *d.ServiceRolloutHandoff.BindingsCheck
	now := time.Now().UTC()
	gate.Status, gate.Code, gate.Blockers, gate.CheckedAt, gate.AuditID = "passed", "", nil, &now, auditID
	if fences := bindingReleaseFences(ctx); len(fences) > 0 {
		gate.PolicyRevision = fences[0].PolicyRevision
	}
	return &gate
}

func blockedServiceBindingGate(gate *api.ServiceRolloutBindingGate, code string, blockers []api.BindingCheckFinding) *api.ServiceRolloutBindingGate {
	updated := *gate
	now := time.Now().UTC()
	updated.Status, updated.Code, updated.CheckedAt = "blocked", safetext.Truncate(code, 80), &now
	updated.Blockers = nil
	for _, finding := range blockers {
		if len(updated.Blockers) == 4 {
			break
		}
		finding.Message = safetext.Truncate(finding.Message, 256)
		finding.Code, finding.Type = safetext.Truncate(finding.Code, 80), safetext.Truncate(finding.Type, 32)
		finding.Scope, finding.DeploymentID = safetext.Truncate(finding.Scope, 64), safetext.Truncate(finding.DeploymentID, 64)
		finding.Name, finding.Binding = safetext.Truncate(finding.Name, 128), safetext.Truncate(finding.Binding, 128)
		updated.Blockers = append(updated.Blockers, finding)
	}
	return &updated
}
