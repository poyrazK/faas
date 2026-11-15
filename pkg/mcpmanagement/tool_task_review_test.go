package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
				return LocalCLIResult{Stdout: []byte(`{"version":1,"status":"review_only","release_fingerprint":"` + strings.Repeat("a", 64) + `","protocol":"2026-07-28","worker_runtime":"node","migrations_required":true,"credential":"SECRET_SENTINEL"}`)}, nil
			}
			cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, LocalCLI: executor, Toolsets: []string{ToolsetBuildTools}}, protocol)
			tools := listedTools(t, ctx, cs)
			if !tools["gregale_plan_task_tool_deploy"] || tools["gregale_apply_tool_deploy"] {
				t.Fatal("read-only Task review capability was not isolated from apply")
			}
			out := publicationCall(t, ctx, cs, "gregale_plan_task_tool_deploy", planTaskToolArgs{PlanPath: path}, false)
			body, _ := json.Marshal(out)
			if calls != 1 || strings.Contains(string(body), "SECRET_SENTINEL") || out["plan_id"] != nil || string(out["ready_to_apply"]) != "false" {
				t.Fatal("review exposed private output or an executable plan", string(body))
			}
			publicationCall(t, ctx, cs, "gregale_plan_task_tool_deploy", planTaskToolArgs{PlanPath: "relative.json"}, true)
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
