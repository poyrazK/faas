package mcpmanagement

import "github.com/onebox-faas/faas/pkg/api"

func taskRetirementOutputSchema(base map[string]any) map[string]any {
	id := map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}
	date := map[string]any{"type": "string", "format": "date-time"}
	uuid := map[string]any{"type": "string", "format": "uuid"}
	props := map[string]any{}
	for k, v := range base["properties"].(map[string]any) {
		props[k] = v
	}
	props["action"] = map[string]any{"type": "string", "const": "retire"}
	qualification := map[string]any{}
	for k, v := range base {
		qualification[k] = v
	}
	qualification["properties"] = props
	resultProps := map[string]any{"operation_id": id, "handover_id": id, "qualification_sha256": id, "park_transition_id": uuid, "worker_deployment_id": uuid, "serving_deployment_id": uuid, "evidence_sha256": id, "observed_at": date, "recorded_at": date, "worker_ids": map[string]any{"type": "array", "minItems": 1, "maxItems": api.TaskToolReleaseReceiptWorkerIDsMax, "uniqueItems": true, "items": uuid}}
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"qualification", "state"}, "properties": map[string]any{"qualification": qualification, "state": map[string]any{"type": "string", "enum": []string{"qualified", "retirement_pending", "retired"}}, "decision": map[string]any{"type": "string", "enum": []string{"approved", "revoked"}}, "authorizer_key_id": uuid, "decided_at": date, "park_transition_id": uuid, "park_requested_at": date, "result": map[string]any{"type": "object", "additionalProperties": false, "properties": resultProps, "required": []string{"operation_id", "handover_id", "qualification_sha256", "park_transition_id", "worker_deployment_id", "serving_deployment_id", "worker_ids", "evidence_sha256", "observed_at", "recorded_at"}}}}
}
