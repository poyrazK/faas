package mcpmanagement

import (
	"github.com/onebox-faas/faas/pkg/api"
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
		"qualification": q, "quarantine": taskQuarantineOutputSchema(q), "retirement": taskRetirementOutputSchema(q), "result": taskRecoveryResultSchema(), "decision": map[string]any{"type": "string", "enum": []string{"approved", "revoked"}}, "authorizer_key_id": map[string]any{"type": "string", "format": "uuid"}, "decided_at": date,
	}}
}

func taskRecoveryResultSchema() map[string]any {
	props := map[string]any{"revision": map[string]any{"type": "integer", "minimum": 1, "maximum": api.TaskToolReleaseReceiptRevisionMax}, "action": map[string]any{"type": "string", "const": "restore"}}
	for _, name := range []string{"intent_id", "operation_id", "handover_id", "qualification_sha256", "evidence_sha256"} {
		props[name] = map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}
	}
	for _, name := range []string{"target_deployment_id", "previous_serving_deployment_id"} {
		props[name] = map[string]any{"type": "string", "format": "uuid"}
	}
	for _, name := range []string{"checked_at", "committed_at"} {
		props[name] = map[string]any{"type": "string", "format": "date-time"}
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": []string{"intent_id", "operation_id", "handover_id", "qualification_sha256", "revision", "action", "target_deployment_id", "previous_serving_deployment_id", "evidence_sha256", "checked_at", "committed_at"}}
}
