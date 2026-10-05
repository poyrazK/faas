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

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/httpjson"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
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
			AccountID        string            `json:"account_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got.InvocationID != "inv-1" || got.AppID != "app-1" || got.PlatformTenantID != "tenant-1" || got.AccountID != "acct-1" {
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

	h := &httpGatewaySynth{client: srv.Client(), basePrefix: srv.URL}
	got, err := h.Invoke(context.Background(), "app-1", state.Invocation{
		PlatformTenantID: "tenant-1",
		ID:               "inv-1",
		AppID:            "app-1",
		Source:           state.InvocationAsyncInvoke,
		Method:           http.MethodPost,
		Path:             "/e2e",
		Headers:          json.RawMessage(`{"content-type":"application/json"}`),
		Payload:          []byte(`{"hello":"world"}`),
		AccountID:        "acct-1",
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

// adr: 570
func TestHTTPGatewaySynthSecurityHandoffComesFromOwnerContext(t *testing.T) {
	value, err := trafficrevocation.EncodeSnapshot(map[trafficrevocation.Scope]trafficrevocation.State{{Kind: "account", ID: uuid.NewString()}: {Revision: 2}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := trafficrevocation.WithHandoffSnapshot(t.Context(), value)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got struct {
			SecuritySnapshot string            `json:"security_snapshot"`
			Headers          map[string]string `json:"headers"`
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
			http.Error(w, "decode", 400)
			return
		}
		if got.SecuritySnapshot != value {
			t.Errorf("handoff was rebased or replaced by guest metadata: %q", got.SecuritySnapshot)
		}
		_, _ = w.Write([]byte(`{"state":"dispatching"}`))
	}))
	defer srv.Close()
	synth := &httpGatewaySynth{client: srv.Client(), basePrefix: srv.URL}
	_, err = synth.InvokeWithWake(ctx, "app", state.Invocation{ID: "inv", Headers: json.RawMessage(`{"security_snapshot":"forged","X-Faas-Traffic-Security":"forged"}`)}, WakeResult{InstanceID: "inst", NodeID: "node", DeploymentID: "dep"})
	if err != nil {
		t.Fatal(err)
	}
}
