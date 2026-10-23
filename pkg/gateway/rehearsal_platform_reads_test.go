// adr: 20261022082721
package gateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func getRehearsalPlatform(t *testing.T, store *fakeRehearsalPlatformAPIStore, path string) *httptest.ResponseRecorder {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusTeapot)
	})
	handler := gateway.WithRehearsalPlatformAPI(next, func(context.Context, string) (string, error) { return "caller", nil }, store)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "api.svc.gregale:10081"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestRehearsalPlatformAPIReadsBackCapturedWork(t *testing.T) {
	created := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	scheduled := created.Add(time.Minute)
	const (
		send    = "11111111-1111-4111-8111-111111111111"
		task    = "22222222-2222-4222-8222-222222222222"
		invoke  = "33333333-3333-4333-8333-333333333333"
		run     = "44444444-4444-4444-8444-444444444444"
		missing = "55555555-5555-4555-8555-555555555555"
	)
	store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32),
		queueRecords: map[string]state.ScenarioCapturedQueueRecord{
			send:   {Kind: api.ScenarioQueueKindQueue, Target: "workload:worker", QueueName: "imports", Message: json.RawMessage(`{"payload":{"order":1}}`), CreatedAt: created},
			task:   {Kind: api.ScenarioQueueKindDelayedTask, Target: "app:billing", Schedule: "delay:60", Message: json.RawMessage(`{"payload":{"invoice":7}}`), ScheduledAt: &scheduled, CreatedAt: created},
			invoke: {Kind: api.ScenarioQueueKindAsyncInvoke, Target: "app:billing", Message: json.RawMessage(`{"invoke":{"method":"PUT","path":"/sync","payload":{"x":1}},"deadline":""}`), CreatedAt: created},
		},
		platformRecords: map[string]state.ScenarioCapturedPlatformRecord{
			run: {Kind: api.ScenarioPlatformKindWorkflowRun, Target: "workload:billing", Name: "invoice", Message: json.RawMessage(`{"input":{"order":3}}`), CreatedAt: created},
		},
		eventRecords: map[[2]string]state.ScenarioCapturedEventRecord{{"orders", "evt-1"}: {Type: "order.created", SchemaVersion: "1", CreatedAt: created}},
	}

	var inv api.Invocation
	response := getRehearsalPlatform(t, store, "/v1/invocations/"+send)
	if response.Code != http.StatusOK || json.NewDecoder(response.Body).Decode(&inv) != nil || inv.State != "pending" || inv.Source != "queue" ||
		inv.QueueName != "imports" || string(inv.Payload) != `{"order":1}` || response.Header().Get("X-Gregale-Rehearsal") != "captured" {
		t.Fatalf("queue send read = %d %+v", response.Code, inv)
	}
	response = getRehearsalPlatform(t, store, "/v1/invocations/"+invoke)
	inv = api.Invocation{}
	if response.Code != http.StatusOK || json.NewDecoder(response.Body).Decode(&inv) != nil || inv.Source != "async_invoke" || inv.Method != "PUT" || inv.Path != "/sync" || string(inv.Payload) != `{"x":1}` {
		t.Fatalf("async invoke read = %d %+v", response.Code, inv)
	}
	var delayed api.DelayedTaskResponse
	response = getRehearsalPlatform(t, store, "/v1/delayed-tasks/"+task)
	if response.Code != http.StatusOK || json.NewDecoder(response.Body).Decode(&delayed) != nil || !delayed.ScheduledAt.Equal(scheduled) || delayed.State != "pending" {
		t.Fatalf("delayed task read = %d %+v", response.Code, delayed)
	}
	if response := getRehearsalPlatform(t, store, "/v1/delayed-tasks/"+send); response.Code != http.StatusNotFound {
		t.Fatalf("a queue send read as a delayed task: %d", response.Code)
	}
	var workflow api.WorkflowRunResponse
	response = getRehearsalPlatform(t, store, "/v1/workflows/runs/"+run)
	if response.Code != http.StatusOK || json.NewDecoder(response.Body).Decode(&workflow) != nil || workflow.Status != state.WorkflowRunStatusPending ||
		workflow.WorkflowName != "invoice" || string(workflow.Input) != `{"order":3}` {
		t.Fatalf("workflow run read = %d %+v", response.Code, workflow)
	}
	var receipt api.EventReceiptResponse
	response = getRehearsalPlatform(t, store, "/v1/events/receipt?source=orders&id=evt-1")
	if response.Code != http.StatusOK || json.NewDecoder(response.Body).Decode(&receipt) != nil || receipt.EventType != "order.created" ||
		receipt.RecipientCount != 0 || len(receipt.Recipients) != 0 || !receipt.AcceptedAt.Equal(created) {
		t.Fatalf("event receipt read = %d %+v", response.Code, receipt)
	}
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/v1/invocations/" + missing, http.StatusNotFound},
		{"/v1/workflows/runs/" + missing, http.StatusNotFound},
		{"/v1/events/receipt?source=orders&id=evt-2", http.StatusNotFound},
		{"/v1/events/receipt?source=orders", http.StatusBadRequest},
		{"/v1/workflows/runs/" + run + "/steps", http.StatusTeapot},
		{"/v1/apps/billing", http.StatusTeapot},
	} {
		if response := getRehearsalPlatform(t, store, tc.path); response.Code != tc.status {
			t.Fatalf("GET %s = %d, want %d: %s", tc.path, response.Code, tc.status, response.Body)
		}
	}
	uncontained := &fakeRehearsalPlatformAPIStore{queueRecords: store.queueRecords}
	if response := getRehearsalPlatform(t, uncontained, "/v1/invocations/"+send); response.Code != http.StatusTeapot {
		t.Fatalf("an uncontained caller's read was served: %d", response.Code)
	}
}

func TestRehearsalPlatformAPICapturesInboxAsyncInvokeAndOrgSends(t *testing.T) {
	inAnHour := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	for _, tc := range []struct {
		name, path, body string
		status           int
		kind, queue      string
		message          string
	}{
		{name: "inbox", path: "/v1/apps/billing/inbox", body: `{"type":"invoice.due","data":{"invoice":7},"queue_name":"billing"}`, status: http.StatusAccepted,
			kind: api.ScenarioQueueKindQueue, queue: "billing",
			message: `{"environment":"","flag_context":"","inbox":{"data":{"invoice":7},"datacontenttype":"application/json","source":"gregale.send","type":"invoice.due"},"queue_name":"billing","retry_policy":null,"work":null}`},
		{name: "inbox-without-type", path: "/v1/apps/billing/inbox", body: `{"data":{}}`, status: http.StatusBadRequest},
		{name: "async-invoke", path: "/v1/apps/billing/invoke/async", body: `{"path":"/sync","payload":{"x":1},"deadline_at":"` + inAnHour + `"}`, status: http.StatusAccepted,
			kind: api.ScenarioQueueKindAsyncInvoke, message: `{"deadline":"absolute","invoke":{"payload":{"x":1},"method":"POST","path":"/sync"}}`},
		{name: "async-invoke-past-deadline", path: "/v1/apps/billing/invoke/async", body: `{"deadline_at":"2001-01-01T00:00:00Z"}`, status: http.StatusUnprocessableEntity},
		{name: "async-invoke-negative-retention", path: "/v1/apps/billing/invoke/async", body: `{"retention_seconds":-1}`, status: http.StatusUnprocessableEntity},
		{name: "org-queue-send", path: "/v1/orgs/acme/apps/billing/queues/send", body: `{"payload":{"order":1}}`, status: http.StatusCreated,
			kind: api.ScenarioQueueKindQueue, message: `{"payload":{"order":1}}`},
		{name: "org-other-route-passes-through", path: "/v1/orgs/acme/apps/billing/queues/receive", body: `{}`, status: http.StatusTeapot},
		{name: "delayed-negative-retention", path: "/v1/apps/billing/delayed-tasks", body: `{"delay_seconds":5,"retention_seconds":-1}`, status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32)}
			response := serveRehearsalPlatformWithKey(t, store, tc.path, tc.body, "")
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body)
			}
			if tc.kind == "" {
				if len(store.captured) != 0 {
					t.Fatalf("refused request was captured: %+v", store.captured)
				}
				return
			}
			got := store.captured[0]
			if len(store.captured) != 1 || got.Kind != tc.kind || got.Slug != "billing" || got.QueueName != tc.queue || string(got.Message) != tc.message {
				t.Fatalf("captured %+v (%s), want kind %s message %s", store.captured, got.Message, tc.kind, tc.message)
			}
			var body struct {
				ID        string `json:"id"`
				StatusURL string `json:"status_url"`
				EventID   string `json:"event_id"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.ID == "" {
				t.Fatalf("response = %+v %v", body, err)
			}
			if tc.status == http.StatusAccepted && body.StatusURL != "/v1/invocations/"+body.ID {
				t.Fatalf("status URL = %q", body.StatusURL)
			}
			if tc.name == "inbox" && body.EventID == "" {
				t.Fatal("inbox response has no event ID")
			}
		})
	}
}

// A send delivered within the run reads back with its invocation's state
// (ADR-20261023111825).
func TestRehearsalPlatformAPIReadsDeliveredSendState(t *testing.T) {
	const delivered, captured = "55555555-5555-4555-8555-555555555555", "66666666-6666-4666-8666-666666666666"
	created := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32), queueRecords: map[string]state.ScenarioCapturedQueueRecord{
		delivered: {Kind: api.ScenarioQueueKindQueue, Target: "workload:worker", Message: json.RawMessage(`{"payload":{"order":7}}`), CreatedAt: created,
			Delivery: api.ScenarioQueueDelivered, DeliveryState: "succeeded"},
		captured: {Kind: api.ScenarioQueueKindQueue, Target: "workload:worker", Message: json.RawMessage(`{"payload":{}}`), CreatedAt: created,
			Delivery: api.ScenarioQueueUnsupported},
	}}
	for id, want := range map[string]string{delivered: "succeeded", captured: string(state.InvocationPending)} {
		response := getRehearsalPlatform(t, store, "/v1/invocations/"+id)
		var inv api.Invocation
		if err := json.NewDecoder(response.Body).Decode(&inv); err != nil || response.Code != http.StatusOK || inv.State != want {
			t.Fatalf("read %s = %d %+v %v, want %s", id, response.Code, inv, err, want)
		}
	}
}
