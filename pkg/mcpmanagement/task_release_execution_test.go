package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPTaskExecutionProtocolsAndDuplicates(t *testing.T) {
	for _, protocol := range []string{api.MCPProtocolVersion, api.MCPLegacyProtocolVersion} {
		t.Run(protocol, func(t *testing.T) {
			record := taskIntentReceipt(t)
			record.Status = "approved"
			record.Execution = &api.TaskToolReleaseExecution{OperationID: strings.Repeat("d", 64), BindingsSHA256: strings.Repeat("e", 64), AuthorizerKeyID: uuid.NewString(), State: "authorized", AuthorizedAt: time.Now()}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/v1/task-tool-release-intents/"+record.ID {
					t.Error("unexpected mutation", r.Method, r.URL.Path)
					w.WriteHeader(500)
					return
				}
				_ = json.NewEncoder(w).Encode(record)
			}))
			defer backend.Close()
			var calls atomic.Int32
			started, stop := make(chan struct{}), make(chan struct{})
			executor := func(ctx context.Context, args []string, input []byte) (LocalCLIResult, error) {
				calls.Add(1)
				want := "mcp server tasks execution apply --id " + record.ID + " --operation-id " + record.Execution.OperationID + " --allow-tool-execution"
				if strings.Join(args, " ") != want || len(input) != 0 {
					t.Error("unexpected command or secret input", args)
				}
				close(started)
				select {
				case <-stop:
				case <-ctx.Done():
				}
				return LocalCLIResult{ExitCode: 1, Stderr: []byte("SECRET_SENTINEL")}, nil
			}
			cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, LocalCLI: executor, Toolsets: []string{ToolsetBuildTools}, AllowWrites: true}, protocol)
			defer close(stop)
			args := applyTaskExecutionArgs{IntentID: record.ID, OperationID: record.Execution.OperationID, AllowToolExecution: true}
			denied := args
			denied.AllowToolExecution = false
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_deploy", denied, true)
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_deploy", args, false)
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("execution worker did not start")
			}
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_deploy", args, false)
			publicationCall(t, ctx, cs, "gregale_get_task_tool_operation", taskReleaseIntentArgs{IntentID: record.ID}, false)
			if calls.Load() != 1 {
				t.Fatal("duplicate spawned an executor", calls.Load())
			}
		})
	}
}
