package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func taskRetirementCLIRecord(t *testing.T) api.TaskToolReleaseIntentRecord {
	t.Helper()
	r := taskQuarantineCLIRecord(t)
	q := r.Execution.Recovery.Quarantine
	stamp := time.Now().UTC()
	qual := q.Qualification
	qual.Action = "retire"
	qual.ObservedAt = stamp
	qual.ExpiresAt = stamp.Add(api.TaskToolRecoveryApprovalTTL)
	transition := uuid.NewString()
	r.Execution.Recovery.Retirement = &api.TaskToolRetirement{Qualification: qual, State: "retired", Decision: "revoked", AuthorizerKeyID: r.Execution.AuthorizerKeyID, DecidedAt: &stamp, ParkRequestedAt: &stamp, ParkTransitionID: transition, Result: &api.TaskToolRetirementResult{OperationID: r.Execution.OperationID, HandoverID: r.Execution.Recovery.ID, QualificationSHA256: tasktoolrelease.QualificationDigest(qual), WorkerDeploymentID: r.Execution.Receipt.WorkerDeploymentID, ServingDeploymentID: r.Execution.Receipt.ServingDeploymentID, WorkerIDs: r.Execution.Receipt.WorkerIDs, ParkTransitionID: transition, EvidenceSHA256: strings.Repeat("9", 64), ObservedAt: stamp, RecordedAt: stamp}}
	return r
}
func TestMCPTaskRetirementCLIRetainedResultAndNoApprovalNoWrites(t *testing.T) {
	r := taskRetirementCLIRecord(t)
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" {
			posts++
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(r)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fixture")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Cleanup(main.ResetCapabilityView)
	args := []string{"--json", "mcp", "server", "tasks", "execution", "retire", "--id", r.ID, "--operation-id", r.Execution.OperationID, "--handover-id", r.Execution.Recovery.ID, "--qualification-sha256", tasktoolrelease.QualificationDigest(r.Execution.Recovery.Retirement.Qualification), "--allow-tool-execution"}
	code, out, stderr := runCLI(t, args...)
	if code != 0 || posts != 0 || !strings.Contains(out, "retired") {
		t.Fatalf("retained observation required private files %d %s %s", code, out, stderr)
	}
	q := r.Execution.Recovery.Retirement
	q.State = "qualified"
	q.ParkRequestedAt = nil
	q.ParkTransitionID = ""
	q.Result = nil
	code, _, _ = runCLI(t, args...)
	if code == 0 || posts != 0 {
		t.Fatal("revoked quarantine dispatched SQL")
	}
	q.Decision = "approved"
	code, _, _ = runCLI(t, args...)
	if code == 0 || posts != 0 {
		t.Fatal("missing profile dispatched SQL")
	}
}
