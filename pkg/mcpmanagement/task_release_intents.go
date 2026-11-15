package mcpmanagement

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type taskReleaseIntentArgs struct {
	IntentID string `json:"intent_id" jsonschema:"Stable caller-chosen 64 lowercase hex characters; reuse it after ambiguous errors"`
}

type prepareTaskReleaseArgs struct {
	IntentID string `json:"intent_id" jsonschema:"Stable caller-chosen 64 lowercase hex characters; reuse it after ambiguous errors"`
	PlanPath string `json:"plan_path" jsonschema:"Absolute local native Task stack release plan path"`
	Reason   string `json:"reason" jsonschema:"Reviewable purpose of this intent, at most 280 characters"`
}

type checkTaskReleaseArgs struct {
	IntentID string `json:"intent_id"`
	PlanPath string `json:"plan_path" jsonschema:"Absolute local native Task stack release plan path to re-review"`
}

func (m *management) taskIntentAccess() error {
	if err := m.toolBuildingAccess(); err != nil {
		return err
	}
	if m.remote() {
		return invalid("tool_build_access_denied", "Task intent review requires a local account connection")
	}
	return nil
}

func (m *management) getTaskReleaseIntent(ctx context.Context, args taskReleaseIntentArgs) (any, error) {
	if err := m.taskIntentAccess(); err != nil {
		return nil, err
	}
	if !toolpublication.ValidID(args.IntentID) {
		return nil, invalid("invalid_argument", "intent_id must be 64 lowercase hex characters")
	}
	out, err := m.client.GetTaskToolReleaseIntent(ctx, args.IntentID)
	if err != nil {
		return nil, err
	}
	return validateTaskIntentReceipt(out, args.IntentID)
}

func (m *management) prepareTaskReleaseIntent(ctx context.Context, args prepareTaskReleaseArgs) (any, error) {
	if err := validateReason("task-review", args.Reason); err != nil {
		return nil, err
	}
	return m.runTaskIntentReview(ctx, args.IntentID, args.PlanPath, "prepare", []string{"--reason", args.Reason})
}

func (m *management) checkTaskReleaseIntent(ctx context.Context, args checkTaskReleaseArgs) (any, error) {
	return m.runTaskIntentReview(ctx, args.IntentID, args.PlanPath, "get", nil)
}

func (m *management) runTaskIntentReview(ctx context.Context, id, path, action string, extra []string) (any, error) {
	if err := m.taskIntentAccess(); err != nil {
		return nil, err
	}
	if !m.opts.AllowWrites || !toolpublication.ValidID(id) || !filepath.IsAbs(path) {
		return nil, invalid("invalid_argument", "journal writes require write consent, a stable intent_id and an absolute plan_path")
	}
	args := []string{"mcp", "server", "tasks", "intent", action, "--id", id, "--plan", filepath.Clean(path)}
	body, err := m.runCLI(ctx, append(args, extra...), nil)
	if err != nil {
		return nil, invalid("task_intent_failed", "local Task intent review failed; inspect it with the local CLI")
	}
	var record api.TaskToolReleaseIntentRecord
	if len(body) > 2*api.TaskToolReleaseIntentMaxBytes || json.Unmarshal(body, &record) != nil {
		return nil, invalid("invalid_response", "local CLI returned no compatible Task review record")
	}
	return validateTaskIntentReceipt(record, id)
}

func validateTaskIntentReceipt(record api.TaskToolReleaseIntentRecord, id string) (api.TaskToolReleaseIntentRecord, error) {
	expected, err := tasktoolrelease.New(record.AccountID, record.RequesterKeyID, id, record.Intent, record.CreatedAt)
	if err != nil || record.Version != 1 || record.ID != id || record.IntentSHA256 != expected.IntentSHA256 ||
		record.ReadyToApply || record.BindingsVerified || record.CreatedAt.IsZero() || !record.ExpiresAt.Equal(expected.ExpiresAt) {
		return api.TaskToolReleaseIntentRecord{}, invalid("invalid_response", "Task intent receipt differs from its reviewed identity")
	}
	if e := record.Execution; e != nil {
		if !toolpublication.ValidID(e.OperationID) || !toolpublication.ValidID(e.BindingsSHA256) || e.AuthorizerKeyID == "" || e.AuthorizedAt.Before(record.CreatedAt) || !e.AuthorizedAt.Before(record.ExpiresAt) ||
			(e.State != "authorized" && e.State != "running" && e.State != "reconcile_required" && e.State != "completed") || e.ClaimSHA256 != "" && !toolpublication.ValidID(e.ClaimSHA256) {
			return api.TaskToolReleaseIntentRecord{}, invalid("invalid_response", "invalid Task execution receipt")
		}
	}
	switch record.Status {
	case "pending", "approved", "rejected", "revoked", "expired", "stale":
		return tasktoolrelease.View(record, time.Now()), nil
	default:
		return api.TaskToolReleaseIntentRecord{}, invalid("invalid_response", "unknown Task intent status")
	}
}

func taskIntentOutputSchema() map[string]any {
	props := map[string]any{"version": map[string]any{"type": "integer", "const": 1}, "intent": map[string]any{"type": "object"},
		"ready_to_apply": map[string]any{"type": "boolean", "const": false}, "bindings_verified": map[string]any{"type": "boolean", "const": false},
		"status": map[string]any{"type": "string", "enum": []string{"pending", "approved", "rejected", "revoked", "expired", "stale"}}}
	props["execution"] = map[string]any{"type": "object"}
	for _, name := range []string{"id", "account_id", "requester_key_id", "intent_sha256", "created_at", "expires_at", "decided_at"} {
		props[name] = map[string]any{"type": "string"}
	}
	return map[string]any{"type": "object", "properties": props, "additionalProperties": false,
		"required": []string{"version", "id", "account_id", "requester_key_id", "intent_sha256", "intent", "status", "created_at", "expires_at", "ready_to_apply", "bindings_verified"}}
}
