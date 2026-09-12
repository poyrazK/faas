// adr: 171 — public admission seals source/input and keeps execution reads
// account-scoped while the runtime remains explicitly opt-in.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionpayload"
)

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

func executionRequest() api.CreateExecutionRequest {
	return api.CreateExecutionRequest{
		Runtime: api.ExecutionRuntimeNode22,
		Source:  "console.log(input.value)",
		Input:   json.RawMessage(`{"value":42}`),
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
