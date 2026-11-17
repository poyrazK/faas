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
)

func taskRetirementReceipt(t *testing.T) api.TaskToolReleaseIntentRecord {
	t.Helper()
	r := taskQuarantineReceipt(t)
	stamp := time.Now().UTC()
	q := r.Execution.Recovery.Quarantine
	q.State = "quarantined"
	q.AttemptSHA256 = strings.Repeat("8", 64)
	q.ClaimedAt = &stamp
	q.Result = &api.TaskToolQuarantineResult{OperationID: r.Execution.OperationID, HandoverID: r.Execution.Recovery.ID, QualificationSHA256: tasktoolrelease.QualificationDigest(q.Qualification), WorkerDeploymentID: r.Execution.Receipt.WorkerDeploymentID, ServingDeploymentID: r.Execution.Receipt.ServingDeploymentID, WorkerIDs: r.Execution.Receipt.WorkerIDs, EvidenceSHA256: strings.Repeat("9", 64), ObservedAt: stamp, RecordedAt: stamp}
	qual := q.Qualification
	qual.Action = "retire"
	qual.ObservedAt = stamp
	qual.ExpiresAt = stamp.Add(api.TaskToolRecoveryApprovalTTL)
	r.Execution.Recovery.Retirement = &api.TaskToolRetirement{Qualification: qual, State: "qualified", Decision: "approved", AuthorizerKeyID: r.Execution.AuthorizerKeyID, DecidedAt: &stamp}
	return r
}
func TestMCPTaskRetirementProtocolsAndRetainedResults(t *testing.T) {
	for _, protocol := range []string{api.MCPProtocolVersion, api.MCPLegacyProtocolVersion} {
		t.Run(protocol, func(t *testing.T) {
			record := taskRetirementReceipt(t)
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
				if len(input) != 0 || len(argv) != 14 || strings.Join(argv[:5], " ") != "mcp server tasks execution retire" || argv[6] != record.ID || argv[10] != record.Execution.Recovery.ID {
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
			args := applyTaskRetirementArgs{IntentID: record.ID, OperationID: record.Execution.OperationID, HandoverID: record.Execution.Recovery.ID, QualificationSHA256: tasktoolrelease.QualificationDigest(record.Execution.Recovery.Retirement.Qualification), AllowToolExecution: true}
			denied := args
			denied.AllowToolExecution = false
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_retire", denied, true)
			denied = args
			denied.QualificationSHA256 = strings.Repeat("9", 64)
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_retire", denied, true)
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_retire", args, false)
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("restore executor not started")
			}
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_retire", args, false)
			if calls.Load() != 1 {
				t.Fatal("duplicate started another gate")
			}
			mu.Lock()
			q := record.Execution.Recovery.Retirement
			stamp := time.Now().UTC()
			q.ParkRequestedAt = &stamp
			q.ParkTransitionID = uuid.NewString()
			q.State = "retired"
			q.Result = &api.TaskToolRetirementResult{OperationID: args.OperationID, HandoverID: args.HandoverID, QualificationSHA256: args.QualificationSHA256, WorkerDeploymentID: record.Execution.Receipt.WorkerDeploymentID, ServingDeploymentID: record.Execution.Receipt.ServingDeploymentID, WorkerIDs: record.Execution.Receipt.WorkerIDs, ParkTransitionID: q.ParkTransitionID, EvidenceSHA256: strings.Repeat("9", 64), ObservedAt: stamp, RecordedAt: stamp}
			q.Decision = "revoked"
			mu.Unlock()
			publicationCall(t, ctx, cs, "gregale_apply_task_tool_retire", args, false)
			publicationCall(t, ctx, cs, "gregale_get_task_tool_operation", taskReleaseIntentArgs{IntentID: record.ID}, false)
			if calls.Load() != 1 {
				t.Fatal("retained result restarted a write")
			}
			readonly, _ := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, LocalCLI: runner, Toolsets: []string{ToolsetBuildTools}}, protocol)
			if listedTools(t, ctx, readonly)["gregale_apply_task_tool_retire"] {
				t.Fatal("read-only connection exposed restore apply")
			}
		})
	}
}

func TestMCPTaskRetirementPendingObservationAfterExpiryAndRevocation(t *testing.T) {
	record := taskRetirementReceipt(t)
	// Move every historical timestamp together so qualification identities remain
	// valid while the retained park is now outside its approval window.
	raw, _ := json.Marshal(record)
	var document any
	_ = json.Unmarshal(raw, &document)
	var shift func(any) any
	shift = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, item := range x {
				x[k] = shift(item)
			}
			return x
		case []any:
			for i, item := range x {
				x[i] = shift(item)
			}
			return x
		case string:
			if stamp, err := time.Parse(time.RFC3339Nano, x); err == nil {
				return stamp.Add(-2 * time.Minute).Format(time.RFC3339Nano)
			}
		}
		return v
	}
	raw, _ = json.Marshal(shift(document))
	if json.Unmarshal(raw, &record) != nil {
		t.Fatal("historical fixture")
	}
	record.Execution.Recovery.Result.QualificationSHA256 = tasktoolrelease.QualificationDigest(*record.Execution.Recovery.Qualification)
	record.Execution.Recovery.Quarantine.Result.QualificationSHA256 = tasktoolrelease.QualificationDigest(record.Execution.Recovery.Quarantine.Qualification)
	q := record.Execution.Recovery.Retirement
	stamp := q.Qualification.ObservedAt
	q.State = "retirement_pending"
	q.ParkTransitionID = uuid.NewString()
	q.ParkRequestedAt = &stamp
	q.Decision = "revoked"
	if !tasktoolrelease.ValidRetirement(record) {
		t.Fatal("invalid pending fixture")
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("management wrote native state")
		}
		_ = json.NewEncoder(w).Encode(record)
	}))
	defer backend.Close()
	called := make(chan struct{}, 1)
	runner := func(ctx context.Context, argv []string, input []byte) (LocalCLIResult, error) {
		if !strings.Contains(strings.Join(argv, " "), "execution retire") || len(input) != 0 {
			t.Error("unbound local dispatch")
		}
		called <- struct{}{}
		return LocalCLIResult{}, nil
	}
	cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true, LocalCLI: runner, Toolsets: []string{ToolsetBuildTools}}, api.MCPProtocolVersion)
	args := applyTaskRetirementArgs{IntentID: record.ID, OperationID: record.Execution.OperationID, HandoverID: record.Execution.Recovery.ID, QualificationSHA256: tasktoolrelease.QualificationDigest(q.Qualification), AllowToolExecution: true}
	publicationCall(t, ctx, cs, "gregale_apply_task_tool_retire", args, false)
	select {
	case <-called:
	case <-time.After(3 * time.Second):
		t.Fatal("pending observation incorrectly needs renewed approval")
	}
}
