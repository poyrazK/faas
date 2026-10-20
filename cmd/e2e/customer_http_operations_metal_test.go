//go:build metal

// adr: 521
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomerHTTPOperationResultMetal(t *testing.T) {
	f := newCustomerHTTPFixture(t)
	id := f.start(t, "hold", "complete")
	privateID := f.waitPrivate(t, id)
	before := f.relay.observation(id)
	if !before.lostAck || before.uploads != 1 || before.lookups < 2 {
		t.Fatal("lost HTTP upload response did not resolve through its receipt")
	}
	f.assertProgress(t, id)
	f.relay.direct(id, customerHTTPRelease)
	done := f.waitState(t, id, api.OperationSucceeded)
	f.assertResult(t, id, privateID, done)
	other, err := f.owner.CreatePlatformTenant(t.Context(), api.CreatePlatformTenantRequest{ExternalRef: "bob", Name: "Bob"})
	mustCustomerHTTP(t, err)
	_, err = f.customerToken(t, other.ID).DownloadPlatformTenantSelfOperationArtifact(t.Context(), id, privateID, &bytes.Buffer{})
	if err == nil {
		t.Fatal("another customer downloaded the HTTP operation result")
	}
	customerHTTPEventually(t, time.Minute, "independent notification failure", func() bool {
		delivery, err := f.owner.GetOperationDelivery(t.Context(), f.app.Slug, id)
		return err == nil && delivery.BusinessState == api.OperationSucceeded && delivery.LastResponseCode == http.StatusServiceUnavailable && delivery.Attempts > 0 && delivery.State != "succeeded"
	})
	if f.read(t, id).State != api.OperationSucceeded {
		t.Fatal("webhook failure changed business success")
	}
	f.webhookReady.Store(true)
	customerHTTPEventually(t, 2*time.Minute, "signed completion retry", func() bool {
		delivery, err := f.owner.GetOperationDelivery(t.Context(), f.app.Slug, id)
		return err == nil && delivery.State == "succeeded" && delivery.Attempts >= 2
	})
	if f.webhookInvalid.Load() {
		t.Fatal("invalid completion webhook signature or envelope")
	}
	delivery, err := f.owner.GetOperationDelivery(t.Context(), f.app.Slug, id)
	mustCustomerHTTP(t, err)
	attempts, err := f.owner.GetOperationDeliveryAttempts(t.Context(), f.app.Slug, id, 10, "")
	mustCustomerHTTP(t, err)
	if len(attempts.Attempts) < 2 {
		t.Fatal("notification retry attempt ledger missing")
	}
	receipt, ok := f.webhookReceipt.Load().(customerJobWebhookReceipt)
	if !ok || receipt.DeliveryID != delivery.DeliveryID || receipt.Operation.ID != id || receipt.Operation.State != api.OperationSucceeded || receipt.Operation.Generation != 1 || len(receipt.Operation.Artifacts) != 1 {
		t.Fatal("signed completion did not identify the HTTP result")
	}
	// Notification retries never re-executed the handler.
	f.assertExecutions(t, id, 1)
	if entered := f.relay.observation(id).entered; len(entered) != 1 || entered[before.proof.proof.InvocationID] != 1 {
		t.Fatal("delivery retry re-executed business work")
	}
	f.assertStale(t, id, before.proof)
}

func TestCustomerHTTPOperationRecoveryMetal(t *testing.T) {
	f := newCustomerHTTPFixture(t)
	id := f.start(t, "hold", "recover")
	firstArtifact := f.waitPrivate(t, id)
	old := f.relay.observation(id).proof
	f.relay.direct(id, customerHTTPCrash) // The real guest process exits before replying.
	f.waitState(t, id, api.OperationRequiresReconciliation)
	before, err := f.store.OperationByID(t.Context(), f.account.ID, f.tenantID, id)
	mustCustomerHTTP(t, err)
	// Either classification means the outcome is uncertain: a transport loss,
	// or a gateway error response that cannot satisfy the output schema.
	t.Logf("process death recorded failure code %q", before.FailureCode)
	if before.FailureCode != "execution_outcome_unknown" && before.FailureCode != "invalid_output" {
		t.Fatalf("process death failure code = %q, want an uncertain outcome", before.FailureCode)
	}
	mustCustomerHTTP(t, f.h.KillAPID())
	mustCustomerHTTP(t, f.h.KillSchedd())
	mustCustomerHTTP(t, f.h.RestartAPID())
	mustCustomerHTTP(t, f.h.RestartSchedd())
	after, err := f.store.OperationByID(t.Context(), f.account.ID, f.tenantID, id)
	mustCustomerHTTP(t, err)
	if before.ArtifactStorageKeys[firstArtifact] == "" || after.State != before.State || after.LatestSequence != before.LatestSequence || after.CurrentInvocationID != before.CurrentInvocationID || after.ArtifactStorageKeys[firstArtifact] != before.ArtifactStorageKeys[firstArtifact] {
		t.Fatal("daemon restart lost the uncertain dispatch or private receipt")
	}
	f.assertUnpublished(t, id, firstArtifact)
	f.assertStale(t, id, old)
	// A newer default must not replace the admitted operation's code or input.
	newDefault := f.deploy(t, "replacement")
	if newDefault.ID == before.DeploymentID {
		t.Fatal("replacement did not create a new deployment")
	}
	f.writePolicy(t, false)
	_, err = f.customer.StartPlatformTenantSelfOperation(t.Context(), api.OperationStartRequest{DefinitionID: f.definition.ID, Input: json.RawMessage(`{"mode":"closed"}`)}, "closed-admission")
	if err == nil {
		t.Fatal("closed preview admitted new HTTP work")
	}
	inspection, err := f.owner.InspectOperationRecovery(t.Context(), f.app.Slug, id)
	mustCustomerHTTP(t, err)
	if inspection.ExecutionKind != "http" || inspection.InvocationID != before.CurrentInvocationID || inspection.DeploymentID != before.DeploymentID {
		t.Fatal("recovery inspection lost the HTTP execution identity or code pin")
	}
	f.relay.direct(id, customerHTTPRelease)
	req := api.OperationRecoveryRequest{RecoveryID: "native-http-resume", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "fixture process death followed verified private retention; the handler has no external business effect", ExpectedInspectionRevision: inspection.InspectionRevision}
	first, err := f.owner.RecoverOperationWithReceipt(t.Context(), f.app.Slug, id, req)
	mustCustomerHTTP(t, err)
	repeated, err := f.owner.RecoverOperationWithReceipt(t.Context(), f.app.Slug, id, req)
	mustCustomerHTTP(t, err)
	if first.RecoveryID != repeated.RecoveryID || first.RequestFingerprint != repeated.RequestFingerprint || !first.RecordedAt.Equal(repeated.RecordedAt) {
		t.Fatal("recovery replay created another decision")
	}
	done := f.waitState(t, id, api.OperationSucceeded)
	if done.Generation != 2 {
		t.Fatal("approved recovery did not advance generation")
	}
	resumed, err := f.store.OperationByID(t.Context(), f.account.ID, f.tenantID, id)
	mustCustomerHTTP(t, err)
	if resumed.DeploymentID != before.DeploymentID || resumed.CurrentInvocationID == before.CurrentInvocationID {
		t.Fatal("approved recovery replaced the code pin or reused the uncertain execution")
	}
	// HTTP files bind the invocation attempt, so generation 2 uploads its own
	// copy; the uncertain generation-1 copy is never published.
	var secondArtifact string
	if len(done.Artifacts) == 1 {
		secondArtifact = done.Artifacts[0].ID
	}
	if secondArtifact == "" || secondArtifact == firstArtifact {
		t.Fatal("generation 2 did not publish its own verified file")
	}
	f.assertResult(t, id, secondArtifact, done)
	if _, err := f.customer.DownloadPlatformTenantSelfOperationArtifact(t.Context(), id, firstArtifact, &bytes.Buffer{}); err == nil {
		t.Fatal("uncertain generation-1 file was published")
	}
	observed := f.relay.observation(id)
	if len(observed.entered) != 2 || observed.entered[before.CurrentInvocationID] != 1 || observed.entered[resumed.CurrentInvocationID] != 1 || observed.uploads != 2 {
		t.Fatal("recovery did not execute exactly one fresh handler with one fresh transfer")
	}
	f.assertExecutions(t, id, 2)
	// Use real fresh workload identity so token expiry cannot mask this fence.
	old.token = observed.proof.token
	f.assertStale(t, id, old)
	f.assertStale(t, id, observed.proof)
}

func TestCustomerHTTPOperationCancellationMetal(t *testing.T) {
	f := newCustomerHTTPFixture(t)
	mustCustomerHTTP(t, f.h.KillSchedd())
	queued := f.start(t, "hold", "queued-cancel")
	_, err := f.customer.CancelPlatformTenantSelfOperation(t.Context(), queued, api.OperationCancellationRequest{ExpectedGeneration: 1})
	mustCustomerHTTP(t, err)
	f.waitState(t, queued, api.OperationCancelled)
	mustCustomerHTTP(t, f.h.RestartSchedd())
	if len(f.relay.observation(queued).entered) != 0 {
		t.Fatal("queued cancellation dispatched business work")
	}
	id := f.start(t, "hold", "running-cancel")
	privateID := f.waitPrivate(t, id)
	old := f.relay.observation(id).proof
	_, err = f.customer.CancelPlatformTenantSelfOperation(t.Context(), id, api.OperationCancellationRequest{ExpectedGeneration: 1})
	mustCustomerHTTP(t, err)
	// The handler observes cancellation and stops; an uncertain external
	// effect still requires reconciliation rather than a cancelled outcome.
	f.waitState(t, id, api.OperationRequiresReconciliation)
	f.assertUnpublished(t, id, privateID)
	f.assertStale(t, id, old)
	f.assertExecutions(t, id, 1)
	if len(f.relay.observation(queued).entered) != 0 {
		t.Fatal("queued cancellation dispatched delayed business work")
	}
}

func TestCustomerHTTPOperationOwnerRevocationMetal(t *testing.T) {
	f := newCustomerHTTPFixture(t)
	id := f.start(t, "hold", "owner-revoked")
	privateID := f.waitPrivate(t, id)
	_, err := f.owner.SetPlatformTenantStatus(t.Context(), f.tenantID, api.SetPlatformTenantStatusRequest{Status: string(state.PlatformTenantSuspended)})
	mustCustomerHTTP(t, err)
	if _, err := f.customer.GetPlatformTenantSelfOperation(t.Context(), id); err == nil {
		t.Fatal("suspended customer read the operation")
	}
	if _, err := f.customer.StartPlatformTenantSelfOperation(t.Context(), api.OperationStartRequest{DefinitionID: f.definition.ID, Input: json.RawMessage(`{"mode":"after-suspension"}`)}, "after-suspension"); err == nil {
		t.Fatal("suspended customer admitted new HTTP work")
	}
	// Suspension revokes customer access; it does not prove an admitted
	// handler's effects failed. The account retains the settled outcome.
	f.relay.direct(id, customerHTTPRelease)
	customerHTTPEventually(t, 3*time.Minute, "settled outcome after suspension", func() bool {
		op, err := f.owner.GetOperation(t.Context(), f.app.Slug, id)
		return err == nil && op.State == api.OperationSucceeded
	})
	if _, err := f.customer.DownloadPlatformTenantSelfOperationArtifact(t.Context(), id, privateID, &bytes.Buffer{}); err == nil {
		t.Fatal("suspended customer downloaded the result")
	}
	var file bytes.Buffer
	_, err = f.owner.DownloadOperationArtifact(t.Context(), f.app.Slug, id, privateID, &file)
	mustCustomerHTTP(t, err)
	if file.String() != customerHTTPCSV {
		t.Fatal("account-retained bytes changed")
	}
	f.assertExecutions(t, id, 1)
}

func (f *customerHTTPFixture) start(t *testing.T, mode, key string) string {
	t.Helper()
	input, _ := json.Marshal(map[string]any{"mode": mode})
	req := api.OperationStartRequest{DefinitionID: f.definition.ID, Input: input}
	first, err := f.customer.StartPlatformTenantSelfOperation(t.Context(), req, key)
	mustCustomerHTTP(t, err)
	f.mu.Lock()
	f.operationIDs[first.ID] = true
	f.mu.Unlock()
	replay, err := f.customer.StartPlatformTenantSelfOperation(t.Context(), req, key)
	mustCustomerHTTP(t, err)
	if first.ID == "" || replay.ID != first.ID {
		t.Fatal("duplicate HTTP submission created another operation")
	}
	changed, _ := json.Marshal(map[string]any{"mode": "different"})
	_, err = f.customer.StartPlatformTenantSelfOperation(t.Context(), api.OperationStartRequest{DefinitionID: f.definition.ID, Input: changed}, key)
	var problem *api.APIError
	if !errors.As(err, &problem) || problem.Problem.Status != http.StatusConflict {
		t.Fatal("changed payload did not conflict")
	}
	return first.ID
}

func (f *customerHTTPFixture) read(t *testing.T, id string) api.OperationResponse {
	t.Helper()
	op, err := f.customer.GetPlatformTenantSelfOperation(t.Context(), id)
	mustCustomerHTTP(t, err)
	return op
}

func (f *customerHTTPFixture) waitState(t *testing.T, id string, want api.OperationState) api.OperationResponse {
	t.Helper()
	var op api.OperationResponse
	customerHTTPEventually(t, 3*time.Minute, "native HTTP operation outcome", func() bool {
		var err error
		op, err = f.customer.GetPlatformTenantSelfOperation(t.Context(), id)
		return err == nil && op.State == want
	})
	return op
}

// waitPrivate waits until the real guest retained its file, resolved the
// injected lost acknowledgement and reported its final progress stage.
func (f *customerHTTPFixture) waitPrivate(t *testing.T, id string) string {
	t.Helper()
	var artifact string
	customerHTTPEventually(t, 2*time.Minute, "private HTTP file and replayed acknowledgement", func() bool {
		observed := f.relay.observation(id)
		if !observed.lostAck || observed.lookups < 2 {
			return false
		}
		op, err := f.store.OperationByID(t.Context(), f.account.ID, f.tenantID, id)
		if err != nil || len(op.Artifacts) != 1 || op.Progress == nil || op.Progress.Stage != "uploading" {
			return false
		}
		artifact = op.Artifacts[0].ID
		return true
	})
	f.assertUnpublished(t, id, artifact)
	return artifact
}

func (f *customerHTTPFixture) assertUnpublished(t *testing.T, id, artifact string) {
	t.Helper()
	op := f.read(t, id)
	if op.State == api.OperationSucceeded || len(op.Result) != 0 {
		t.Fatal("unconfirmed HTTP handler published output")
	}
	_, err := f.customer.DownloadPlatformTenantSelfOperationArtifact(t.Context(), id, artifact, &bytes.Buffer{})
	if err == nil {
		t.Fatal("private pending file downloadable")
	}
}

func (f *customerHTTPFixture) assertResult(t *testing.T, id, artifact string, op api.OperationResponse) {
	t.Helper()
	var result struct {
		Rows       int    `json:"rows"`
		ArtifactID string `json:"artifact_id"`
		Version    string `json:"version"`
	}
	mustCustomerHTTP(t, json.Unmarshal(op.Result, &result))
	if result.Rows != 1 || result.ArtifactID != artifact || result.Version != "original" || len(op.Artifacts) != 1 || op.Artifacts[0].ID != artifact {
		t.Fatal("typed result or admitted code changed")
	}
	var file bytes.Buffer
	_, err := f.customer.DownloadPlatformTenantSelfOperationArtifact(t.Context(), id, artifact, &file)
	mustCustomerHTTP(t, err)
	if file.String() != customerHTTPCSV {
		t.Fatal("retained bytes changed")
	}
}

func (f *customerHTTPFixture) assertProgress(t *testing.T, id string) {
	t.Helper()
	op := f.read(t, id)
	if op.Progress == nil || op.Progress.Stage != "uploading" || op.State != api.OperationRunning {
		t.Fatal("durable HTTP progress missing")
	}
	events, err := f.customer.GetPlatformTenantSelfOperationEvents(t.Context(), id, 0)
	mustCustomerHTTP(t, err)
	// One ordered history: admission, claim, reported stages and the file.
	want := []string{"accepted", "running", "progress", "artifact_attached", "progress"}
	var kinds []string
	next := 0
	for _, event := range events.Events {
		kinds = append(kinds, event.Type)
		if next < len(want) && event.Type == want[next] {
			next++
		}
	}
	if next != len(want) {
		t.Fatalf("operation history = %v, want ordered %v", kinds, want)
	}
}

// assertExecutions checks the backend execution ledger: one HTTP invocation per
// generation. Handler entries are counted separately by the relay, because a
// pre-dispatch binding retry may claim an attempt without running business code.
func (f *customerHTTPFixture) assertExecutions(t *testing.T, id string, generations int) {
	t.Helper()
	executions, err := f.owner.GetOperationExecutions(t.Context(), f.app.Slug, id, 0, 10)
	mustCustomerHTTP(t, err)
	if len(executions.Executions) != generations {
		t.Fatalf("execution ledger has %d entries, want %d", len(executions.Executions), generations)
	}
	seen := map[string]bool{}
	for _, execution := range executions.Executions {
		if execution.InvocationID == "" || execution.WorkflowRunID != "" || execution.JobRunID != "" || seen[execution.InvocationID] || execution.Attempts < 1 {
			t.Fatal("HTTP execution ledger lost its invocation identity")
		}
		seen[execution.InvocationID] = true
	}
}
