package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPTaskRecoveryProtocolsWithoutLocalFiles(t *testing.T) {
	for _, protocol := range []string{api.MCPProtocolVersion, api.MCPLegacyProtocolVersion} {
		t.Run(protocol, func(t *testing.T) {
			record := taskIntentReceipt(t)
			var requests, cliCalls atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != "GET" || r.URL.Path != "/v1/task-tool-release-intents/"+record.ID {
					t.Error("unexpected recovery mutation or read", r.Method, r.URL.Path)
					w.WriteHeader(500)
					return
				}
				_ = json.NewEncoder(w).Encode(record)
			}))
			defer backend.Close()
			unavailable := func(context.Context, []string, []byte) (LocalCLIResult, error) {
				cliCalls.Add(1)
				return LocalCLIResult{}, errors.New("local files and CLI unavailable")
			}
			cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, LocalCLI: unavailable, Toolsets: []string{ToolsetBuildTools}}, protocol)
			catalog, err := cs.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			tools := map[string]bool{}
			for _, tool := range catalog.Tools {
				tools[tool.Name] = true
			}
			if !tools["gregale_inspect_task_tool_recovery"] || !tools["gregale_plan_task_tool_recovery"] || tools["gregale_apply_task_tool_deploy"] {
				t.Fatal("read-only recovery discovery lost its boundary", tools)
			}
			for _, name := range []string{"gregale_inspect_task_tool_recovery", "gregale_plan_task_tool_recovery"} {
				publicationCall(t, ctx, cs, name, taskReleaseIntentArgs{IntentID: record.ID}, false)
			}
			if cliCalls.Load() != 0 || requests.Load() != 2 {
				t.Fatal("recovery depended on local execution or unbounded reads", cliCalls.Load(), requests.Load())
			}
		})
	}
}
