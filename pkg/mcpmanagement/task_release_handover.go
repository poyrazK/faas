package mcpmanagement

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func taskHandoverOutputSchema() map[string]any {
	id := map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}
	date := map[string]any{"type": "string", "format": "date-time"}
	revision := map[string]any{"type": "integer", "minimum": 1, "maximum": api.TaskToolReleaseReceiptRevisionMax}
	q := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"handover_id", "operation_id", "revision", "action", "target_deployment_id", "expected_serving_deployment_id", "bindings_sha256", "evidence_sha256", "native_apps", "observed_at", "expires_at"}, "properties": map[string]any{
		"handover_id": id, "operation_id": id, "revision": revision, "action": map[string]any{"type": "string", "const": "restore"},
		"target_deployment_id": map[string]any{"type": "string", "format": "uuid"}, "expected_serving_deployment_id": map[string]any{"type": "string", "format": "uuid"},
		"bindings_sha256": id, "evidence_sha256": id, "observed_at": date, "expires_at": date,
		"native_apps": map[string]any{"type": "array", "minItems": 3, "maxItems": api.TaskToolReleaseAppsMax, "items": map[string]any{"type": "object"}},
	}}
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "operation_id", "revision", "state", "requested_at"}, "properties": map[string]any{
		"id": id, "operation_id": id, "revision": revision, "state": map[string]any{"type": "string", "enum": []string{"stopping", "settled"}}, "requested_at": date, "settled_at": date,
		"qualification": q, "decision": map[string]any{"type": "string", "enum": []string{"approved", "revoked"}}, "authorizer_key_id": map[string]any{"type": "string", "format": "uuid"}, "decided_at": date,
	}}
}

// Metadata validation never treats an old qualification or verdict as live authority.
func validTaskHandoverMetadata(e *api.TaskToolReleaseExecution) bool {
	if e.FenceVersion != 0 && e.FenceVersion != 1 {
		return false
	}
	if e.ExecutorExitedAt != nil && (e.FenceVersion != 1 || e.ExecutorExitedAt.Before(e.AuthorizedAt) || e.State == "authorized" || e.State == "running") {
		return false
	}
	h := e.Recovery
	if h == nil {
		return true
	}
	if e.FenceVersion != 1 || e.Receipt == nil || !toolpublication.ValidID(h.ID) || h.OperationID != e.OperationID || h.Revision != e.Receipt.Revision || h.RequestedAt.Before(e.AuthorizedAt) {
		return false
	}
	if h.State == "stopping" {
		return h.SettledAt == nil && e.ExecutorExitedAt == nil && h.Qualification == nil && h.Decision == ""
	}
	if h.State != "settled" || h.SettledAt == nil || e.ExecutorExitedAt == nil || h.SettledAt.Before(h.RequestedAt) || h.SettledAt.Before(*e.ExecutorExitedAt) {
		return false
	}
	if h.Decision != "" && (h.Decision != "approved" && h.Decision != "revoked" || uuid.Validate(h.AuthorizerKeyID) != nil || h.DecidedAt == nil) {
		return false
	}
	if h.Qualification == nil {
		return h.Decision == ""
	}
	q := h.Qualification
	return q.HandoverID == h.ID && q.OperationID == e.OperationID && q.Revision == h.Revision && q.Action == "restore" &&
		q.BindingsSHA256 == e.BindingsSHA256 && toolpublication.ValidID(q.EvidenceSHA256) && q.TargetDeploymentID != "" && q.TargetDeploymentID == e.Receipt.ServingDeploymentID &&
		q.ExpectedServingDeploymentID != "" && q.ExpectedServingDeploymentID == e.Receipt.WebDeploymentID &&
		!q.ObservedAt.Before(*h.SettledAt) && q.ExpiresAt.Equal(q.ObservedAt.Add(api.TaskToolRecoveryApprovalTTL))
}
