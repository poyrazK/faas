// adr: 20261021150000
package gateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func serveRehearsalPlatformWithKey(t *testing.T, store *fakeRehearsalPlatformAPIStore, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusTeapot)
	})
	handler := gateway.WithRehearsalPlatformAPI(next, func(context.Context, string) (string, error) { return "caller", nil }, store)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Host = "api.svc.gregale:10081"
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestRehearsalPlatformAPICapturesContainedWorkflowStarts(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, key string
		status                int
		want                  *state.ScenarioPlatformCallCapture
	}{
		{name: "start", path: "/v1/apps/billing/workflows/invoice/runs", body: `{"order":1}`, status: http.StatusCreated,
			want: &state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindWorkflowRun, Address: "billing", Name: "invoice", Message: json.RawMessage(`{"input":{"order":1}}`)}},
		{name: "empty-input", path: "/v1/apps/billing/workflows/invoice/runs", status: http.StatusCreated,
			want: &state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindWorkflowRun, Address: "billing", Name: "invoice", Message: json.RawMessage(`{"input":{}}`)}},
		{name: "keyed", path: "/v1/apps/billing/workflows/invoice/runs", body: `{}`, key: " k1 ", status: http.StatusCreated,
			want: &state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindWorkflowRun, Address: "billing", Name: "invoice", IdempotencyKey: "k1", Message: json.RawMessage(`{"input":{}}`)}},
		{name: "invalid-json", path: "/v1/apps/billing/workflows/invoice/runs", body: `{`, status: http.StatusBadRequest},
		{name: "blank-key", path: "/v1/apps/billing/workflows/invoice/runs", body: `{}`, key: "  ", status: http.StatusBadRequest},
		{name: "unknown-app", path: "/v1/apps/missing/workflows/invoice/runs", body: `{}`, status: http.StatusNotFound},
		{name: "oversized-input", path: "/v1/apps/billing/workflows/invoice/runs", body: `"` + strings.Repeat("x", api.MaxScenarioCapturedPlatformCallBytes) + `"`, status: http.StatusCreated,
			want: &state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindWorkflowRun, Address: "billing", Name: "invoice", Oversized: true}},
		{name: "input-over-public-limit", path: "/v1/apps/billing/workflows/invoice/runs", body: `"` + strings.Repeat("x", int(api.WorkflowRunInputMaxBytes)) + `"`, status: http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32)}
			response := serveRehearsalPlatformWithKey(t, store, tc.path, tc.body, tc.key)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body)
			}
			if tc.want == nil {
				if len(store.calls) != 0 {
					t.Fatalf("refused start was captured: %+v", store.calls)
				}
				return
			}
			if len(store.calls) != 1 || response.Header().Get("X-Gregale-Rehearsal") != "captured" {
				t.Fatalf("calls = %+v, headers %v", store.calls, response.Header())
			}
			got := store.calls[0]
			if got.Kind != tc.want.Kind || got.Address != tc.want.Address || got.Name != tc.want.Name || got.IdempotencyKey != tc.want.IdempotencyKey ||
				got.Oversized != tc.want.Oversized || string(got.Message) != string(tc.want.Message) {
				t.Fatalf("captured %+v (%s), want %+v (%s)", got, got.Message, *tc.want, tc.want.Message)
			}
			var run api.WorkflowRunResponse
			if err := json.NewDecoder(response.Body).Decode(&run); err != nil || run.ID == "" || run.WorkflowName != "invoice" || run.Status != state.WorkflowRunStatusPending {
				t.Fatalf("run response = %+v %v", run, err)
			}
		})
	}
}

func TestRehearsalPlatformAPIReplaysIdempotentWorkflowStart(t *testing.T) {
	store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32)}
	first := serveRehearsalPlatformWithKey(t, store, "/v1/apps/billing/workflows/invoice/runs", `{}`, "k1")
	again := serveRehearsalPlatformWithKey(t, store, "/v1/apps/billing/workflows/invoice/runs", `{}`, "k1")
	var a, b api.WorkflowRunResponse
	_ = json.NewDecoder(first.Body).Decode(&a)
	_ = json.NewDecoder(again.Body).Decode(&b)
	if first.Code != http.StatusCreated || again.Code != http.StatusCreated || a.ID != b.ID || again.Header().Get("Idempotent-Replayed") != "true" || len(store.calls) != 1 {
		t.Fatalf("replay: %d/%d ids %q/%q header %q calls %d", first.Code, again.Code, a.ID, b.ID, again.Header().Get("Idempotent-Replayed"), len(store.calls))
	}
}

func TestRehearsalPlatformAPICapturesContainedWorkflowEvents(t *testing.T) {
	const run = "/v1/workflows/runs/5d1c8b4e-2f3a-4c55-9d7e-0c1f6a2b3c4d/events"
	for _, tc := range []struct {
		name, path, body string
		status           int
		message          string
	}{
		{name: "event", path: run, body: `{"event_name":"approved","payload":{"by":"ops"}}`, status: http.StatusOK, message: `{"payload":{"by":"ops"}}`},
		{name: "no-payload", path: run, body: `{"event_name":"approved"}`, status: http.StatusOK, message: `{}`},
		{name: "missing-name", path: run, body: `{"payload":{}}`, status: http.StatusBadRequest},
		{name: "callback-name", path: run, body: `{"event_name":"workflow.callback.x"}`, status: http.StatusBadRequest},
		{name: "unknown-field", path: run, body: `{"event_name":"approved","surprise":1}`, status: http.StatusBadRequest},
		{name: "unknown-run", path: "/v1/workflows/runs/missing/events", body: `{"event_name":"approved"}`, status: http.StatusNotFound},
		{name: "other-run-route-passes-through", path: "/v1/workflows/runs/x/cancel", body: `{}`, status: http.StatusTeapot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32)}
			response := serveRehearsalPlatformWithKey(t, store, tc.path, tc.body, "")
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body)
			}
			if tc.message == "" {
				if len(store.calls) != 0 {
					t.Fatalf("refused event was captured: %+v", store.calls)
				}
				return
			}
			got := store.calls[0]
			if len(store.calls) != 1 || got.Kind != api.ScenarioPlatformKindWorkflowEvent || got.Name != "approved" || string(got.Message) != tc.message ||
				got.Address != "5d1c8b4e-2f3a-4c55-9d7e-0c1f6a2b3c4d" {
				t.Fatalf("captured %+v (%s)", store.calls, got.Message)
			}
		})
	}
}
