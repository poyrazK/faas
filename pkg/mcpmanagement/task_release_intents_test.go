package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func taskIntentReceipt(t *testing.T) api.TaskToolReleaseIntentRecord {
	t.Helper()
	in := api.TaskToolReleaseIntent{ReleaseFingerprint: strings.Repeat("a", 64), WorkerRuntime: "node", Protocol: api.MCPProtocolVersion,
		Endpoint: "/mcp", AccessMode: "open", ObserverMetricApp: "worker", ToolScopes: map[string][]string{"report": {}}, Reason: "Review report worker", RequiredSetup: tasktoolrelease.RequiredSetup()}
	for _, role := range []string{"web", "worker", "observer"} {
		in.Apps = append(in.Apps, api.TaskToolReleaseApp{Role: role, App: role, Review: api.AppReleaseReview{
			AppID: uuid.NewString(), Scope: api.DefaultEnvScope, ConfigurationHash: strings.Repeat("b", 64), ServingDeploymentIDs: []string{}}})
	}
	out, err := tasktoolrelease.New(uuid.NewString(), uuid.NewString(), strings.Repeat("c", 64), in, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTaskIntentManagementProtocols(t *testing.T) {
	for _, protocol := range []string{api.MCPProtocolVersion, api.MCPLegacyProtocolVersion} {
		t.Run(protocol, func(t *testing.T) {
			record := taskIntentReceipt(t)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/v1/task-tool-release-intents/"+record.ID {
					t.Error("unexpected native request", r.Method, r.URL.Path)
					w.WriteHeader(500)
					return
				}
				_ = json.NewEncoder(w).Encode(record)
			}))
			defer backend.Close()
			path := filepath.Join(t.TempDir(), "release.json")
			calls := 0
			executor := func(_ context.Context, args []string, input []byte) (LocalCLIResult, error) {
				calls++
				if len(args) < 9 || strings.Join(args[:4], " ") != "mcp server tasks intent" || args[4] != "prepare" && args[4] != "get" || args[5] != "--id" || args[6] != record.ID || args[7] != "--plan" || args[8] != path || len(input) != 0 {
					t.Fatal("Task intent dispatched unexpected execution", args)
				}
				body, _ := json.Marshal(record)
				return LocalCLIResult{Stdout: body}, nil
			}
			// Reads recover the native journal even with write consent disabled.
			cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, LocalCLI: executor, Toolsets: []string{ToolsetBuildTools}}, protocol)
			tools := listedTools(t, ctx, cs)
			if !tools["gregale_get_task_tool_deploy"] || tools["gregale_prepare_task_tool_deploy"] || tools["gregale_check_task_tool_deploy"] {
				t.Fatal("Task journal write consent was bypassed")
			}
			publicationCall(t, ctx, cs, "gregale_get_task_tool_deploy", taskReleaseIntentArgs{IntentID: record.ID}, false)
			if calls != 0 {
				t.Fatal("journal read inspected source or executed a CLI action")
			}
			cs, ctx = connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true, LocalCLI: executor, Toolsets: []string{ToolsetBuildTools}}, protocol)
			publicationCall(t, ctx, cs, "gregale_prepare_task_tool_deploy", prepareTaskReleaseArgs{IntentID: record.ID, PlanPath: path, Reason: record.Intent.Reason}, false)
			publicationCall(t, ctx, cs, "gregale_check_task_tool_deploy", checkTaskReleaseArgs{IntentID: record.ID, PlanPath: path}, false)
			for name := range listedTools(t, ctx, cs) {
				if strings.Contains(name, "task") && (strings.Contains(name, "approve") || strings.Contains(name, "apply")) {
					t.Fatal("agent received owner or execution authority", name)
				}
			}
		})
	}
}

func TestTaskIntentManagementRejectsUnsafeReceipts(t *testing.T) {
	record := taskIntentReceipt(t)
	for _, scenario := range []string{"CLI error", "execution authority", "changed digest", "extended expiry"} {
		t.Run(scenario, func(t *testing.T) {
			copy := record
			executor := func(context.Context, []string, []byte) (LocalCLIResult, error) {
				if scenario == "CLI error" {
					return LocalCLIResult{}, errors.New("SECRET_SENTINEL")
				}
				switch scenario {
				case "execution authority":
					copy.ReadyToApply = true
				case "changed digest":
					copy.IntentSHA256 = strings.Repeat("f", 64)
				case "extended expiry":
					copy.ExpiresAt = copy.ExpiresAt.Add(time.Hour)
				}
				body, _ := json.Marshal(copy)
				return LocalCLIResult{Stdout: body}, nil
			}
			_, m, err := newManagementServer(api.NewClient("http://invalid", testToken), Options{AllowWrites: true, LocalCLI: executor, Toolsets: []string{ToolsetBuildTools}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = m.checkTaskReleaseIntent(t.Context(), checkTaskReleaseArgs{IntentID: record.ID, PlanPath: filepath.Join(t.TempDir(), "plan.json")})
			if err == nil || strings.Contains(err.Error(), "SECRET_SENTINEL") {
				t.Fatal("unsafe review escaped validation", err)
			}
		})
	}
}
