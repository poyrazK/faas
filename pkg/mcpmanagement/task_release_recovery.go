package mcpmanagement

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func (m *management) inspectTaskRecovery(ctx context.Context, args taskReleaseIntentArgs) (any, error) {
	return m.taskRecovery(ctx, args, false)
}
func (m *management) planTaskRecovery(ctx context.Context, args taskReleaseIntentArgs) (any, error) {
	return m.taskRecovery(ctx, args, true)
}

func (m *management) taskRecovery(ctx context.Context, args taskReleaseIntentArgs, plan bool) (any, error) {
	if err := m.taskIntentAccess(); err != nil {
		return nil, err
	}
	out, err := tasktoolrelease.InspectRecovery(ctx, m.client, args.IntentID, plan, time.Now())
	if errors.Is(err, tasktoolrelease.ErrInvalid) {
		return nil, invalid("invalid_response", "native Task recovery metadata is incompatible")
	}
	return out, err
}

func taskRecoveryOutputSchema() map[string]any {
	props := map[string]any{
		"version":                      map[string]any{"type": "integer", "const": 1},
		"kind":                         map[string]any{"type": "string", "enum": []string{"inspection", "recovery_plan"}},
		"read_only":                    map[string]any{"type": "boolean", "const": true},
		"ready_to_apply":               map[string]any{"type": "boolean", "const": false},
		"owner_authorization_required": map[string]any{"type": "boolean", "const": true},
		"receipt":                      taskReleaseReceiptSchema(),
		"checks":                       map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"name", "state", "code"}, "properties": map[string]any{"name": map[string]any{"type": "string"}, "state": map[string]any{"type": "string", "enum": []string{"passed", "failed", "unknown"}}, "code": map[string]any{"type": "string"}}}},
		"deployments":                  map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
		"actions": map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"action", "eligibility", "blockers", "required_owner_checks"}, "properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"resume", "restore", "retire"}}, "eligibility": map[string]any{"type": "string", "enum": []string{"blocked", "requires_owner_checks"}},
			"blockers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "required_owner_checks": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}}},
		"completed_stages": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	}
	for _, name := range []string{"intent_id", "account_id", "operation_id", "intent_status", "execution_state", "classification", "observed_at", "expires_at", "evidence_sha256", "recovery_plan_id"} {
		props[name] = map[string]any{"type": "string"}
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": []string{"version", "kind", "intent_id", "account_id", "intent_status", "execution_state", "classification", "completed_stages", "observed_at", "expires_at", "checks", "deployments", "evidence_sha256", "read_only", "ready_to_apply", "owner_authorization_required"}}
}
