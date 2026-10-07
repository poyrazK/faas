// adr: 080

package sched

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/httpjson"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHTTPGatewaySynthNegotiatesManagedOperationResults(t *testing.T) {
	result := `"` + strings.Repeat("x", api.MaxExclusiveResultBytes-2) + `"`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got struct {
			Version int                  `json:"operation_result_version"`
			Claim   *exclusivework.Claim `json:"exclusive_claim"`
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.Version != api.ManagedOperationResultVersion || got.Claim == nil {
			t.Errorf("negotiation=%+v err=%v", got, err)
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(`{"state":"dispatching","status_code":200,"result":` + result + `}`))
	}))
	defer srv.Close()
	h := &httpGatewaySynth{client: srv.Client(), basePrefix: srv.URL, mintInternalSvcToken: func(string) (string, error) { return "test-token", nil }}
	out, err := h.InvokeWithWake(t.Context(), "app-1", state.Invocation{ID: "op-1", AppID: "app-1", Source: state.InvocationExclusiveOperation, Method: http.MethodPost, Path: "/orders", ExclusiveClaim: &exclusivework.Claim{OperationID: "op-1"}}, WakeResult{InstanceID: "instance-1", NodeID: "node-1"})
	if err != nil || string(out.Result) != result {
		t.Fatalf("bounded managed result: bytes=%d err=%v", len(out.Result), err)
	}
}

func TestHTTPGatewaySynthInvokeCarriesEnvelopeAndResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/invocations:dispatch" {
			t.Fatalf("path = %q, want dispatch route", r.URL.Path)
		}
		var got struct {
			PlatformTenantID string            `json:"platform_tenant_id"`
			InvocationID     string            `json:"invocation_id"`
			AppID            string            `json:"app_id"`
			Headers          map[string]string `json:"headers"`
			BodyB64          string            `json:"body_b64"`
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got.InvocationID != "inv-1" || got.AppID != "app-1" || got.PlatformTenantID != "tenant-1" {
			t.Fatalf("identity = %#v", got)
		}
		if got.Headers["content-type"] != "application/json" {
			t.Fatalf("headers = %#v", got.Headers)
		}
		if body, err := base64.StdEncoding.DecodeString(got.BodyB64); err != nil || string(body) != `{"hello":"world"}` {
			t.Fatalf("body_b64 = %q, decoded body = %q, err = %v", got.BodyB64, body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"dispatching","result":{"ok":true}}`))
	}))
	defer srv.Close()

	h := &httpGatewaySynth{client: srv.Client(), basePrefix: srv.URL, mintInternalSvcToken: func(string) (string, error) { return "test-token", nil }}
	got, err := h.Invoke(context.Background(), "app-1", state.Invocation{
		PlatformTenantID: "tenant-1",
		ID:               "inv-1",
		AppID:            "app-1",
		Source:           state.InvocationAsyncInvoke,
		Method:           http.MethodPost,
		Path:             "/e2e",
		Headers:          json.RawMessage(`{"content-type":"application/json"}`),
		Payload:          []byte(`{"hello":"world"}`),
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got.State != state.InvocationDispatching {
		t.Fatalf("state = %q, want dispatching", got.State)
	}
	if string(got.Result) != `{"ok":true}` {
		t.Fatalf("result = %s, want {\"ok\":true}", got.Result)
	}
}

func TestHTTPGatewaySynthWorkflowStepCarriesPersistedTenantIdentity(t *testing.T) {
	runID := "11111111-1111-4111-8111-111111111111"
	operationID, err := api.ManagedWorkflowStepOperationID(runID, "process")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got struct {
			PlatformTenantID                   string            `json:"platform_tenant_id"`
			Source                             string            `json:"source"`
			Headers                            map[string]string `json:"headers"`
			OperationResultVersion             int               `json:"operation_result_version"`
			ManagedWorkflowOperationID         string            `json:"managed_workflow_operation_id"`
			ManagedWorkflowOperationGeneration int64             `json:"managed_workflow_operation_generation"`
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.PlatformTenantID != "tenant-1" || got.Source != "workflow" || got.Headers["X-Faas-Workflow-Run-Id"] != runID ||
			got.OperationResultVersion != api.ManagedOperationResultVersion || got.ManagedWorkflowOperationID != operationID || got.ManagedWorkflowOperationGeneration != 1 {
			t.Fatalf("workflow identity envelope=%+v", got)
		}
		_, _ = w.Write([]byte(`{"state":"dispatching","status_code":200,"result":{"ok":true}}`))
	}))
	defer srv.Close()
	h := &httpGatewaySynth{client: srv.Client(), basePrefix: srv.URL, mintInternalSvcToken: func(string) (string, error) { return "test-token", nil }}
	status, body, err := h.ExecuteWorkflowStep(context.Background(), "app-1", WorkflowStepIdentity{
		RunID: runID, PlatformTenantID: "tenant-1",
	}, "/process", http.MethodPost, map[string]string{
		"X-Faas-Internal-Wake": "workflow", "X-Faas-Workflow-Run-Id": runID,
		"X-Faas-Workflow-Step": "process", "X-Faas-Workflow-Attempt": "1",
	}, []byte(`{"order_id":"42"}`), time.Second, operationID, 1)
	if err != nil || status != http.StatusOK || string(body) != `{"ok":true}` {
		t.Fatalf("ExecuteWorkflowStep = %d %s, %v", status, body, err)
	}
}

func TestHTTPGatewaySynthHandlerFailureIsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"failed","status_code":500,"result":{"error":"handler_error","message":"boom"}}`))
	}))
	defer srv.Close()

	h := &httpGatewaySynth{client: srv.Client(), basePrefix: srv.URL}
	out, err := h.Invoke(context.Background(), "app-1", state.Invocation{ID: "inv-1", AppID: "app-1"})
	if !errors.Is(err, ErrPermanentInvoke) {
		t.Fatalf("Invoke error = %v, want ErrPermanentInvoke", err)
	}
	if !strings.Contains(err.Error(), "boom") || !strings.Contains(string(out.Result), "handler_error") {
		t.Fatalf("Invoke result/error = %s / %v", out.Result, err)
	}
}

func TestHTTPGatewaySynthOrdinaryServerErrorIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"dispatching","result":{"error":"upstream_unavailable"},"status_code":503,"outcome_code":"upstream_unavailable"}`))
	}))
	defer srv.Close()
	synth := &httpGatewaySynth{client: srv.Client(), basePrefix: srv.URL}
	out, err := synth.Invoke(context.Background(), "app-1", state.Invocation{ID: "inv-1"})
	if err == nil {
		t.Fatal("Invoke returned nil error for retryable 503")
	}
	if errors.Is(err, ErrPermanentInvoke) {
		t.Fatalf("Invoke error = %v, must remain retryable", err)
	}
	if out.ResponseStatusCode != http.StatusServiceUnavailable || out.OutcomeCode != "upstream_unavailable" {
		t.Fatalf("Invoke response classification evidence = status %d code %q", out.ResponseStatusCode, out.OutcomeCode)
	}
}

func TestHTTPGatewaySynthRejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":"` + strings.Repeat("x", gatewayInvocationResponseMaxBytes) + `"}`))
	}))
	defer srv.Close()

	h := &httpGatewaySynth{client: srv.Client(), basePrefix: srv.URL}
	_, err := h.Invoke(context.Background(), "app-1", state.Invocation{ID: "inv-1"})
	if !errors.Is(err, httpjson.ErrResponseTooLarge) {
		t.Fatalf("Invoke oversized error = %v, want ErrResponseTooLarge", err)
	}
}

func TestHTTPGatewaySynthInvokeWithWakeCarriesTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got struct {
			InstanceID   string `json:"instance_id"`
			NodeID       string `json:"node_id"`
			DeploymentID string `json:"deployment_id"`
			WakeID       string `json:"wake_id"`
			Port         int    `json:"port"`
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got.InstanceID != "inst-1" || got.NodeID != "node-1" ||
			got.DeploymentID != "dep-1" || got.WakeID != "wake-1" || got.Port != 8081 {
			t.Fatalf("target = %#v", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"dispatching"}`))
	}))
	defer srv.Close()

	h := &httpGatewaySynth{client: srv.Client(), basePrefix: srv.URL}
	_, err := h.InvokeWithWake(context.Background(), "app-1", state.Invocation{
		ID:     "inv-1",
		AppID:  "app-1",
		Source: state.InvocationAsyncInvoke,
		Method: http.MethodPost,
		Path:   "/e2e",
	}, WakeResult{
		InstanceID:   "inst-1",
		NodeID:       "node-1",
		DeploymentID: "dep-1",
		WakeID:       "wake-1",
		Port:         8081,
	})
	if err != nil {
		t.Fatalf("InvokeWithWake: %v", err)
	}
}

func TestHTTPGatewaySynthExecuteStepPreservesDownstreamStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/invocations:dispatch" {
			t.Fatalf("path = %q, want dispatch route", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer workflow-token" {
			t.Fatalf("Authorization = %q, want workflow bearer", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"dispatching","status_code":503,"result":{"retry":true}}`))
	}))
	defer srv.Close()

	h := &httpGatewaySynth{
		client:     srv.Client(),
		basePrefix: srv.URL,
		mintInternalSvcToken: func(string) (string, error) {
			return "workflow-token", nil
		},
	}
	status, body, err := h.ExecuteStep(context.Background(), "app-1", "/flaky", http.MethodPost,
		map[string]string{"content-type": "application/json"}, []byte(`{"input":1}`), time.Second)
	if err != nil {
		t.Fatalf("ExecuteStep: %v", err)
	}
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	if string(body) != `{"retry":true}` {
		t.Fatalf("body = %s, want retry result", body)
	}
}

func TestHTTPGatewaySynthExecuteStepRequiresWorkflowTokenMinter(t *testing.T) {
	h := &httpGatewaySynth{
		client:     http.DefaultClient,
		basePrefix: "http://127.0.0.1:1",
	}
	_, _, err := h.ExecuteStep(context.Background(), "app-1", "/step", http.MethodPost, nil, nil, time.Second)
	if err == nil || !strings.Contains(err.Error(), "workflow invocation requires internal service token minter") {
		t.Fatalf("err = %v, want missing workflow token minter", err)
	}
}

func TestHTTPGatewaySynthWorkflowThrottlingParksUntilRetryAfter(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	calls := 0
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Headers map[string]string `json:"headers"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		keys = append(keys, request.Headers["Idempotency-Key"])
		calls++
		_, _ = w.Write([]byte(`{"state":"failed","status_code":429,"retry_after":"2","result":{"error":"throttled"}}`))
	}))
	defer srv.Close()
	transport := &httpGatewaySynth{client: srv.Client(), basePrefix: srv.URL, mintInternalSvcToken: func(string) (string, error) { return "test-token", nil }}
	run := &state.WorkflowRun{AppID: "00000000-0000-4000-8000-000000000001", WorkflowName: "report", Input: json.RawMessage(`{}`), DefinitionSnapshot: json.RawMessage(`{"name":"report","steps":[{"name":"main","run":"report","retry":{"max_attempts":2}}]}`)}
	if err := store.CreateWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	orchestrator := NewWorkflowOrchestrator(store, transport, nil, nil, nil)
	before := time.Now().UTC()
	if err := orchestrator.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	steps, err := store.GetWorkflowSteps(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	parked, err := store.GetWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(steps) != 1 || steps[0].NextRetryAt == nil || steps[0].NextRetryAt.Before(before.Add(2*time.Second)) || parked.ScheduledFor.Before(*steps[0].NextRetryAt) {
		t.Fatalf("not durably parked: calls=%d run=%+v steps=%+v", calls, parked, steps)
	}
	// Reconstruct the orchestrator, as after scheduler restart.
	orchestrator = NewWorkflowOrchestrator(store, transport, nil, nil, nil)
	if err := orchestrator.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("restart ignored retry deadline")
	}
	time.Sleep(time.Until(*steps[0].NextRetryAt) + 10*time.Millisecond)
	if err := orchestrator.DispatchTick(ctx); err != nil {
		t.Fatal(err)
	}
	final, err := store.GetWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] || final.Status != state.WorkflowRunStatusFailed {
		t.Fatalf("retry budget/idempotency: calls=%d keys=%v run=%+v", calls, keys, final)
	}
}
