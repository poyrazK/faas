package mcpmanagement

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type planTaskToolArgs struct {
	PlanPath string `json:"plan_path" jsonschema:"Absolute path to an existing native MCP Task stack release JSON plan; source paths resolve relative to that plan"`
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
	body, err := m.runCLI(ctx, []string{"mcp", "tasks", "release", "plan", "--plan", filepath.Clean(args.PlanPath)}, nil)
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
