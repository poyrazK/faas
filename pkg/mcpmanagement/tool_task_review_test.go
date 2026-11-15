package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestTaskToolPlanReadOnlyProtocols(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			_, backend := newPublicationFixture(t)
			path := filepath.Join(t.TempDir(), "release.json")
			calls := 0
			executor := func(_ context.Context, args []string, input []byte) (LocalCLIResult, error) {
				calls++
				want := []string{"mcp", "tasks", "release", "plan", "--plan", path}
				if !reflect.DeepEqual(args, want) || len(input) != 0 {
					t.Fatalf("review dispatched unexpected execution: %v", args)
				}
				review := mcphosting.TaskReleaseReview{Version: 1, Status: "review_only", Fingerprint: strings.Repeat("a", 64),
					Protocol: "2026-07-28", WorkerRuntime: "node", WebApp: "web", WorkerApp: "worker", ObserverApp: "observer", ObserverMetricApp: "worker",
					Endpoint: "/mcp", AccessMode: "open", ToolScopes: mcphosting.ScopePolicy{"report": {}}, MigrationsRequired: true}
				body, err := json.Marshal(review)
				if err != nil {
					t.Fatal(err)
				}
				body = append(body[:len(body)-1], []byte(`,"credential":"SECRET_SENTINEL"}`)...)
				return LocalCLIResult{Stdout: body}, nil
			}
			cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, LocalCLI: executor, Toolsets: []string{ToolsetBuildTools}}, protocol)
			tools := listedTools(t, ctx, cs)
			if !tools["gregale_plan_task_tool_deploy"] || tools["gregale_apply_tool_deploy"] {
				t.Fatal("read-only Task review capability was not isolated from apply")
			}
			catalog, err := cs.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, tool := range catalog.Tools {
				if tool.Name == "gregale_plan_task_tool_deploy" && (tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint) {
					t.Fatal("offline review must be read-only and idempotent")
				}
			}
			out := publicationCall(t, ctx, cs, "gregale_plan_task_tool_deploy", planTaskToolArgs{PlanPath: path}, false)
			body, _ := json.Marshal(out)
			if calls != 1 || strings.Contains(string(body), "SECRET_SENTINEL") || out["plan_id"] != nil || string(out["ready_to_apply"]) != "false" {
				t.Fatal("review exposed private output or an executable plan", string(body))
			}
			publicationCall(t, ctx, cs, "gregale_plan_task_tool_deploy", planTaskToolArgs{PlanPath: "relative.json"}, true)
			result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_plan_task_tool_deploy", Arguments: map[string]any{"plan_path": path, "credential": "inline-secret"}})
			if err == nil && !result.IsError {
				t.Fatal("review accepted an undeclared credential argument")
			}
			if calls != 1 {
				t.Fatal("invalid path reached CLI")
			}
		})
	}
}

func TestTaskToolPlanRejectsExecutionAndOpaqueErrors(t *testing.T) {
	for _, scenario := range []string{"executable receipt", "CLI error", "oversized result"} {
		t.Run(scenario, func(t *testing.T) {
			_, backend := newPublicationFixture(t)
			executor := func(context.Context, []string, []byte) (LocalCLIResult, error) {
				switch scenario {
				case "CLI error":
					return LocalCLIResult{}, errors.New("SECRET_SENTINEL")
				case "oversized result":
					return LocalCLIResult{Stdout: []byte(strings.Repeat("SECRET_SENTINEL", maxLocalShapeBytes))}, nil
				default:
					r := mcphosting.TaskReleaseReview{Version: 1, Status: "review_only", Fingerprint: strings.Repeat("a", 64), WorkerRuntime: "node", Protocol: "2026-07-28", MigrationsRequired: true, ReadyToApply: true}
					b, _ := json.Marshal(r)
					return LocalCLIResult{Stdout: b}, nil
				}
			}
			_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, LocalCLI: executor, Toolsets: []string{ToolsetBuildTools}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = m.planTaskToolDeploy(t.Context(), planTaskToolArgs{PlanPath: filepath.Join(t.TempDir(), "plan.json")})
			if err == nil || strings.Contains(err.Error(), "SECRET_SENTINEL") {
				t.Fatal("untrusted CLI output escaped review", err)
			}
		})
	}
}
