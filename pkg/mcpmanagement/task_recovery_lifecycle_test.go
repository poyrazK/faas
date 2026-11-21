package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func TestMCPTaskRecoveryLifecycleBothProtocols(t *testing.T) {
	for _, protocol := range []string{api.MCPProtocolVersion, api.MCPLegacyProtocolVersion} {
		for _, stage := range []string{"restored", "quarantined", "quarantine_applying", "retirement_pending", "retired", "expired", "retirement_pending_expired", "malformed"} {
			t.Run(protocol+"/"+stage, func(t *testing.T) {
				record := taskRetirementReceipt(t)
				if stage == "expired" || stage == "retirement_pending_expired" {
					record = shiftRecoveryLifecycleClock(t, record, -2*time.Minute)
				}
				h := record.Execution.Recovery
				q := h.Retirement
				stamp := q.Qualification.ObservedAt
				expected, step := "quarantined", "owner_reconciliation"
				switch stage {
				case "restored":
					h.Quarantine, h.Retirement = nil, nil
					expected = "restored"
				case "quarantined":
					h.Retirement = nil
				case "quarantine_applying":
					h.Retirement = nil
					h.Quarantine.Result = nil
					h.Quarantine.State = "applying"
					h.Quarantine.Decision = "revoked"
					expected, step = "quarantine_applying", "observe_quarantine"
				case "retirement_pending", "retirement_pending_expired", "retired":
					q.State = "retirement_pending"
					q.ParkTransitionID = uuid.NewString()
					q.ParkRequestedAt = &stamp
					q.Decision = "revoked"
					expected, step = "retirement_pending", "observe_retirement"
					if stage == "retired" {
						q.State = "retired"
						q.Result = &api.TaskToolRetirementResult{OperationID: record.Execution.OperationID, HandoverID: h.ID, QualificationSHA256: tasktoolrelease.QualificationDigest(q.Qualification), WorkerDeploymentID: record.Execution.Receipt.WorkerDeploymentID, ServingDeploymentID: record.Execution.Receipt.ServingDeploymentID, WorkerIDs: record.Execution.Receipt.WorkerIDs, ParkTransitionID: q.ParkTransitionID, EvidenceSHA256: strings.Repeat("8", 64), ObservedAt: stamp, RecordedAt: stamp}
						expected, step = "retired", "prepare_new_release"
					}
				case "expired":
				case "malformed":
					h.Quarantine.Result.QualificationSHA256 = strings.Repeat("0", 64)
				}
				var writes, cli atomic.Int32
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" {
						writes.Add(1)
						t.Error("recovery mutation")
						w.WriteHeader(500)
						return
					}
					if r.URL.Path == "/v1/task-tool-release-intents/"+record.ID {
						_ = json.NewEncoder(w).Encode(record)
						return
					}
					w.WriteHeader(404)
				}))
				defer backend.Close()
				cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, Toolsets: []string{ToolsetBuildTools}, LocalCLI: func(context.Context, []string, []byte) (LocalCLIResult, error) {
					cli.Add(1)
					t.Error("read-only inspection dispatched local adapter")
					return LocalCLIResult{}, nil
				}}, protocol)
				for _, name := range []string{"gregale_inspect_task_tool_recovery", "gregale_plan_task_tool_recovery"} {
					body := publicationCall(t, ctx, cs, name, taskReleaseIntentArgs{IntentID: record.ID}, stage == "malformed")
					if stage == "malformed" {
						continue
					}
					raw, _ := json.Marshal(body)
					var report tasktoolrelease.RecoveryReport
					if json.Unmarshal(raw, &report) != nil {
						t.Fatal("invalid recovery output")
					}
					if report.Classification != expected || report.Lifecycle == nil || report.NextStep == nil || report.NextStep.Action != step || !report.ReadOnly || report.ReadyToApply || !report.OwnerAuthorizationRequired {
						t.Fatalf("unexpected recovery report %s", raw)
					}
					if stage == "expired" && report.Lifecycle.Retirement != "approval_expired" {
						t.Fatal("expired qualification was treated as current")
					}
					if name == "gregale_plan_task_tool_recovery" {
						for _, a := range report.Actions {
							if a.Action == "resume" && !slices.Contains(a.Blockers, "executor_permanently_fenced") {
								t.Fatal("fence missing", a)
							}
							if stage == "retirement_pending" && a.Action == "retire" && !slices.Contains(a.Blockers, "retirement_park_already_committed") {
								t.Fatal("pending park can repeat")
							}
						}
					}
					if strings.Contains(string(raw), record.Execution.ClaimSHA256) {
						t.Fatal("claim hash leaked into report")
					}
				}
				if writes.Load() != 0 || cli.Load() != 0 {
					t.Fatal("read-only boundary lost")
				}
			})
		}
	}
}

func shiftRecoveryLifecycleClock(t *testing.T, r api.TaskToolReleaseIntentRecord, delta time.Duration) api.TaskToolReleaseIntentRecord {
	t.Helper()
	raw, _ := json.Marshal(r)
	var value any
	if json.Unmarshal(raw, &value) != nil {
		t.Fatal("invalid lifecycle fixture")
	}
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
				return stamp.Add(delta).Format(time.RFC3339Nano)
			}
		}
		return v
	}
	raw, _ = json.Marshal(shift(value))
	if json.Unmarshal(raw, &r) != nil {
		t.Fatal("invalid shifted fixture")
	}
	h := r.Execution.Recovery
	h.Result.QualificationSHA256 = tasktoolrelease.QualificationDigest(*h.Qualification)
	h.Quarantine.Result.QualificationSHA256 = tasktoolrelease.QualificationDigest(h.Quarantine.Qualification)
	return r
}
