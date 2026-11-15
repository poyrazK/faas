package mcpmanagement

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type planTaskToolArgs struct {
	PlanPath string `json:"plan_path" jsonschema:"Absolute path to an existing native MCP Task stack release JSON plan; source paths resolve relative to that plan"`
}

func taskToolReviewOutputSchema() map[string]any {
	props := map[string]any{
		"version":             map[string]any{"type": "integer", "const": 1},
		"status":              map[string]any{"type": "string", "const": "review_only"},
		"worker_runtime":      map[string]any{"type": "string", "const": "node"},
		"protocol":            map[string]any{"type": "string", "const": api.MCPProtocolVersion},
		"ready_to_apply":      map[string]any{"type": "boolean", "const": false},
		"bindings_verified":   map[string]any{"type": "boolean", "const": false},
		"migrations_required": map[string]any{"type": "boolean", "const": true},
	}
	for _, name := range []string{"release_fingerprint", "web_app", "worker_app", "observer_app", "observer_metric_app", "endpoint", "access_mode"} {
		props[name] = map[string]any{"type": "string"}
	}
	for _, name := range []string{"previous_worker_apps", "readiness_roles", "required_setup"} {
		props[name] = map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "string"}}
	}
	for _, name := range []string{"tool_scopes", "web_configured_limits", "worker_configured_limits"} {
		props[name] = map[string]any{"type": "object"}
	}
	props["release_timeout_seconds"] = map[string]any{"type": "integer"}
	required := make([]string, 0, len(props))
	for name := range props {
		required = append(required, name)
	}
	sort.Strings(required)
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func (m *management) planTaskToolDeploy(ctx context.Context, args planTaskToolArgs) (any, error) {
	if err := m.toolBuildingAccess(); err != nil {
		return nil, err
	}
	if m.remote() {
		return nil, invalid("tool_build_access_denied", "Task source review requires a local account connection")
	}
	if !filepath.IsAbs(args.PlanPath) {
		return nil, invalid("invalid_argument", "plan_path must be an absolute native Task release plan path")
	}
	body, err := m.runCLI(ctx, []string{"mcp", "server", "tasks", "deploy", "plan", "--plan", filepath.Clean(args.PlanPath)}, nil)
	if err != nil {
		// File parse errors and CLI stderr can contain source or credential data.
		return nil, invalid("task_plan_failed", "local Task release review failed; inspect the plan with the local CLI")
	}
	var review mcphosting.TaskReleaseReview
	if len(body) > maxLocalShapeBytes || json.Unmarshal(body, &review) != nil ||
		review.Version != 1 || review.Status != "review_only" || !toolpublication.ValidID(review.Fingerprint) ||
		review.ReadyToApply || review.BindingsVerified || !review.MigrationsRequired ||
		review.WorkerRuntime != "node" || review.Protocol != api.MCPProtocolVersion {
		return nil, invalid("invalid_response", "local CLI returned no compatible offline Task release review")
	}
	return review, nil
}
