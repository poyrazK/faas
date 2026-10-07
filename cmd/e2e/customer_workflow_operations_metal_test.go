//go:build metal

// adr: 609
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
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomerWorkflowOperationResultMetal(t *testing.T) {
	f := newCustomerWorkflowFixture(t, 0)
	id := f.start(t, "hold", "complete")
	privateID := f.waitPrivate(t, id)
	before := f.relay.observation(id)
	if !before.lostAck || before.uploads != 1 || before.lookups < 2 {
		t.Fatal("lost native upload response did not resolve through its receipt")
	}
	f.assertProgress(t, id)
	f.relay.release(id)
	done := f.waitState(t, id, api.OperationSucceeded)
	f.assertResult(t, id, privateID, done)
	other, err := f.owner.CreatePlatformTenant(t.Context(), api.CreatePlatformTenantRequest{ExternalRef: "bob", Name: "Bob"})
	mustCustomerWorkflow(t, err)
	_, err = f.customerToken(t, other.ID).DownloadPlatformTenantSelfOperationArtifact(t.Context(), id, privateID, &bytes.Buffer{})
	if err == nil {
		t.Fatal("another customer downloaded the native result")
	}
	customerWorkflowEventually(t, time.Minute, "independent notification failure", func() bool {
		delivery, err := f.owner.GetOperationDelivery(t.Context(), f.app.Slug, id)
		return err == nil && delivery.BusinessState == api.OperationSucceeded && delivery.LastResponseCode == http.StatusServiceUnavailable && delivery.Attempts > 0 && delivery.State != "succeeded"
	})
	if f.read(t, id).State != api.OperationSucceeded {
		t.Fatal("webhook failure changed business success")
	}
	f.webhookReady.Store(true)
	customerWorkflowEventually(t, 2*time.Minute, "signed completion retry", func() bool {
		delivery, err := f.owner.GetOperationDelivery(t.Context(), f.app.Slug, id)
		return err == nil && delivery.State == "succeeded" && delivery.Attempts >= 2
	})
	if f.webhookInvalid.Load() {
		t.Fatal("invalid completion webhook signature or envelope")
	}
	delivery, err := f.owner.GetOperationDelivery(t.Context(), f.app.Slug, id)
	mustCustomerWorkflow(t, err)
	attempts, err := f.owner.GetOperationDeliveryAttempts(t.Context(), f.app.Slug, id, 10, "")
	mustCustomerWorkflow(t, err)
	if len(attempts.Attempts) < 2 {
		t.Fatal("notification retry attempt ledger missing")
	}
	receipt, ok := f.webhookReceipt.Load().(customerJobWebhookReceipt)
	if !ok || receipt.DeliveryID != delivery.DeliveryID || receipt.Operation.ID != id || receipt.Operation.State != api.OperationSucceeded || receipt.Operation.Generation != 1 || len(receipt.Operation.Artifacts) != 1 {
		t.Fatal("signed completion did not identify the native result")
	}
	f.assertSteps(t, id, 1)
	f.assertStale(t, id, f.relay.observation(id).proof)
}

func TestCustomerWorkflowOperationRecoveryMetal(t *testing.T) {
	f := newCustomerWorkflowFixture(t, 0)
	id := f.start(t, "crash", "recover")
	privateID := f.waitPrivate(t, id)
	old := f.relay.observation(id).proof
	f.relay.release(id) // The real guest process exits before replying.
	f.waitState(t, id, api.OperationRequiresReconciliation)
	before, err := f.store.OperationByID(t.Context(), f.account.ID, f.tenantID, id)
	mustCustomerWorkflow(t, err)
	mustCustomerWorkflow(t, f.h.KillAPID())
	mustCustomerWorkflow(t, f.h.KillSchedd())
	mustCustomerWorkflow(t, f.h.RestartAPID())
	mustCustomerWorkflow(t, f.h.RestartSchedd())
	after, err := f.store.OperationByID(t.Context(), f.account.ID, f.tenantID, id)
	mustCustomerWorkflow(t, err)
	if after.State != before.State || after.LatestSequence != before.LatestSequence || len(after.WorkflowArtifactReceipts) != 1 || after.ArtifactStorageKeys[privateID] != before.ArtifactStorageKeys[privateID] {
		t.Fatal("daemon restart lost the uncertain action or private receipt")
	}
	f.assertStale(t, id, old)
	// A newer default must not replace the admitted workflow's code or inputs.
	newDefault := f.deploy(t, "replacement")
	if newDefault.ID == before.DeploymentID {
		t.Fatal("replacement did not create a new deployment")
	}
	f.writePolicy(t, false)
	inspection, err := f.owner.InspectOperationRecovery(t.Context(), f.app.Slug, id)
	mustCustomerWorkflow(t, err)
	req := api.OperationRecoveryRequest{RecoveryID: "native-workflow-resume", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "fixture process death followed verified private retention; confirmed prefix is pure and no external business effect occurred", ExpectedInspectionRevision: inspection.InspectionRevision}
	first, err := f.owner.RecoverOperationWithReceipt(t.Context(), f.app.Slug, id, req)
	mustCustomerWorkflow(t, err)
	repeated, err := f.owner.RecoverOperationWithReceipt(t.Context(), f.app.Slug, id, req)
	mustCustomerWorkflow(t, err)
	if first.RecoveryID != repeated.RecoveryID || first.RequestFingerprint != repeated.RequestFingerprint || !first.RecordedAt.Equal(repeated.RecordedAt) {
		t.Fatal("recovery replay created another decision")
	}
	done := f.waitState(t, id, api.OperationSucceeded)
	if done.Generation != 2 {
		t.Fatal("approved resume did not advance generation")
	}
	f.assertResult(t, id, privateID, done)
	resumed, err := f.store.OperationByID(t.Context(), f.account.ID, f.tenantID, id)
	mustCustomerWorkflow(t, err)
	if resumed.WorkflowRunID != before.WorkflowRunID || resumed.DeploymentID != before.DeploymentID {
		t.Fatal("approved resume replaced retained run or code pins")
	}
	observed := f.relay.observation(id)
	if observed.uploads != 1 || observed.entered["collect"] != 1 || observed.entered["transform"] != 1 || observed.entered["finish"] != 2 {
		t.Fatal("recovery repeated a confirmed prefix or transferred the file again")
	}
	f.assertSteps(t, id, 2)
	// Use real fresh workload identity so token expiry cannot mask this fence.
	old.token = observed.proof.token
	f.assertStale(t, id, old)
	f.assertStale(t, id, observed.proof)
}

func TestCustomerWorkflowOperationCancellationMetal(t *testing.T) {
	f := newCustomerWorkflowFixture(t, 0)
	mustCustomerWorkflow(t, f.h.KillSchedd())
	queued := f.start(t, "hold", "queued-cancel")
	_, err := f.customer.CancelPlatformTenantSelfOperation(t.Context(), queued, api.OperationCancellationRequest{ExpectedGeneration: 1})
	mustCustomerWorkflow(t, err)
	f.waitState(t, queued, api.OperationCancelled)
	mustCustomerWorkflow(t, f.h.RestartSchedd())
	if len(f.relay.observation(queued).entered) != 0 {
		t.Fatal("queued cancellation dispatched business work")
	}
	id := f.start(t, "hold", "running-cancel")
	privateID := f.waitPrivate(t, id)
	old := f.relay.observation(id).proof
	_, err = f.customer.CancelPlatformTenantSelfOperation(t.Context(), id, api.OperationCancellationRequest{ExpectedGeneration: 1})
	mustCustomerWorkflow(t, err)
	f.waitState(t, id, api.OperationRequiresReconciliation)
	f.assertUnpublished(t, id, privateID)
	f.assertStale(t, id, old)
	f.assertSteps(t, id, 1)
	if len(f.relay.observation(queued).entered) != 0 {
		t.Fatal("queued cancellation dispatched delayed business work")
	}
}

func TestCustomerWorkflowOperationDeadlineMetal(t *testing.T) {
	f := newCustomerWorkflowFixture(t, 12*time.Second)
	id := f.start(t, "hold", "deadline")
	privateID := f.waitPrivate(t, id)
	old := f.relay.observation(id).proof
	control, err := api.NewClient(f.h.APIDURL, old.token).GetWorkflowOperationExecutionControl(t.Context(), id, old.proof)
	mustCustomerWorkflow(t, err)
	f.waitState(t, id, api.OperationRequiresReconciliation)
	if time.Now().Before(control.DeadlineAt) {
		t.Fatal("native action stopped before its captured deadline without injection")
	}
	f.assertUnpublished(t, id, privateID)
	f.assertStale(t, id, old)
	f.assertSteps(t, id, 1)
}

func TestCustomerWorkflowOperationOwnerRevocationMetal(t *testing.T) {
	f := newCustomerWorkflowFixture(t, 0)
	id := f.start(t, "hold", "owner-revoked")
	privateID := f.waitPrivate(t, id)
	old := f.relay.observation(id).proof
	_, err := f.owner.SetPlatformTenantStatus(t.Context(), f.tenantID, api.SetPlatformTenantStatusRequest{Status: string(state.PlatformTenantSuspended)})
	mustCustomerWorkflow(t, err)
	// Account reads remain available after revoking the customer's access.
	customerWorkflowEventually(t, time.Minute, "revoked owner action interruption", func() bool {
		op, err := f.store.OperationByID(context.Background(), f.account.ID, f.tenantID, id)
		return err == nil && op.State == api.OperationRequiresReconciliation
	})
	op, err := f.store.OperationByID(t.Context(), f.account.ID, f.tenantID, id)
	mustCustomerWorkflow(t, err)
	if len(op.Artifacts) != 0 {
		t.Fatal("revoked owner file published")
	}
	_, err = f.customer.DownloadPlatformTenantSelfOperationArtifact(t.Context(), id, privateID, &bytes.Buffer{})
	if err == nil {
		t.Fatal("revoked owner downloaded the file")
	}
	f.assertStale(t, id, old)
	f.assertSteps(t, id, 1)
}

func (f *customerWorkflowFixture) start(t *testing.T, mode, key string) string {
	t.Helper()
	input, _ := json.Marshal(map[string]any{"mode": mode})
	req := api.OperationStartRequest{DefinitionID: f.definition.ID, Input: input}
	first, err := f.customer.StartPlatformTenantSelfOperation(t.Context(), req, key)
	mustCustomerWorkflow(t, err)
	f.mu.Lock()
	f.operationIDs[first.ID] = true
	f.mu.Unlock()
	replay, err := f.customer.StartPlatformTenantSelfOperation(t.Context(), req, key)
	mustCustomerWorkflow(t, err)
	if first.ID == "" || replay.ID != first.ID {
		t.Fatal("duplicate native submission created another operation")
	}
	changed, _ := json.Marshal(map[string]any{"mode": "different"})
	_, err = f.customer.StartPlatformTenantSelfOperation(t.Context(), api.OperationStartRequest{DefinitionID: f.definition.ID, Input: changed}, key)
	var problem *api.APIError
	if !errors.As(err, &problem) || problem.Problem.Status != http.StatusConflict {
		t.Fatal("changed payload did not conflict")
	}
	return first.ID
}

func (f *customerWorkflowFixture) read(t *testing.T, id string) api.OperationResponse {
	t.Helper()
	op, err := f.customer.GetPlatformTenantSelfOperation(t.Context(), id)
	mustCustomerWorkflow(t, err)
	return op
}
func (f *customerWorkflowFixture) waitState(t *testing.T, id string, want api.OperationState) api.OperationResponse {
	t.Helper()
	var op api.OperationResponse
	customerWorkflowEventually(t, 3*time.Minute, "native workflow operation outcome", func() bool {
		var err error
		op, err = f.customer.GetPlatformTenantSelfOperation(t.Context(), id)
		return err == nil && op.State == want
	})
	return op
}
func (f *customerWorkflowFixture) waitPrivate(t *testing.T, id string) string {
	t.Helper()
	customerWorkflowEventually(t, time.Minute, "private final-action file and replayed acknowledgement", func() bool { observed := f.relay.observation(id); return observed.prepared && observed.lookups >= 2 })
	op, err := f.store.OperationByID(t.Context(), f.account.ID, f.tenantID, id)
	mustCustomerWorkflow(t, err)
	receipt, ok := op.WorkflowArtifactReceipts["export-csv"]
	if !ok {
		t.Fatal("native retained receipt missing")
	}
	f.assertUnpublished(t, id, receipt.Artifact.ID)
	return receipt.Artifact.ID
}
func (f *customerWorkflowFixture) assertUnpublished(t *testing.T, id, artifact string) {
	t.Helper()
	op := f.read(t, id)
	if len(op.Artifacts) != 0 || len(op.Result) != 0 {
		t.Fatal("unconfirmed action published output")
	}
	_, err := f.customer.DownloadPlatformTenantSelfOperationArtifact(t.Context(), id, artifact, &bytes.Buffer{})
	if err == nil {
		t.Fatal("private pending file downloadable")
	}
}
func (f *customerWorkflowFixture) assertResult(t *testing.T, id, artifact string, op api.OperationResponse) {
	t.Helper()
	var result struct {
		Rows       int    `json:"rows"`
		ArtifactID string `json:"artifact_id"`
		Version    string `json:"version"`
	}
	mustCustomerWorkflow(t, json.Unmarshal(op.Result, &result))
	if result.Rows != 1 || result.ArtifactID != artifact || result.Version != "original" || len(op.Artifacts) != 1 || op.Artifacts[0].ID != artifact {
		t.Fatal("typed result or admitted code changed")
	}
	var file bytes.Buffer
	_, err := f.customer.DownloadPlatformTenantSelfOperationArtifact(t.Context(), id, artifact, &file)
	mustCustomerWorkflow(t, err)
	if file.String() != customerWorkflowCSV {
		t.Fatal("retained bytes changed")
	}
}
func (f *customerWorkflowFixture) assertSteps(t *testing.T, id string, finishAttempts int) {
	t.Helper()
	op, err := f.store.OperationByID(t.Context(), f.account.ID, f.tenantID, id)
	mustCustomerWorkflow(t, err)
	for _, step := range []string{"collect", "transform", "finish"} {
		attempts, err := f.store.GetWorkflowStepAttempts(t.Context(), op.WorkflowRunID, step)
		mustCustomerWorkflow(t, err)
		want := 1
		if step == "finish" {
			want = finishAttempts
		}
		if len(attempts) != want {
			t.Fatalf("%s attempts = %d, want %d", step, len(attempts), want)
		}
	}
}
func (f *customerWorkflowFixture) assertProgress(t *testing.T, id string) {
	t.Helper()
	op := f.read(t, id)
	if op.Progress == nil || op.Progress.Stage != "finish" || op.State != api.OperationRunning {
		t.Fatal("durable final-step progress missing")
	}
	observed := f.relay.observation(id)
	for _, step := range []string{"collect", "transform", "finish"} {
		if observed.entered[step] != 1 {
			t.Fatal("guest did not execute the three-step chain exactly once")
		}
	}
}
