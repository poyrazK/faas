// adr: 171 — public admission seals source/input and keeps execution reads
// account-scoped while the runtime remains explicitly opt-in.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/state"
)

func createRunsOnlyKey(t *testing.T, e testEnv, label string) string {
	t.Helper()
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, label, []string{api.ScopeRunsRead, api.ScopeRunsWrite}); err != nil {
		t.Fatal(err)
	}
	return plaintext
}

func createExecutionAs(t *testing.T, e testEnv, key, idempotencyKey string, request api.CreateExecutionRequest) api.ExecutionResponse {
	t.Helper()
	rec := e.doAs(t, http.MethodPost, "/v1/executions", request, map[string]string{"Idempotency-Key": idempotencyKey}, key)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create run = %d: %s", rec.Code, rec.Body.String())
	}
	var row api.ExecutionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	return row
}

func finishExecutionWithArtifact(t *testing.T, e testEnv, executionID string) {
	t.Helper()
	ctx := context.Background()
	claimedAt := time.Now().UTC().Add(time.Millisecond)
	claim, err := e.store.ClaimExecution(ctx, "ownership-test", claimedAt, time.Minute)
	if err != nil || claim.ID != executionID {
		t.Fatalf("ClaimExecution = %q, %v; want %q", claim.ID, err, executionID)
	}
	startedAt := claimedAt.Add(time.Millisecond)
	if _, err := e.store.MarkExecutionRunning(ctx, executionID, *claim.LeaseToken, startedAt); err != nil {
		t.Fatal(err)
	}
	content := []byte("value\n42\n")
	digest := sha256.Sum256(content)
	_, err = e.store.CompleteExecution(ctx, state.CompleteExecutionParams{
		ID: executionID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded,
		Result:     json.RawMessage("null"),
		Artifacts:  []api.ExecutionArtifact{{Name: "result.csv", Content: content, SizeBytes: len(content), SHA256: "sha256:" + hex.EncodeToString(digest[:])}},
		Usage:      api.ExecutionUsage{WallTimeMS: 5, CPUTimeMS: 3, PeakMemoryMB: 64},
		FinishedAt: startedAt.Add(time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunsOnlyKeysAreIsolatedByAPIKeyFamily(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableExecutionAPIForTest(t, &e)
	agentA := createRunsOnlyKey(t, e, "agent-a")
	agentB := createRunsOnlyKey(t, e, "agent-b")
	sharedRequest := executionRequest()
	owned := createExecutionAs(t, e, agentA, "same-key", sharedRequest)
	finishExecutionWithArtifact(t, e, owned.ID)
	pending := createExecutionAs(t, e, agentA, "pending", sharedRequest)
	other := createExecutionAs(t, e, agentB, "same-key", sharedRequest)
	if owned.ID == other.ID {
		t.Fatal("second agent replayed the first agent's idempotent response")
	}

	for _, check := range []struct {
		key  string
		want []string
	}{{agentA, []string{pending.ID, owned.ID}}, {agentB, []string{other.ID}}, {e.key, []string{other.ID, pending.ID, owned.ID}}} {
		rec := e.doAs(t, http.MethodGet, "/v1/executions", nil, nil, check.key)
		if rec.Code != http.StatusOK {
			t.Fatalf("list runs = %d: %s", rec.Code, rec.Body.String())
		}
		var response api.ExecutionListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(response.Executions))
		for i := range response.Executions {
			got[i] = response.Executions[i].ID
		}
		if !reflect.DeepEqual(got, check.want) {
			t.Fatalf("list for key %q = %v, want %v", check.key[:min(len(check.key), 12)], got, check.want)
		}
	}

	for _, path := range []string{
		"/v1/executions/" + owned.ID,
		"/v1/executions/" + owned.ID + "/events",
	} {
		rec := e.doAs(t, http.MethodGet, path, nil, nil, agentB)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("cross-agent GET %s = %d: %s", path, rec.Code, rec.Body.String())
		}
	}
	if rec := e.doAs(t, http.MethodDelete, "/v1/executions/"+pending.ID, nil,
		map[string]string{"Idempotency-Key": "cancel-other"}, agentB); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-agent cancellation = %d: %s", rec.Code, rec.Body.String())
	}
	stillQueued, err := e.store.ExecutionByID(context.Background(), e.acct.ID, pending.ID)
	if err != nil || stillQueued.Status != api.ExecutionStatusQueued {
		t.Fatalf("unauthorized cancellation changed owner run: status=%s err=%v", stillQueued.Status, err)
	}

	importRequest := api.CreateExecutionRequest{
		Runtime: api.ExecutionRuntimeNode22, Entrypoint: "main.js",
		Files:          []api.ExecutionFile{{Path: "main.js", Content: []byte("export default async () => 1")}},
		ArtifactInputs: []api.ExecutionArtifactInput{{ExecutionID: owned.ID, Name: "result.csv", Path: "input/result.csv"}},
	}
	rec := e.doAs(t, http.MethodPost, "/v1/executions", importRequest,
		map[string]string{"Idempotency-Key": "cross-artifact"}, agentB)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-agent artifact import = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestExecutionWorkflowSummariesRespectKeyFamilyBoundaries(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableExecutionAPIForTest(t, &e)
	agentA := createRunsOnlyKey(t, e, "workflow-agent-a")
	agentB := createRunsOnlyKey(t, e, "workflow-agent-b")
	workflowID := "agent-flow-2026-10"

	producer := executionRequest()
	producer.WorkflowID = workflowID
	producer.StepLabel = "collect inputs"
	completed := createExecutionAs(t, e, agentA, "workflow-a", producer)
	finishExecutionWithArtifact(t, e, completed.ID)
	privateRequest := executionRequest()
	privateRequest.WorkflowID = "agent-a-private-flow"
	createExecutionAs(t, e, agentA, "workflow-a-private", privateRequest)

	consumer := executionRequest()
	consumer.WorkflowID = workflowID
	consumer.StepLabel = "analyze"
	pending := createExecutionAs(t, e, agentB, "workflow-b", consumer)
	if completed.WorkflowID != workflowID || completed.StepLabel != producer.StepLabel {
		t.Fatalf("workflow receipt metadata = %q/%q", completed.WorkflowID, completed.StepLabel)
	}
	if rec := e.doAs(t, http.MethodGet, "/v1/execution-workflows/agent-a-private-flow", nil, nil, agentB); rec.Code != http.StatusNotFound {
		t.Fatalf("other family workflow summary = %d: %s", rec.Code, rec.Body.String())
	}

	for _, check := range []struct {
		key       string
		wantRuns  int64
		wantState api.ExecutionStatus
	}{{agentA, 1, api.ExecutionStatusSucceeded}, {agentB, 1, api.ExecutionStatusQueued}, {e.key, 2, ""}} {
		rec := e.doAs(t, http.MethodGet, "/v1/execution-workflows/"+workflowID, nil, nil, check.key)
		if rec.Code != http.StatusOK {
			t.Fatalf("workflow summary = %d: %s", rec.Code, rec.Body.String())
		}
		var summary api.ExecutionWorkflowResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
			t.Fatal(err)
		}
		if summary.RunCount != check.wantRuns {
			t.Fatalf("workflow run count for key %q = %d, want %d", check.key[:min(len(check.key), 12)], summary.RunCount, check.wantRuns)
		}
		if check.wantState != "" && summary.StatusCounts.Succeeded+summary.StatusCounts.Queued != 1 {
			t.Fatalf("workflow status counts for key = %+v", summary.StatusCounts)
		}
		if check.key == agentA && (summary.StatusCounts.Succeeded != 1 || summary.Usage.WallTimeMS != 5 || summary.Usage.CPUTimeMS != 3 || summary.Usage.PeakMemoryMB != 64 || summary.Usage.OutputBytes == 0) {
			t.Fatalf("agent A workflow aggregate = %+v", summary)
		}
		if check.key == agentB && summary.StatusCounts.Queued != 1 {
			t.Fatalf("agent B workflow aggregate = %+v", summary)
		}
		if check.key == e.key && (summary.StatusCounts.Succeeded != 1 || summary.StatusCounts.Queued != 1) {
			t.Fatalf("broad workflow aggregate = %+v", summary)
		}
	}

	for _, check := range []struct {
		key  string
		want string
	}{{agentA, completed.ID}, {agentB, pending.ID}, {e.key, ""}} {
		rec := e.doAs(t, http.MethodGet, "/v1/executions?workflow_id="+workflowID, nil, nil, check.key)
		if rec.Code != http.StatusOK {
			t.Fatalf("workflow-filtered list = %d: %s", rec.Code, rec.Body.String())
		}
		var page api.ExecutionListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if check.key == e.key {
			if len(page.Executions) != 2 {
				t.Fatalf("broad workflow list contains %d runs, want 2", len(page.Executions))
			}
		} else if len(page.Executions) != 1 || page.Executions[0].ID != check.want {
			t.Fatalf("workflow list for key = %+v, want only %s", page.Executions, check.want)
		}
	}

	invalid := executionRequest()
	invalid.StepLabel = "unscoped"
	if rec := e.doAs(t, http.MethodPost, "/v1/executions", invalid, map[string]string{"Idempotency-Key": "workflow-invalid"}, agentA); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("step label without workflow_id = %d: %s", rec.Code, rec.Body.String())
	}
}

func enableExecutionAPIForTest(t *testing.T, e *testEnv) *age.X25519Identity {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	previous := setSecretRecipient
	setSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	t.Cleanup(func() { setSecretRecipient = previous })
	e.s.WithExecutionAPIEnabled(true)
	return identity
}

func TestCreateExecutionRejectsDuplicateAgentWorkflowStep(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableExecutionAPIForTest(t, &e)
	key := createRunsOnlyKey(t, e, "workflow-step-agent")
	request := executionRequest()
	request.WorkflowID = "incident-duplicate-step"
	request.StepLabel = "gwf:0123456789abcdef01234567:inspect"
	first := createExecutionAs(t, e, key, "workflow-step-first", request)

	rec := e.doAs(t, http.MethodPost, "/v1/executions", request, map[string]string{"Idempotency-Key": "workflow-step-retry"}, key)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate workflow step = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != api.CodeExecutionWorkflowStepExists {
		t.Fatalf("duplicate workflow step code = %q", problem.Code)
	}
	list := e.doAs(t, http.MethodGet, "/v1/executions?workflow_id="+request.WorkflowID, nil, nil, key)
	if list.Code != http.StatusOK {
		t.Fatalf("list workflow receipts = %d: %s", list.Code, list.Body.String())
	}
	var page api.ExecutionListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Executions) != 1 || page.Executions[0].ID != first.ID {
		t.Fatalf("workflow receipts = %+v, want only first receipt %q", page.Executions, first.ID)
	}
}

func TestExecutionDataProfileAdmissionAndReceiptProvenance(t *testing.T) {
	e := setup(t, api.PlanPro)
	identity := enableExecutionAPIForTest(t, &e)
	req := api.CreateExecutionRequest{Runtime: api.ExecutionRuntimePython313, Profile: api.ExecutionProfilePythonDataV1, Source: "def main(input, context): return input"}
	rec := e.do(t, http.MethodPost, "/v1/executions", req, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("admission=%d: %s", rec.Code, rec.Body.String())
	}
	var response api.ExecutionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Profile != req.Profile || response.Packages["numpy"] == "" || response.Packages["pandas"] == "" || response.RuntimeImageDigest != "" {
		t.Fatalf("queued provenance=%+v", response)
	}
	ctx := context.Background()
	claim, err := e.store.ClaimExecution(ctx, "profile", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := executionpayload.DecodeRequest(ctx, []*age.X25519Identity{identity}, claim.SealedPayload, claim.PayloadKID)
	if err != nil || decoded.Profile != req.Profile {
		t.Fatalf("sealed profile=%q, %v", decoded.Profile, err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	if _, err := e.store.PinExecutionRuntime(ctx, claim.ID, *claim.LeaseToken, digest, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.MarkExecutionRunning(ctx, claim.ID, *claim.LeaseToken, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CompleteExecution(ctx, state.CompleteExecutionParams{ID: claim.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded, Result: json.RawMessage("null"), FinishedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodGet, "/v1/executions/"+claim.ID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.RuntimeImageDigest != digest || response.Profile != req.Profile || response.Packages["pandas"] == "" {
		t.Fatalf("terminal provenance=%+v", response)
	}
}

func executionRequest() api.CreateExecutionRequest {
	return api.CreateExecutionRequest{
		Runtime: api.ExecutionRuntimeNode22,
		Source:  "console.log(input.value)",
		Input:   json.RawMessage(`{"value":42}`),
	}
}

func TestCreateExecutionKeepsIntegrationAllowlistOutOfGuestPayload(t *testing.T) {
	e := setup(t, api.PlanPro)
	identity := enableExecutionAPIForTest(t, &e)
	offer, err := e.store.CreateOutboundIntegration(context.Background(), state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: e.acct.ID, Name: "agent-api", Origin: "https://api.example.com",
		AllowedMethods: []string{"GET"}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		OwnerKind: "customer", CredentialSource: "customer_sealed",
	})
	if err != nil {
		t.Fatalf("CreateOutboundIntegration: %v", err)
	}
	if err := e.store.SetOutboundCredential(context.Background(), e.acct.ID, offer.ID, []byte("sealed-provider-credential")); err != nil {
		t.Fatalf("SetOutboundCredential: %v", err)
	}
	if err := e.store.SetOutboundIntegrationRunsEnabled(context.Background(), e.acct.ID, offer.ID, true); err != nil {
		t.Fatalf("SetOutboundIntegrationRunsEnabled: %v", err)
	}
	request := executionRequest()
	request.IntegrationIDs = []string{offer.ID}
	key := createRunsOnlyKey(t, e, "integration-agent")
	response := createExecutionAs(t, e, key, "integration-run", request)
	responseJSON, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(responseJSON), offer.ID) {
		t.Fatalf("customer response contains integration ID: %s", responseJSON)
	}
	claimedAt := time.Now().UTC().Add(time.Millisecond)
	claim, err := e.store.ClaimExecution(context.Background(), "integration-guest", claimedAt, time.Minute)
	if err != nil || claim.ID != response.ID {
		t.Fatalf("ClaimExecution = %q, %v; want %q", claim.ID, err, response.ID)
	}
	if len(claim.OutboundIntegrationIDs) != 1 || claim.OutboundIntegrationIDs[0] != offer.ID {
		t.Fatalf("scheduler claim integration IDs = %v, want [%s]", claim.OutboundIntegrationIDs, offer.ID)
	}
	resolved, err := executionpayload.DecodeRequest(context.Background(), []*age.X25519Identity{identity}, claim.SealedPayload, claim.PayloadKID)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	serialized, err := json.Marshal(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), offer.ID) || strings.Contains(string(serialized), "integration_ids") {
		t.Fatalf("guest request payload contains integration allowlist: %s", serialized)
	}
}

func TestExecutionAPIIsDisabledByDefault(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, http.MethodPost, "/v1/executions", executionRequest(), nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("POST /v1/executions with gate off = %d, want 501; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), api.CodeNotImplemented) {
		t.Fatalf("disabled response missing %q: %s", api.CodeNotImplemented, rec.Body.String())
	}
}

func TestExecutionFreePlanRejectedBeforeRuntimeGate(t *testing.T) {
	e := setup(t, api.PlanFree)
	rec := e.do(t, http.MethodPost, "/v1/executions", executionRequest(), nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /v1/executions for Free with gate off = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), api.CodeExecutionsNotAllowed) {
		t.Fatalf("free-plan response missing plan-limit code: %s", rec.Body.String())
	}
}

func TestCreateExecutionSealsPayloadAndReturnsQueuedProjection(t *testing.T) {
	e := setup(t, api.PlanHobby)
	identity := enableExecutionAPIForTest(t, &e)
	rec := e.do(t, http.MethodPost, "/v1/executions", executionRequest(), nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /v1/executions = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var response api.ExecutionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID == "" || response.Status != api.ExecutionStatusQueued || response.Runtime != api.ExecutionRuntimeNode22 {
		t.Fatalf("unexpected response: %+v", response)
	}
	if response.StartedAt != nil || response.FinishedAt != nil || response.Usage != nil {
		t.Fatalf("queued response contains terminal fields: %+v", response)
	}
	if strings.Contains(rec.Body.String(), "console.log") || strings.Contains(rec.Body.String(), "value") {
		t.Fatalf("response leaked source/input: %s", rec.Body.String())
	}

	claim, err := e.store.ClaimExecution(context.Background(), "schedd-test", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("ClaimExecution: %v", err)
	}
	source, input, err := executionpayload.Decode(context.Background(), []*age.X25519Identity{identity}, claim.SealedPayload, claim.PayloadKID)
	if err != nil {
		t.Fatalf("Decode sealed claim: %v", err)
	}
	if source != "console.log(input.value)" || string(input) != `{"value":42}` {
		t.Fatalf("decoded payload = source %q input %s", source, input)
	}
}

func TestCreateExecutionFreePlanRejectedBeforeSealing(t *testing.T) {
	e := setup(t, api.PlanFree)
	e.s.WithExecutionAPIEnabled(true)
	previous := setSecretRecipient
	setSecretRecipient = func() *age.X25519Recipient { return nil }
	t.Cleanup(func() { setSecretRecipient = previous })
	rec := e.do(t, http.MethodPost, "/v1/executions", executionRequest(), nil)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodeExecutionsNotAllowed) {
		t.Fatalf("free execution admission = %d %s, want 403 %s", rec.Code, rec.Body.String(), api.CodeExecutionsNotAllowed)
	}
}

func TestCreateExecutionFailsClosedWithoutHostRecipient(t *testing.T) {
	e := setup(t, api.PlanHobby)
	e.s.WithExecutionAPIEnabled(true)
	previous := setSecretRecipient
	setSecretRecipient = func() *age.X25519Recipient { return nil }
	t.Cleanup(func() { setSecretRecipient = previous })
	rec := e.do(t, http.MethodPost, "/v1/executions", executionRequest(), nil)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), api.CodeCapacity) {
		t.Fatalf("missing recipient admission = %d %s, want 503 %s", rec.Code, rec.Body.String(), api.CodeCapacity)
	}
}

func TestStreamExecutionEventsReplaysAndClosesAtTerminal(t *testing.T) {
	e := setup(t, api.PlanHobby)
	enableExecutionAPIForTest(t, &e)
	created := e.do(t, http.MethodPost, "/v1/executions", executionRequest(), nil)
	if created.Code != http.StatusAccepted {
		t.Fatalf("create = %d: %s", created.Code, created.Body)
	}
	var receipt api.ExecutionResponse
	if err := json.Unmarshal(created.Body.Bytes(), &receipt); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	cancelled := e.do(t, http.MethodDelete, "/v1/executions/"+receipt.ID, nil, nil)
	if cancelled.Code != http.StatusAccepted {
		t.Fatalf("cancel = %d: %s", cancelled.Code, cancelled.Body)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/executions/"+receipt.ID+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stream = %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: status") || !strings.Contains(body, "event: terminal") || !strings.Contains(body, `"status":"cancelled"`) {
		t.Fatalf("stream body = %q, want queued/status and terminal cancellation", body)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content type = %q, want text/event-stream", rec.Header().Get("Content-Type"))
	}
}

func TestStreamExecutionEventsRejectsBadCursor(t *testing.T) {
	e := setup(t, api.PlanHobby)
	enableExecutionAPIForTest(t, &e)
	created := e.do(t, http.MethodPost, "/v1/executions", executionRequest(), nil)
	var receipt api.ExecutionResponse
	if err := json.Unmarshal(created.Body.Bytes(), &receipt); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/executions/"+receipt.ID+"/events?after=-1", nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor = %d: %s", rec.Code, rec.Body)
	}
}

func TestExecutionStatusAndCancellationAreAccountScoped(t *testing.T) {
	e := setup(t, api.PlanHobby)
	enableExecutionAPIForTest(t, &e)
	created := e.do(t, http.MethodPost, "/v1/executions", executionRequest(), nil)
	if created.Code != http.StatusAccepted {
		t.Fatalf("create = %d; body=%s", created.Code, created.Body.String())
	}
	var response api.ExecutionResponse
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	status := e.do(t, http.MethodGet, "/v1/executions/"+response.ID, nil, nil)
	if status.Code != http.StatusOK {
		t.Fatalf("GET execution = %d; body=%s", status.Code, status.Body.String())
	}

	other, err := e.store.CreateAccount(context.Background(), "execution-other@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, otherHash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(context.Background(), other.ID, otherHash, "other", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	otherReq := httptest.NewRequest(http.MethodGet, "/v1/executions/"+response.ID, nil)
	otherReq.Header.Set("Authorization", "Bearer "+otherToken)
	otherRec := httptest.NewRecorder()
	e.h.ServeHTTP(otherRec, otherReq)
	if otherRec.Code != http.StatusNotFound || !strings.Contains(otherRec.Body.String(), api.CodeNotFound) {
		t.Fatalf("cross-account GET = %d %s, want identical 404", otherRec.Code, otherRec.Body.String())
	}

	cancel := e.do(t, http.MethodDelete, "/v1/executions/"+response.ID, nil, nil)
	if cancel.Code != http.StatusAccepted {
		t.Fatalf("DELETE execution = %d; body=%s", cancel.Code, cancel.Body.String())
	}
	var cancelled api.ExecutionResponse
	if err := json.Unmarshal(cancel.Body.Bytes(), &cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != api.ExecutionStatusCancelled || cancelled.FinishedAt == nil {
		t.Fatalf("cancelled response = %+v", cancelled)
	}
}

func TestListExecutionsPaginatesAndFiltersByStatus(t *testing.T) {
	e := setup(t, api.PlanHobby)
	enableExecutionAPIForTest(t, &e)

	first := e.do(t, http.MethodPost, "/v1/executions", executionRequest(), nil)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first create = %d; body=%s", first.Code, first.Body.String())
	}
	var firstResponse api.ExecutionResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstResponse); err != nil {
		t.Fatal(err)
	}
	if cancel := e.do(t, http.MethodDelete, "/v1/executions/"+firstResponse.ID, nil, nil); cancel.Code != http.StatusAccepted {
		t.Fatalf("first cancel = %d; body=%s", cancel.Code, cancel.Body.String())
	}
	second := e.do(t, http.MethodPost, "/v1/executions", executionRequest(), nil)
	if second.Code != http.StatusAccepted {
		t.Fatalf("second create = %d; body=%s", second.Code, second.Body.String())
	}

	page := e.do(t, http.MethodGet, "/v1/executions?limit=1", nil, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("list = %d; body=%s", page.Code, page.Body.String())
	}
	var listed api.ExecutionListResponse
	if err := json.Unmarshal(page.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Executions) != 1 || listed.Limit != 1 || listed.Offset != 0 || listed.NextOffset != 1 {
		t.Fatalf("first page = %+v", listed)
	}
	last := e.do(t, http.MethodGet, "/v1/executions?limit=1&offset=1", nil, nil)
	if last.Code != http.StatusOK {
		t.Fatalf("last page = %d; body=%s", last.Code, last.Body.String())
	}
	var lastPage api.ExecutionListResponse
	if err := json.Unmarshal(last.Body.Bytes(), &lastPage); err != nil {
		t.Fatal(err)
	}
	if len(lastPage.Executions) != 1 || lastPage.NextOffset != -1 {
		t.Fatalf("last page = %+v", lastPage)
	}

	cancelled := e.do(t, http.MethodGet, "/v1/executions?status=cancelled", nil, nil)
	if cancelled.Code != http.StatusOK {
		t.Fatalf("cancelled list = %d; body=%s", cancelled.Code, cancelled.Body.String())
	}
	var cancelledPage api.ExecutionListResponse
	if err := json.Unmarshal(cancelled.Body.Bytes(), &cancelledPage); err != nil {
		t.Fatal(err)
	}
	if len(cancelledPage.Executions) != 1 || cancelledPage.Executions[0].Status != api.ExecutionStatusCancelled || cancelledPage.NextOffset != -1 {
		t.Fatalf("cancelled page = %+v", cancelledPage)
	}

	bad := e.do(t, http.MethodGet, "/v1/executions?status=unknown", nil, nil)
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), api.CodeValidation) {
		t.Fatalf("bad status = %d %s", bad.Code, bad.Body.String())
	}
}

func TestExecutionAPIGateEnv(t *testing.T) {
	getenv := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}
	if executionAPIEnabledFromEnv(getenv(map[string]string{})) {
		t.Fatal("empty environment enabled execution API")
	}
	if !executionAPIEnabledFromEnv(getenv(map[string]string{"FAAS_EXECUTION_API_ENABLED": "1"})) {
		t.Fatal("FAAS_EXECUTION_API_ENABLED=1 did not enable execution API")
	}
	if executionAPIEnabledFromEnv(getenv(map[string]string{"FAAS_EXECUTION_API_ENABLED": "true"})) {
		t.Fatal("non-canonical execution API value enabled execution API")
	}
}
