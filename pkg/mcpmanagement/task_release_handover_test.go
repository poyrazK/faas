package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func TestMCPTaskRecoveryMetadataProtocols(t *testing.T) {
	for _, protocol := range []string{api.MCPProtocolVersion, api.MCPLegacyProtocolVersion} {
		t.Run(protocol, func(t *testing.T) {
			record := taskIntentReceipt(t)
			record.Status = "approved"
			exited := time.Now().UTC()
			record.Execution = &api.TaskToolReleaseExecution{OperationID: strings.Repeat("1", 64), BindingsSHA256: strings.Repeat("2", 64), AuthorizerKeyID: uuid.NewString(), AuthorizedAt: record.CreatedAt, State: "reconcile_required", FenceVersion: 1, ClaimSHA256: strings.Repeat("3", 64), ExecutorExitedAt: &exited}
			record.Execution.Receipt = &api.TaskToolReleaseReceipt{Revision: 1, ReleaseID: tasktoolrelease.ExecutionReleaseID(record), Stage: "prepared", PreviousDeployments: map[string]string{}, WorkerIDs: []string{}, Parked: map[string]bool{}}
			record.Execution.CompletedStages = tasktoolrelease.ReceiptStages(*record.Execution.Receipt)
			record.Execution.Recovery = &api.TaskToolRecoveryHandover{ID: strings.Repeat("4", 64), OperationID: record.Execution.OperationID, Revision: 1, State: "settled", RequestedAt: exited, SettledAt: &exited}
			var recordMu sync.Mutex
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				recordMu.Lock()
				defer recordMu.Unlock()
				if r.Method != "GET" {
					t.Error("metadata read mutated native state")
				}
				_ = json.NewEncoder(w).Encode(record)
			}))
			defer backend.Close()
			cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, LocalCLI: func(context.Context, []string, []byte) (LocalCLIResult, error) {
				t.Error("metadata read ran CLI")
				return LocalCLIResult{}, nil
			}, Toolsets: []string{ToolsetBuildTools}}, protocol)
			publicationCall(t, ctx, cs, "gregale_get_task_tool_operation", taskReleaseIntentArgs{IntentID: record.ID}, false)
			body, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			validateWorkflowSchema(t, taskIntentOutputSchema(), body)
			for name := range listedTools(t, ctx, cs) {
				if strings.Contains(name, "task") && (strings.Contains(name, "approve") || strings.Contains(name, "qualify") || strings.Contains(name, "stop") || strings.Contains(name, "restore")) {
					t.Fatal("owner recovery authority exposed to agent", name)
				}
			}
			recordMu.Lock()
			record.Execution.Recovery.Revision++
			recordMu.Unlock()
			publicationCall(t, ctx, cs, "gregale_get_task_tool_operation", taskReleaseIntentArgs{IntentID: record.ID}, true)
		})
	}
}
