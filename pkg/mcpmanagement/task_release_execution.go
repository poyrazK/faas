package mcpmanagement

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type applyTaskExecutionArgs struct {
	IntentID           string `json:"intent_id"`
	OperationID        string `json:"operation_id" jsonschema:"Exact separately owner-authorized Task execution identity"`
	AllowToolExecution bool   `json:"allow_tool_execution" jsonschema:"Explicit consent to the reviewed code, database migrations and native release effects"`
}

func taskExecutionOutputSchema() map[string]any {
	props := map[string]any{
		"authorizer_key_id": map[string]any{"type": "string", "format": "uuid"},
		"authorized_at":     map[string]any{"type": "string", "format": "date-time"},
		"state":             map[string]any{"type": "string", "enum": []string{"authorized", "running", "reconcile_required", "completed"}},
		"stage":             map[string]any{"type": "string", "enum": []string{"prepared", "deploying_worker", "deploying_web", "replacements_started", "web_promoted", "draining_previous", "previous_parked", "complete"}},
	}
	for _, name := range []string{"operation_id", "bindings_sha256", "claim_sha256"} {
		props[name] = map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}
	}
	props["receipt"] = taskReleaseReceiptSchema()
	props["fence_version"] = map[string]any{"type": "integer", "const": 1}
	props["executor_exited_at"] = map[string]any{"type": "string", "format": "date-time"}
	props["recovery"] = taskHandoverOutputSchema()
	props["completed_stages"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"operation_id", "bindings_sha256", "authorizer_key_id", "state", "authorized_at"}, "properties": props}
}

func taskReleaseReceiptSchema() map[string]any {
	props := map[string]any{
		"revision":         map[string]any{"type": "integer", "minimum": 1, "maximum": api.TaskToolReleaseReceiptRevisionMax},
		"release_id":       map[string]any{"type": "string", "pattern": "^[0-9a-f]{32}$"},
		"stage":            map[string]any{"type": "string", "enum": []string{"prepared", "deploying_worker", "deploying_web", "replacements_started", "web_promoted", "draining_previous", "previous_parked", "complete"}},
		"serving_captured": map[string]any{"type": "boolean"}, "promoted": map[string]any{"type": "boolean"},
		"previous_deployments": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string", "format": "uuid"}},
		"parked":               map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "boolean", "const": true}},
		"worker_ids":           map[string]any{"type": "array", "maxItems": api.TaskToolReleaseReceiptWorkerIDsMax, "uniqueItems": true, "items": map[string]any{"type": "string", "format": "uuid"}},
		"pending_submission":   map[string]any{"type": "string", "enum": []string{"web", "worker"}},
	}
	for _, name := range []string{"serving_deployment_id", "web_deployment_id", "worker_deployment_id"} {
		props[name] = map[string]any{"type": "string", "format": "uuid"}
	}
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"revision", "release_id", "stage", "serving_captured", "previous_deployments", "worker_ids", "promoted", "parked"}, "properties": props}
}

func (m *management) getTaskExecution(ctx context.Context, args taskReleaseIntentArgs) (any, error) {
	return m.getTaskReleaseIntent(ctx, args)
}

func (m *management) applyTaskExecution(ctx context.Context, args applyTaskExecutionArgs) (any, error) {
	if err := m.taskIntentAccess(); err != nil {
		return nil, err
	}
	if !m.opts.AllowWrites || !args.AllowToolExecution || !toolpublication.ValidID(args.IntentID) || !toolpublication.ValidID(args.OperationID) {
		return nil, invalid("invalid_argument", "Task apply requires write consent, explicit execution consent and exact intent/operation identities")
	}
	retained, err := m.client.GetTaskToolReleaseIntent(ctx, args.IntentID)
	if err != nil {
		return nil, err
	}
	if _, err = validateTaskIntentReceipt(retained, args.IntentID); err != nil {
		return nil, err
	}
	if retained.Execution == nil || retained.Execution.OperationID != args.OperationID {
		return nil, invalid("execution_not_authorized", "a separate matching owner execution authorization is required")
	}
	if retained.Execution.State != "authorized" {
		return retained, nil
	}
	if retained.Status != "approved" || !time.Now().Before(retained.ExpiresAt) {
		return nil, invalid("execution_not_authorized", "the owner-approved review has expired or changed")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.taskExecutions[args.IntentID] {
		return retained, nil
	}
	select {
	case toolPublishSlots <- struct{}{}:
	default:
		return nil, invalid("publication_capacity", "release workers are busy; inspect and retry the same unstarted operation")
	}
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.TaskToolReleaseApprovalTTL)
	work, finish, err := m.lifecycle.enter(work, false)
	if err != nil {
		cancel()
		<-toolPublishSlots
		return nil, err
	}
	work, finishGrant, err := m.grantLifecycle.enter(work, false)
	if err != nil {
		finish()
		cancel()
		<-toolPublishSlots
		return nil, err
	}
	if m.taskExecutions == nil {
		m.taskExecutions = map[string]bool{}
	}
	m.taskExecutions[args.IntentID] = true
	go func() {
		defer cancel()
		defer finish()
		defer finishGrant()
		defer func() { <-toolPublishSlots; m.mu.Lock(); delete(m.taskExecutions, args.IntentID); m.mu.Unlock() }()
		// CLI and child output may contain local diagnostics. Poll only the
		// native journal; never forward failed-command output or rerun it.
		_, _ = m.runCLI(work, []string{"mcp", "server", "tasks", "execution", "apply", "--id", args.IntentID, "--operation-id", args.OperationID, "--allow-tool-execution"}, nil)
	}()
	return retained, nil
}
