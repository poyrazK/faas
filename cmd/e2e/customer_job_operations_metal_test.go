//go:build metal

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

// These tests use real apid/schedd/vmmd subprocesses and the real guest exit
// channel. The per-instance vsock relay only transports HTTP to loopback apid.
func TestCustomerJobOperationDirectUploadMetal(t *testing.T) {
	f := newCustomerJobFixture(t)
	id := f.start(t, "direct-hold", "direct-export")
	relay, run := f.connect(t, id, true)
	f.waitProgress(t, id)
	customerJobEventually(t, time.Minute, "direct private guest upload", relay.prepared.Load)
	pending, err := f.customer.GetPlatformTenantSelfOperation(t.Context(), id)
	mustCustomerJob(t, err)
	privateID := operations.ArtifactIdentity(id, run, 1, f.artifact.ReportID)
	if pending.State != api.OperationRunning || len(pending.Artifacts) != 0 || len(pending.Result) != 0 {
		t.Fatal("direct file published before task exit")
	}
	if _, err := f.customer.DownloadPlatformTenantSelfOperationArtifact(t.Context(), id, privateID, &bytes.Buffer{}); err == nil {
		t.Fatal("unconfirmed direct file downloadable")
	}
	relay.release.Store(true)
	done := f.waitState(t, id, api.OperationSucceeded)
	if len(done.Artifacts) != 1 || done.Artifacts[0].URI != "operation://"+id+"/artifacts/"+privateID || !relay.lostAck.Load() || relay.receiptRequests.Load() < 1 {
		t.Fatal("direct receipt or lost acknowledgement recovery missing")
	}
	var file bytes.Buffer
	_, err = f.customer.DownloadPlatformTenantSelfOperationArtifact(t.Context(), id, privateID, &file)
	mustCustomerJob(t, err)
	if file.String() != customerJobCSV || f.sourceReads.Load() != 0 {
		t.Fatal("direct upload depended on managed source")
	}
	_, err = f.otherCustomer(t).DownloadPlatformTenantSelfOperationArtifact(t.Context(), id, privateID, &bytes.Buffer{})
	if err == nil {
		t.Fatal("cross-customer direct download")
	}
	f.waitDeliveryFailure(t, id)
	observed, err := f.customer.GetPlatformTenantSelfOperation(t.Context(), id)
	mustCustomerJob(t, err)
	if observed.State != api.OperationSucceeded {
		t.Fatal("notification failure changed business result")
	}
	f.assertRuns(t, id, 1)
	f.assertTask(t, run, "succeeded")
	f.assertStaleProof(t, id, relay.proof())
	if _, err := api.NewClient(f.h.APIDURL, "").ReuseJobOperationUpload(t.Context(), id, relay.proof(), api.OperationArtifactUploadRequest{ReportID: f.artifact.ReportID, Name: f.artifact.Name, SizeBytes: f.artifact.SizeBytes, SHA256: f.artifact.SHA256}); err == nil {
		t.Fatal("closed upload proof accepted")
	}
	f.waitNoInstances(t)
}

func TestCustomerJobOperationResultMetal(t *testing.T) {
	f := newCustomerJobFixture(t)
	id := f.start(t, "hold", "export")
	relay, run := f.connect(t, id, true)
	f.waitProgress(t, id)
	customerJobEventually(t, time.Minute, "private guest result preparation", relay.prepared.Load)
	pending, err := f.customer.GetPlatformTenantSelfOperation(t.Context(), id)
	mustCustomerJob(t, err)
	if pending.State != api.OperationRunning || len(pending.Artifacts) != 0 || len(pending.Result) != 0 {
		t.Fatal("private preparation exposed a business result")
	}
	privateID := operations.ArtifactIdentity(id, run, 1, f.artifact.ReportID)
	if _, err := f.customer.DownloadPlatformTenantSelfOperationArtifact(t.Context(), id, privateID, &bytes.Buffer{}); err == nil {
		t.Fatal("prepared private file was downloadable before native exit")
	}
	relay.release.Store(true)
	done := f.waitState(t, id, api.OperationSucceeded)
	var result struct {
		File string `json:"file"`
	}
	mustCustomerJob(t, json.Unmarshal(done.Result, &result))
	if len(done.Artifacts) != 1 || result.File != "export.csv" || !relay.lostAck.Load() || relay.receiptRequests.Load() < 1 {
		t.Fatal("native result or lost acknowledgement recovery missing")
	}
	f.sourceMissing.Store(true)
	var file bytes.Buffer
	_, err = f.customer.DownloadPlatformTenantSelfOperationArtifact(t.Context(), id, done.Artifacts[0].ID, &file)
	mustCustomerJob(t, err)
	if file.String() != customerJobCSV || f.sourceReads.Load() != 1 {
		t.Fatal("private file was reread or its contents changed")
	}
	other := f.otherCustomer(t)
	_, err = other.DownloadPlatformTenantSelfOperationArtifact(t.Context(), id, done.Artifacts[0].ID, &bytes.Buffer{})
	if err == nil {
		t.Fatal("another customer downloaded the result")
	}
	f.waitDeliveryFailure(t, id)
	observed, err := f.customer.GetPlatformTenantSelfOperation(t.Context(), id)
	mustCustomerJob(t, err)
	if observed.State != api.OperationSucceeded {
		t.Fatal("notification failure changed business success")
	}
	f.webhookReady.Store(true)
	f.waitDeliverySuccess(t, id)
	f.assertRuns(t, id, 1)
	f.assertTask(t, run, "succeeded")
	f.assertStaleProof(t, id, relay.proof())
	f.waitNoInstances(t)
}

func TestCustomerJobOperationRestartMetal(t *testing.T) {
	f := newCustomerJobFixture(t)
	id := f.start(t, "hold", "restart")
	relay, run := f.connect(t, id, false)
	f.waitProgress(t, id)
	customerJobEventually(t, time.Minute, "private guest result preparation", relay.prepared.Load)
	before, err := f.customer.GetPlatformTenantSelfOperation(t.Context(), id)
	mustCustomerJob(t, err)
	mustCustomerJob(t, f.h.KillAPID())
	f.h.MustKillSchedd(t)
	mustCustomerJob(t, f.h.RestartAPID())
	f.h.MustRestartSchedd(t)
	after, err := f.customer.GetPlatformTenantSelfOperation(t.Context(), id)
	mustCustomerJob(t, err)
	if after.State != api.OperationRunning || after.Progress == nil || after.Progress.Stage != "generating" || after.LatestSequence != before.LatestSequence {
		t.Fatal("restart lost durable guest progress")
	}
	// Closing new admission must not strand an already accepted native run.
	f.writePolicy(t, false)
	relay.release.Store(true)
	done := f.waitState(t, id, api.OperationSucceeded)
	if done.Generation != 1 || len(done.Artifacts) != 1 {
		t.Fatal("restart lost the accepted operation")
	}
	f.assertRuns(t, id, 1)
	f.assertTask(t, run, "succeeded")
	f.assertStaleProof(t, id, relay.proof())
	f.waitNoInstances(t)
}

func TestCustomerJobOperationRecoveryMetal(t *testing.T) {
	f := newCustomerJobFixture(t)
	id := f.start(t, "uncertain", "recover")
	first, run := f.connect(t, id, false)
	failed := f.waitState(t, id, api.OperationRequiresReconciliation)
	if !first.prepared.Load() || len(failed.Artifacts) != 0 || len(failed.Result) != 0 || failed.FailureCode != "execution_outcome_unknown" {
		t.Fatal("uncertain native effect was published or silently retried")
	}
	f.assertRuns(t, id, 1)
	f.assertTask(t, run, "failed")
	f.assertStaleProof(t, id, first.proof())
	f.waitNoInstances(t)
	changedCommand := []string{"/job-fixture", "fail"}
	_, err := f.owner.UpdateJob(t.Context(), f.definition.Spec.Job, api.UpdateJobRequest{Command: changedCommand})
	mustCustomerJob(t, err)
	inspection, err := f.owner.InspectOperationRecovery(t.Context(), f.app.Slug, id)
	mustCustomerJob(t, err)
	if inspection.ExecutionKind != "job" || inspection.InspectionRevision == "" || len(inspection.Artifacts) != 1 || !inspection.Artifacts[0].Retained || inspection.Artifacts[0].State != "prepared" {
		t.Fatal("missing native recovery inspection")
	}
	// Recovery uses retained admission and the immutable original run snapshot.
	f.writePolicy(t, false)
	req := api.OperationRecoveryRequest{RecoveryID: "approved-native-retry", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "fixture failure was injected after private preparation; no external business effect", ExpectedInspectionRevision: inspection.InspectionRevision}
	decision, err := f.owner.RecoverOperationWithReceipt(t.Context(), f.app.Slug, id, req)
	mustCustomerJob(t, err)
	replay, err := f.owner.RecoverOperationWithReceipt(t.Context(), f.app.Slug, id, req)
	mustCustomerJob(t, err)
	if decision.Generation != 2 || replay.JobRunID != decision.JobRunID || decision.JobRunID == run {
		t.Fatal("recovery did not provide one stable new execution")
	}
	_, next := f.connect(t, id, false)
	done := f.waitState(t, id, api.OperationSucceeded)
	if done.Generation != 2 || len(done.Artifacts) != 1 || done.Artifacts[0].ID == inspection.Artifacts[0].ID || f.sourceReads.Load() != 2 {
		t.Fatal("approved recovery failed to publish the new generation")
	}
	f.assertRuns(t, id, 2)
	f.assertTask(t, next, "succeeded")
	f.assertStaleProof(t, id, first.proof())
	f.waitNoInstances(t)
}

func TestCustomerJobOperationCancellationMetal(t *testing.T) {
	f := newCustomerJobFixture(t)
	f.h.MustKillSchedd(t)
	queued := f.start(t, "cancel", "cancel-queued")
	_, err := f.customer.CancelPlatformTenantSelfOperation(t.Context(), queued, api.OperationCancellationRequest{ExpectedGeneration: 1})
	mustCustomerJob(t, err)
	f.waitState(t, queued, api.OperationCancelled)
	operation, err := f.store.OperationByID(t.Context(), f.account.ID, f.tenant.ID, queued)
	mustCustomerJob(t, err)
	task, err := f.store.JobTaskGet(t.Context(), operation.JobRunID, 0)
	mustCustomerJob(t, err)
	if task.Status != "cancelled" || task.Attempt != 0 || task.StartedAt != nil {
		t.Fatal("queued cancellation started native work")
	}
	f.h.MustRestartSchedd(t)
	f.waitNoInstances(t)
	f.assertRuns(t, queued, 1)
	running := f.start(t, "cancel", "cancel-running")
	relay, run := f.connect(t, running, false)
	f.waitProgress(t, running)
	_, err = f.customer.CancelPlatformTenantSelfOperation(t.Context(), running, api.OperationCancellationRequest{ExpectedGeneration: 1})
	mustCustomerJob(t, err)
	done := f.waitState(t, running, api.OperationRequiresReconciliation)
	if !done.CancellationRequested || len(done.Artifacts) != 0 {
		t.Fatal("running cancellation lost its uncertain outcome")
	}
	f.assertTask(t, run, "failed")
	f.assertRuns(t, running, 1)
	f.assertStaleProof(t, running, relay.proof())
	f.waitNoInstances(t)
}

func (f *customerJobFixture) start(t *testing.T, mode, key string) string {
	t.Helper()
	payload := map[string]any{"mode": mode, "artifact": f.artifact}
	if mode == "direct-hold" {
		payload["artifact_data"] = customerJobCSV
	}
	input, err := json.Marshal(payload)
	mustCustomerJob(t, err)
	req := api.OperationStartRequest{DefinitionID: f.definition.ID, Input: input}
	first, err := f.customer.StartPlatformTenantSelfOperation(t.Context(), req, key)
	mustCustomerJob(t, err)
	f.operationIDs = append(f.operationIDs, first.ID)
	repeated, err := f.customer.StartPlatformTenantSelfOperation(t.Context(), req, key)
	mustCustomerJob(t, err)
	if first.ID == "" || first.ID != repeated.ID {
		t.Fatal("duplicate submission created a second logical operation")
	}
	conflicting, err := json.Marshal(map[string]any{"mode": "changed-input", "artifact": f.artifact})
	mustCustomerJob(t, err)
	_, err = f.customer.StartPlatformTenantSelfOperation(t.Context(), api.OperationStartRequest{DefinitionID: f.definition.ID, Input: conflicting}, key)
	var problem *api.APIError
	if !errors.As(err, &problem) || problem.Problem.Status != http.StatusConflict {
		t.Fatal("changed input reused a submission key without a conflict")
	}
	return first.ID
}

func (f *customerJobFixture) waitState(t *testing.T, id string, want api.OperationState) api.OperationResponse {
	t.Helper()
	var found api.OperationResponse
	customerJobEventually(t, 3*time.Minute, "customer business state "+string(want), func() bool {
		var err error
		found, err = f.customer.GetPlatformTenantSelfOperation(t.Context(), id)
		return err == nil && found.State == want
	})
	mustCustomerJob(t, f.rememberResultFiles(t.Context(), id))
	return found
}
func (f *customerJobFixture) waitProgress(t *testing.T, id string) {
	t.Helper()
	customerJobEventually(t, 2*time.Minute, "guest progress", func() bool {
		o, err := f.customer.GetPlatformTenantSelfOperation(t.Context(), id)
		return err == nil && o.Progress != nil && o.Progress.Stage == "generating"
	})
}
func (f *customerJobFixture) assertRuns(t *testing.T, id string, want int) {
	t.Helper()
	executions, err := f.owner.GetOperationExecutions(t.Context(), f.app.Slug, id, 0, 10)
	mustCustomerJob(t, err)
	if len(executions.Executions) != want {
		t.Fatalf("operation has %d executions, want %d", len(executions.Executions), want)
	}
	for _, execution := range executions.Executions {
		if execution.JobRunID == "" {
			t.Fatal("execution did not retain native job linkage")
		}
	}
}
func (f *customerJobFixture) assertTask(t *testing.T, run, status string) {
	t.Helper()
	task, err := f.store.JobTaskGet(context.Background(), run, 0)
	mustCustomerJob(t, err)
	if task.Status != status || task.Attempt != 1 {
		t.Fatalf("native task state=%s attempt=%d, want %s attempt 1", task.Status, task.Attempt, status)
	}
}
