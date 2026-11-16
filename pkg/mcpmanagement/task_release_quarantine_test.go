package mcpmanagement

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func taskQuarantineReceipt(t *testing.T) api.TaskToolReleaseIntentRecord {
	t.Helper()
	r := taskRestoreReceipt(t)
	prev := api.TaskToolReleaseApp{Role: "previous_worker", App: "previous", Review: api.AppReleaseReview{AppID: uuid.NewString(), Scope: api.DefaultEnvScope, ConfigurationHash: strings.Repeat("b", 64), LatestDeploymentID: uuid.NewString(), ServingDeploymentIDs: []string{}}}
	r.Intent.Apps = append(r.Intent.Apps, prev)
	native, err := tasktoolrelease.New(r.AccountID, r.RequesterKeyID, r.ID, r.Intent, r.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	r.IntentSHA256 = native.IntentSHA256
	r.Execution.Receipt.ReleaseID = tasktoolrelease.ExecutionReleaseID(r)
	r.Execution.Receipt.PreviousDeployments[prev.App] = prev.Review.LatestDeploymentID
	r.Execution.CompletedStages = tasktoolrelease.ReceiptStages(*r.Execution.Receipt)
	r.Execution.Recovery.Qualification.NativeApps = append(r.Execution.Recovery.Qualification.NativeApps, prev)
	stamp := time.Now().UTC()
	q := r.Execution.Recovery.Qualification
	r.Execution.Recovery.Result = &api.TaskToolRecoveryResult{IntentID: r.ID, OperationID: r.Execution.OperationID, HandoverID: q.HandoverID, QualificationSHA256: tasktoolrelease.QualificationDigest(*q), Revision: 1, Action: "restore", TargetDeploymentID: q.TargetDeploymentID, PreviousServingDeploymentID: q.ExpectedServingDeploymentID, EvidenceSHA256: strings.Repeat("6", 64), CheckedAt: stamp, CommittedAt: stamp}
	qual := *q
	qual.Action = "quarantine"
	qual.TargetDeploymentID = r.Execution.Receipt.WorkerDeploymentID
	qual.ExpectedServingDeploymentID = r.Execution.Receipt.ServingDeploymentID
	qual.NativeApps = append([]api.TaskToolReleaseApp{}, q.NativeApps...)
	qual.ObservedAt = stamp
	qual.ExpiresAt = stamp.Add(api.TaskToolRecoveryApprovalTTL)
	for i := range qual.NativeApps {
		if qual.NativeApps[i].Role == "web" {
			qual.NativeApps[i].Review.ServingDeploymentIDs = []string{r.Execution.Receipt.ServingDeploymentID}
		}
	}
	r.Execution.Recovery.Quarantine = &api.TaskToolQuarantine{Qualification: qual, State: "qualified", Decision: "approved", AuthorizerKeyID: r.Execution.AuthorizerKeyID, DecidedAt: &stamp}
	return r
}
func TestMCPTaskQuarantineProtocolsAndRetainedResults(t *testing.T) {
	for _, protocol := range []string{api.MCPProtocolVersion, api.MCPLegacyProtocolVersion} {
		t.Run(protocol, func(t *testing.T) {
			record := taskQuarantineReceipt(t)
			var mu sync.Mutex
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method != "GET" {
					t.Error("management bypassed local gate")
				}
				_ = json.NewEncoder(w).Encode(record)
			}))
			defer backend.Close()
			started, stop := make(chan struct{}), make(chan struct{})
			defer close(stop)
			var calls atomic.Int32
			runner := func(ctx context.Context, argv []string, input []byte) (LocalCLIResult, error) {
				calls.Add(1)
				if len(input) != 0 || len(argv) != 14 || strings.Join(argv[:5], " ") != "mcp server tasks execution quarantine" || argv[6] != record.ID || argv[10] != record.Execution.Recovery.ID {
					t.Error("unexpected restore arguments", argv)
				}
				close(started)
				select {
				case <-stop:
				case <-ctx.Done():
				}
				return LocalCLIResult{ExitCode: 1, Stderr: []byte("SECRET_SENTINEL")}, nil
			}
			cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true, LocalCLI: runner, Toolsets: []string{ToolsetBuildTools}}, protocol)
			args := applyTaskQuarantineArgs{IntentID: record.ID, OperationID: record.Execution.OperationID, HandoverID: record.Execution.Recovery.ID, QualificationSHA256: tasktoolrelease.QualificationDigest(record.Execution.Recovery.Quarantine.Qualification), AllowToolExecution: true}
			denied := args
			denied.AllowToolExecution = false
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_quarantine", denied, true)
			denied = args
			denied.QualificationSHA256 = strings.Repeat("9", 64)
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_quarantine", denied, true)
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_quarantine", args, false)
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("restore executor not started")
			}
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_quarantine", args, false)
			if calls.Load() != 1 {
				t.Fatal("duplicate started another gate")
			}
			mu.Lock()
			q := record.Execution.Recovery.Quarantine
			stamp := time.Now().UTC()
			q.ClaimedAt = &stamp
			q.AttemptSHA256 = strings.Repeat("8", 64)
			q.State = "quarantined"
			q.Result = &api.TaskToolQuarantineResult{OperationID: args.OperationID, HandoverID: args.HandoverID, QualificationSHA256: args.QualificationSHA256, WorkerDeploymentID: record.Execution.Receipt.WorkerDeploymentID, ServingDeploymentID: record.Execution.Receipt.ServingDeploymentID, WorkerIDs: record.Execution.Receipt.WorkerIDs, EvidenceSHA256: strings.Repeat("9", 64), ObservedAt: stamp, RecordedAt: stamp}
			q.Decision = "revoked"
			mu.Unlock()
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_quarantine", args, false)
			publicationCall(t, ctx, cs, "gregale_get_task_tool_operation", taskReleaseIntentArgs{IntentID: record.ID}, false)
			if calls.Load() != 1 {
				t.Fatal("retained result restarted a write")
			}
			readonly, _ := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, LocalCLI: runner, Toolsets: []string{ToolsetBuildTools}}, protocol)
			if listedTools(t, ctx, readonly)["gregale_apply_task_tool_quarantine"] {
				t.Fatal("read-only connection exposed restore apply")
			}
		})
	}
}
