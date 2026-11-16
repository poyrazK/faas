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
	if err := m.taskInspectionAccess(); err != nil {
		return nil, err
	}
	out, err := tasktoolrelease.InspectRecovery(ctx, m.client, args.IntentID, plan, time.Now())
	if errors.Is(err, tasktoolrelease.ErrInvalid) {
		return nil, invalid("invalid_response", "native Task recovery metadata is incompatible")
	}
	if err == nil && m.remote() && out.AccountID != m.opts.accountID {
		return nil, invalid("invalid_response", "native Task recovery metadata belongs to another account")
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
		"lifecycle":                    taskRecoveryLifecycleSchema(),
		"next_step":                    taskRecoveryNextStepSchema(),
		"checks":                       map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"name", "state", "code"}, "properties": map[string]any{"name": map[string]any{"type": "string"}, "state": map[string]any{"type": "string", "enum": []string{"passed", "failed", "unknown"}}, "code": map[string]any{"type": "string"}}}},
		"deployments":                  map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
		"actions": map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"action", "eligibility", "blockers", "required_owner_checks"}, "properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"resume", "restore", "quarantine", "retire"}}, "eligibility": map[string]any{"type": "string", "enum": []string{"blocked", "requires_owner_checks"}},
			"blockers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "required_owner_checks": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}}},
		"completed_stages": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	}
	for _, name := range []string{"intent_id", "account_id", "operation_id", "intent_status", "execution_state", "classification", "observed_at", "expires_at", "evidence_sha256", "recovery_plan_id"} {
		props[name] = map[string]any{"type": "string"}
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": []string{"version", "kind", "intent_id", "account_id", "intent_status", "execution_state", "classification", "completed_stages", "observed_at", "expires_at", "checks", "deployments", "evidence_sha256", "read_only", "ready_to_apply", "owner_authorization_required"}}
}

func taskRecoveryLifecycleSchema() map[string]any {
	state := func(values ...string) map[string]any { return map[string]any{"type": "string", "enum": values} }
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"executor_fenced", "handover_state", "restore", "quarantine", "retirement"}, "properties": map[string]any{
		"executor_fenced": map[string]any{"type": "boolean", "const": true}, "handover_id": map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}, "handover_state": state("not_requested", "stopping", "settled"),
		"restore": state("not_qualified", "qualified", "approved", "approval_expired", "revoked", "restored"), "quarantine": state("not_qualified", "qualified", "approved", "approval_expired", "revoked", "applying", "quarantined"), "retirement": state("not_qualified", "qualified", "approved", "approval_expired", "revoked", "retirement_pending", "retired"),
	}}
}
func taskRecoveryNextStepSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"action", "requires_original_bindings", "blockers"}, "properties": map[string]any{
		"action":                     map[string]any{"type": "string", "enum": []string{"inspect_again", "request_handover", "observe_handover", "qualify_restore", "approve_restore", "apply_restore", "qualify_quarantine", "approve_quarantine", "apply_quarantine", "observe_quarantine", "qualify_retirement", "approve_retirement", "apply_retirement", "observe_retirement", "owner_reconciliation", "prepare_new_release"}},
		"requires_original_bindings": map[string]any{"type": "boolean"}, "blockers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	}}
}
