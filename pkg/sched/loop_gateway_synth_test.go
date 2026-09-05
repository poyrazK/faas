package sched

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestHTTPGatewaySynthInvokeCarriesEnvelopeAndResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/invocations:dispatch" {
			t.Fatalf("path = %q, want dispatch route", r.URL.Path)
		}
		var got struct {
			InvocationID string            `json:"invocation_id"`
			AppID        string            `json:"app_id"`
			Headers      map[string]string `json:"headers"`
			BodyB64      string            `json:"body_b64"`
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got.InvocationID != "inv-1" || got.AppID != "app-1" {
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
		ID:      "inv-1",
		AppID:   "app-1",
		Source:  state.InvocationAsyncInvoke,
		Method:  http.MethodPost,
		Path:    "/e2e",
		Headers: json.RawMessage(`{"content-type":"application/json"}`),
		Payload: []byte(`{"hello":"world"}`),
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
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"dispatching","status_code":503,"result":{"retry":true}}`))
	}))
	defer srv.Close()

	h := &httpGatewaySynth{client: srv.Client(), basePrefix: srv.URL}
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
