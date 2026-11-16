package main_test

import (
	"encoding/json"
	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func taskQuarantineCLIRecord(t *testing.T) api.TaskToolReleaseIntentRecord {
	t.Helper()
	now := time.Now().UTC()
	baseline, candidate, worker, previous := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	in := api.TaskToolReleaseIntent{ReleaseFingerprint: strings.Repeat("a", 64), WorkerRuntime: "node", Protocol: api.MCPProtocolVersion, ObserverMetricApp: "observer", Endpoint: "/mcp", AccessMode: "open", ToolScopes: map[string][]string{"report": {}}, RequiredSetup: tasktoolrelease.RequiredSetup(), Reason: "Quarantine CLI replay"}
	for _, role := range []string{"web", "worker", "observer", "previous_worker"} {
		app := role
		if role == "previous_worker" {
			app = "previous"
		}
		review := api.AppReleaseReview{AppID: uuid.NewString(), Scope: api.DefaultEnvScope, ConfigurationHash: strings.Repeat("b", 64), ServingDeploymentIDs: []string{}}
		if role == "web" {
			review.LatestDeploymentID = baseline
			review.ServingDeploymentIDs = []string{baseline}
		}
		if role == "previous_worker" {
			review.LatestDeploymentID = previous
		}
		in.Apps = append(in.Apps, api.TaskToolReleaseApp{Role: role, App: app, Review: review})
	}
	r, err := tasktoolrelease.New(uuid.NewString(), uuid.NewString(), strings.Repeat("1", 64), in, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	stamp := now.Add(-10 * time.Second)
	r.Execution = &api.TaskToolReleaseExecution{OperationID: strings.Repeat("2", 64), BindingsSHA256: strings.Repeat("3", 64), AuthorizerKeyID: uuid.NewString(), State: "reconcile_required", AuthorizedAt: r.CreatedAt, FenceVersion: 1, ExecutorExitedAt: &stamp}
	receipt := &api.TaskToolReleaseReceipt{Revision: 1, ReleaseID: tasktoolrelease.ExecutionReleaseID(r), Stage: "web_promoted", ServingCaptured: true, ServingDeploymentID: baseline, WebDeploymentID: candidate, WorkerDeploymentID: worker, WorkerIDs: []string{uuid.NewString()}, Promoted: true, PreviousDeployments: map[string]string{"previous": previous}, Parked: map[string]bool{}}
	r.Execution.Receipt = receipt
	r.Execution.CompletedStages = tasktoolrelease.ReceiptStages(*receipt)
	q := api.TaskToolRecoveryQualification{HandoverID: strings.Repeat("4", 64), OperationID: r.Execution.OperationID, Revision: 1, Action: "restore", TargetDeploymentID: baseline, ExpectedServingDeploymentID: candidate, BindingsSHA256: r.Execution.BindingsSHA256, EvidenceSHA256: strings.Repeat("5", 64), NativeApps: append([]api.TaskToolReleaseApp{}, in.Apps...), ObservedAt: stamp, ExpiresAt: stamp.Add(api.TaskToolRecoveryApprovalTTL)}
	for i := range q.NativeApps {
		if q.NativeApps[i].Role == "web" {
			q.NativeApps[i].Review.LatestDeploymentID = candidate
			q.NativeApps[i].Review.ServingDeploymentIDs = []string{candidate}
		}
		if q.NativeApps[i].Role == "worker" {
			q.NativeApps[i].Review.LatestDeploymentID = worker
		}
	}
	h := &api.TaskToolRecoveryHandover{ID: q.HandoverID, OperationID: q.OperationID, Revision: 1, State: "settled", RequestedAt: stamp, SettledAt: &stamp, Qualification: &q}
	r.Execution.Recovery = h
	h.Result = &api.TaskToolRecoveryResult{IntentID: r.ID, OperationID: q.OperationID, HandoverID: q.HandoverID, QualificationSHA256: tasktoolrelease.QualificationDigest(q), Revision: 1, Action: "restore", TargetDeploymentID: baseline, PreviousServingDeploymentID: candidate, EvidenceSHA256: strings.Repeat("6", 64), CheckedAt: stamp, CommittedAt: stamp}
	qual := q
	qual.Action = "quarantine"
	qual.TargetDeploymentID = worker
	qual.ExpectedServingDeploymentID = baseline
	qual.NativeApps = append([]api.TaskToolReleaseApp{}, q.NativeApps...)
	for i := range qual.NativeApps {
		if qual.NativeApps[i].Role == "web" {
			qual.NativeApps[i].Review.ServingDeploymentIDs = []string{baseline}
		}
	}
	h.Quarantine = &api.TaskToolQuarantine{Qualification: qual, State: "quarantined", Decision: "revoked", AuthorizerKeyID: r.Execution.AuthorizerKeyID, DecidedAt: &stamp, AttemptSHA256: strings.Repeat("7", 64), ClaimedAt: &stamp, Result: &api.TaskToolQuarantineResult{OperationID: q.OperationID, HandoverID: q.HandoverID, QualificationSHA256: tasktoolrelease.QualificationDigest(qual), WorkerDeploymentID: worker, ServingDeploymentID: baseline, WorkerIDs: receipt.WorkerIDs, EvidenceSHA256: strings.Repeat("8", 64), ObservedAt: stamp, RecordedAt: stamp}}
	return r
}
func TestMCPTaskQuarantineCLIRetainedResultAndNoApprovalNoWrites(t *testing.T) {
	r := taskQuarantineCLIRecord(t)
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
	args := []string{"--json", "mcp", "server", "tasks", "execution", "quarantine", "--id", r.ID, "--operation-id", r.Execution.OperationID, "--handover-id", r.Execution.Recovery.ID, "--qualification-sha256", tasktoolrelease.QualificationDigest(r.Execution.Recovery.Quarantine.Qualification), "--allow-tool-execution"}
	code, out, stderr := runCLI(t, args...)
	if code != 0 || posts != 0 || !strings.Contains(out, "quarantined") {
		t.Fatalf("retained observation required private files %d %s %s", code, out, stderr)
	}
	q := r.Execution.Recovery.Quarantine
	q.State = "qualified"
	q.ClaimedAt = nil
	q.AttemptSHA256 = ""
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
