package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func taskRestoreReceipt(t *testing.T) api.TaskToolReleaseIntentRecord {
	t.Helper()
	old := taskIntentReceipt(t)
	in := old.Intent
	baseline := uuid.NewString()
	web, worker := uuid.NewString(), uuid.NewString()
	for i := range in.Apps {
		if in.Apps[i].Role == "web" {
			in.Apps[i].Review.LatestDeploymentID = baseline
			in.Apps[i].Review.ServingDeploymentIDs = []string{baseline}
		}
	}
	r, err := tasktoolrelease.New(old.AccountID, old.RequesterKeyID, old.ID, in, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	r.Status = "revoked"
	stamp := time.Now().Add(-time.Second).UTC()
	r.Execution = &api.TaskToolReleaseExecution{OperationID: strings.Repeat("1", 64), BindingsSHA256: strings.Repeat("2", 64), AuthorizerKeyID: uuid.NewString(), State: "reconcile_required", AuthorizedAt: r.CreatedAt, FenceVersion: 1, ClaimSHA256: toolpublication.Digest([]byte(strings.Repeat("3", 64))), ExecutorExitedAt: &stamp}
	r.Execution.Receipt = &api.TaskToolReleaseReceipt{Revision: 1, ReleaseID: tasktoolrelease.ExecutionReleaseID(r), Stage: "web_promoted", ServingCaptured: true, ServingDeploymentID: baseline, WebDeploymentID: web, WorkerDeploymentID: worker, PreviousDeployments: map[string]string{}, WorkerIDs: []string{uuid.NewString()}, Promoted: true, Parked: map[string]bool{}}
	r.Execution.CompletedStages = tasktoolrelease.ReceiptStages(*r.Execution.Receipt)
	nativeApps := append([]api.TaskToolReleaseApp{}, in.Apps...)
	for i := range nativeApps {
		if nativeApps[i].Role == "web" {
			nativeApps[i].Review.LatestDeploymentID = web
			nativeApps[i].Review.ServingDeploymentIDs = []string{web}
		}
		if nativeApps[i].Role == "worker" {
			nativeApps[i].Review.LatestDeploymentID = worker
		}
	}
	q := &api.TaskToolRecoveryQualification{HandoverID: strings.Repeat("4", 64), OperationID: r.Execution.OperationID, Revision: 1, Action: "restore", TargetDeploymentID: baseline, ExpectedServingDeploymentID: web, BindingsSHA256: r.Execution.BindingsSHA256, EvidenceSHA256: strings.Repeat("5", 64), NativeApps: nativeApps, ObservedAt: stamp, ExpiresAt: stamp.Add(api.TaskToolRecoveryApprovalTTL)}
	r.Execution.Recovery = &api.TaskToolRecoveryHandover{ID: q.HandoverID, OperationID: q.OperationID, Revision: 1, State: "settled", RequestedAt: stamp, SettledAt: &stamp, Qualification: q, Decision: "approved", AuthorizerKeyID: r.Execution.AuthorizerKeyID, DecidedAt: &stamp}
	return r
}
func TestMCPTaskRestoreProtocolsAndRetainedResults(t *testing.T) {
	for _, protocol := range []string{api.MCPProtocolVersion, api.MCPLegacyProtocolVersion} {
		t.Run(protocol, func(t *testing.T) {
			record := taskRestoreReceipt(t)
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
				if len(input) != 0 || len(argv) != 14 || strings.Join(argv[:5], " ") != "mcp server tasks execution restore" || argv[6] != record.ID || argv[10] != record.Execution.Recovery.ID {
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
			args := applyTaskRestoreArgs{IntentID: record.ID, OperationID: record.Execution.OperationID, HandoverID: record.Execution.Recovery.ID, QualificationSHA256: tasktoolrelease.QualificationDigest(*record.Execution.Recovery.Qualification), AllowToolExecution: true}
			denied := args
			denied.AllowToolExecution = false
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_restore", denied, true)
			denied = args
			denied.QualificationSHA256 = strings.Repeat("9", 64)
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_restore", denied, true)
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_restore", args, false)
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("restore executor not started")
			}
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_restore", args, false)
			if calls.Load() != 1 {
				t.Fatal("duplicate started another gate")
			}
			mu.Lock()
			q := record.Execution.Recovery.Qualification
			record.Execution.Recovery.Result = &api.TaskToolRecoveryResult{IntentID: record.ID, OperationID: args.OperationID, HandoverID: args.HandoverID, QualificationSHA256: args.QualificationSHA256, Revision: 1, Action: "restore", TargetDeploymentID: q.TargetDeploymentID, PreviousServingDeploymentID: q.ExpectedServingDeploymentID, EvidenceSHA256: strings.Repeat("6", 64), CheckedAt: time.Now().UTC(), CommittedAt: time.Now().UTC()}
			record.Execution.Recovery.Decision = "revoked"
			mu.Unlock()
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_restore", args, false)
			publicationCall(t, ctx, cs, "gregale_get_task_tool_operation", taskReleaseIntentArgs{IntentID: record.ID}, false)
			if calls.Load() != 1 {
				t.Fatal("retained result restarted a write")
			}
			readonly, _ := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, LocalCLI: runner, Toolsets: []string{ToolsetBuildTools}}, protocol)
			if listedTools(t, ctx, readonly)["gregale_apply_task_tool_restore"] {
				t.Fatal("read-only connection exposed restore apply")
			}
		})
	}
}
