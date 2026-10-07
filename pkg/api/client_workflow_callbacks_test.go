package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientWorkflowCallbackRoutes(t *testing.T) {
	const base = "/v1/workflows/runs/run-id/callbacks"
	const callback = base + "/callback-id"
	const binding = callback + "/webhook-binding"
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("missing bearer authorization on %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		switch r.Method + " " + r.URL.Path {
		case "GET " + base:
			_, _ = w.Write([]byte(`{"callbacks":[{"id":"callback-id","step_name":"wait"}]}`))
		case "POST " + callback:
			if string(body) != `{"paid":true}` {
				t.Errorf("callback body = %s", body)
			}
			_, _ = w.Write([]byte(`{"status":"received","duplicate":false}`))
		case "PUT " + binding:
			var req CreateWorkflowCallbackWebhookBindingRequest
			if err := json.Unmarshal(body, &req); err != nil || req.EndpointID != "endpoint-id" || req.EventType != "invoice.paid" || req.ObjectID != "in_123" {
				t.Errorf("binding request = %+v, %v", req, err)
			}
			_, _ = w.Write([]byte(`{"id":"binding-id","run_id":"run-id","step_name":"wait","endpoint_id":"endpoint-id","event_type":"invoice.paid","object_id":"in_123"}`))
		case "GET " + binding:
			_, _ = w.Write([]byte(`{"id":"binding-id","run_id":"run-id","step_name":"wait","endpoint_id":"endpoint-id","event_type":"invoice.paid","object_id":"in_123"}`))
		case "DELETE " + binding:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "token")
	ctx := context.Background()
	if out, err := c.ListWorkflowCallbacks(ctx, "run-id"); err != nil || len(out.Callbacks) != 1 || out.Callbacks[0].ID != "callback-id" {
		t.Fatalf("list callbacks = %+v, %v", out, err)
	}
	if out, err := c.CompleteWorkflowCallback(ctx, "run-id", "callback-id", json.RawMessage(`{"paid":true}`)); err != nil || out.Status != "received" {
		t.Fatalf("complete callback = %+v, %v", out, err)
	}
	req := CreateWorkflowCallbackWebhookBindingRequest{EndpointID: "endpoint-id", EventType: "invoice.paid", ObjectID: "in_123"}
	if out, err := c.PutWorkflowCallbackWebhookBinding(ctx, "run-id", "callback-id", req); err != nil || out.ID != "binding-id" {
		t.Fatalf("put binding = %+v, %v", out, err)
	}
	if out, err := c.GetWorkflowCallbackWebhookBinding(ctx, "run-id", "callback-id"); err != nil || out.ID != "binding-id" {
		t.Fatalf("get binding = %+v, %v", out, err)
	}
	if err := c.DeleteWorkflowCallbackWebhookBinding(ctx, "run-id", "callback-id"); err != nil {
		t.Fatalf("delete binding: %v", err)
	}
	if len(requests) != 5 {
		t.Fatalf("requests = %v", requests)
	}
}

func TestClientPlatformTenantWorkflowContinuations(t *testing.T) {
	const base = "/v1/platform-tenant-self/workflows/runs/run-id"
	const callbacks = base + "/callbacks"
	const callback = callbacks + "/callback-id"
	const events = base + "/events"
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer tenant-token" {
			t.Errorf("missing tenant bearer authorization on %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		switch r.Method + " " + r.URL.Path {
		case "GET " + callbacks:
			_, _ = w.Write([]byte(`{"callbacks":[{"id":"callback-id","step_name":"wait"}]}`))
		case "POST " + callback:
			if string(body) != `{"approved":true}` {
				t.Errorf("callback body = %s", body)
			}
			_, _ = w.Write([]byte(`{"status":"received","duplicate":false}`))
		case "POST " + events:
			if r.Header.Get("Idempotency-Key") != "approval-retry-1" {
				t.Errorf("event Idempotency-Key = %q", r.Header.Get("Idempotency-Key"))
			}
			if string(body) != `{"event_name":"approved","payload":{"approved":true}}` {
				t.Errorf("event body = %s", body)
			}
			_, _ = w.Write([]byte(`{"status":"received","event_name":"approved"}`))
		default:
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "tenant-token")
	ctx := context.Background()
	if out, err := c.ListPlatformTenantSelfWorkflowCallbacks(ctx, "run-id"); err != nil || len(out.Callbacks) != 1 || out.Callbacks[0].ID != "callback-id" {
		t.Fatalf("list tenant callbacks = %+v, %v", out, err)
	}
	if out, err := c.CompletePlatformTenantSelfWorkflowCallback(ctx, "run-id", "callback-id", json.RawMessage(`{"approved":true}`)); err != nil || out.Status != "received" {
		t.Fatalf("complete tenant callback = %+v, %v", out, err)
	}
	if out, err := c.SendPlatformTenantSelfWorkflowEvent(ctx, "run-id", "approved", json.RawMessage(`{"approved":true}`), "approval-retry-1"); err != nil || out.EventName != "approved" {
		t.Fatalf("send tenant event = %+v, %v", out, err)
	}
	if len(requests) != 3 {
		t.Fatalf("requests = %v", requests)
	}
}
