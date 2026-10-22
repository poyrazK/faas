// adr: 20261021090000
package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type fakeRehearsalPlatformAPIStore struct {
	run      string
	plan     api.Plan
	err      error
	captured []state.ScenarioQueueCapture
	events   []state.ScenarioEventCapture
	eventRun string
	calls    []state.ScenarioPlatformCallCapture
	// Read-back fixtures keyed by capture ID (events by source and ID).
	queueRecords    map[string]state.ScenarioCapturedQueueRecord
	platformRecords map[string]state.ScenarioCapturedPlatformRecord
	eventRecords    map[[2]string]state.ScenarioCapturedEventRecord
}

func (f *fakeRehearsalPlatformAPIStore) ScenarioCapturedQueueMessage(_ context.Context, accountID, runID, id string) (state.ScenarioCapturedQueueRecord, error) {
	record, ok := f.queueRecords[id]
	if !ok || accountID != fakeRehearsalAccount || runID != f.run {
		return state.ScenarioCapturedQueueRecord{}, state.ErrNotFound
	}
	return record, nil
}

func (f *fakeRehearsalPlatformAPIStore) ScenarioCapturedPlatformCall(_ context.Context, accountID, runID, id string) (state.ScenarioCapturedPlatformRecord, error) {
	record, ok := f.platformRecords[id]
	if !ok {
		record, ok = f.capturedRecord(id)
	}
	if !ok || accountID != fakeRehearsalAccount || runID != f.run {
		return state.ScenarioCapturedPlatformRecord{}, state.ErrNotFound
	}
	return record, nil
}

// capturedRecord reads back the fake's own capture, cancelled by the first
// later cancellation that addressed it.
func (f *fakeRehearsalPlatformAPIStore) capturedRecord(id string) (state.ScenarioCapturedPlatformRecord, bool) {
	for i, call := range f.calls {
		if fakeCallID(i) != id {
			continue
		}
		record := state.ScenarioCapturedPlatformRecord{Kind: call.Kind, Name: call.Name, Message: call.Message, Oversized: call.Oversized, CreatedAt: fakeCapturedAt}
		for _, later := range f.calls[i+1:] {
			if call.Kind == api.ScenarioPlatformKindExecution && later.Kind == api.ScenarioPlatformKindExecutionCancel && later.Address == id {
				at := fakeCapturedAt.Add(time.Minute)
				record.CancelledAt = &at
				break
			}
		}
		return record, true
	}
	return state.ScenarioCapturedPlatformRecord{}, false
}

var fakeCapturedAt = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func (f *fakeRehearsalPlatformAPIStore) ScenarioCapturedEvent(_ context.Context, accountID, runID, source, eventID string) (state.ScenarioCapturedEventRecord, error) {
	record, ok := f.eventRecords[[2]string{source, eventID}]
	if !ok || accountID != fakeRehearsalAccount || runID != f.run {
		return state.ScenarioCapturedEventRecord{}, state.ErrNotFound
	}
	return record, nil
}

const fakeRehearsalAccount = "5b0f6f0e-8a43-4d0e-9a4f-2d8f7a1c9e01"

func (f *fakeRehearsalPlatformAPIStore) ScenarioRehearsalNetworkPolicy(context.Context, string) (state.ScenarioRehearsalNetworkPolicy, bool, error) {
	return state.ScenarioRehearsalNetworkPolicy{RunID: f.run, AccountID: fakeRehearsalAccount, Plan: f.plan}, f.run != "", nil
}

// CaptureScenarioEvent mirrors the store's identity rule: the same source and
// ID with the same content is a duplicate, different content a conflict.
func (f *fakeRehearsalPlatformAPIStore) CaptureScenarioEvent(_ context.Context, appID, installedRunID string, event state.ScenarioEventCapture) (bool, error) {
	if appID != "caller" {
		return false, nil
	}
	if f.err != nil {
		return true, f.err
	}
	f.eventRun = installedRunID
	for _, prior := range f.events {
		if prior.Source == event.Source && prior.ID == event.ID {
			if prior.Type == event.Type && string(prior.Data) == string(event.Data) {
				return true, state.ErrScenarioEventDuplicate
			}
			return true, state.ErrScenarioEventIdentityConflict
		}
	}
	f.events = append(f.events, event)
	return true, nil
}

// CaptureScenarioPlatformCall records calls; an unknown address is
// ErrNotFound and a reused key replays the same name, conflicts otherwise.
func (f *fakeRehearsalPlatformAPIStore) CaptureScenarioPlatformCall(_ context.Context, appID string, call state.ScenarioPlatformCallCapture) (state.ScenarioPlatformCallResult, bool, error) {
	if appID != "caller" {
		return state.ScenarioPlatformCallResult{}, false, nil
	}
	if f.err != nil {
		return state.ScenarioPlatformCallResult{}, true, f.err
	}
	if call.Address == "missing" {
		return state.ScenarioPlatformCallResult{}, true, state.ErrNotFound
	}
	if record, ok := f.capturedRecord(call.Address); call.Kind == api.ScenarioPlatformKindExecutionCancel && (!ok || record.Kind != api.ScenarioPlatformKindExecution) {
		return state.ScenarioPlatformCallResult{}, true, state.ErrNotFound
	}
	for i, prior := range f.calls {
		if call.IdempotencyKey != "" && prior.IdempotencyKey == call.IdempotencyKey && prior.Kind == call.Kind && prior.Address == call.Address {
			if prior.Name != call.Name && call.Kind != api.ScenarioPlatformKindExecution {
				return state.ScenarioPlatformCallResult{}, true, state.ErrScenarioIdempotencyConflict
			}
			return state.ScenarioPlatformCallResult{ID: fakeCallID(i), Replayed: true}, true, nil
		}
	}
	f.calls = append(f.calls, call)
	return state.ScenarioPlatformCallResult{ID: fakeCallID(len(f.calls) - 1)}, true, nil
}

func fakeCallID(i int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", i) }

func (f *fakeRehearsalPlatformAPIStore) PutEventSchema(context.Context, state.EventSchema) (bool, error) {
	return false, nil
}

func (f *fakeRehearsalPlatformAPIStore) ListEventSchemasForSourceType(context.Context, string, string, string) ([]state.EventSchema, error) {
	return nil, nil
}

// LookupEventSchemaForPublish registers no schemas: unversioned events pass
// and any schema version is unknown, as for an account without a registry.
func (f *fakeRehearsalPlatformAPIStore) LookupEventSchemaForPublish(context.Context, string, string, string, string) (bool, json.RawMessage, error) {
	return false, nil, nil
}

func (f *fakeRehearsalPlatformAPIStore) CaptureScenarioQueueMessage(_ context.Context, appID string, msg state.ScenarioQueueCapture) (string, bool, error) {
	if appID != "caller" {
		return "", false, nil
	}
	if f.err != nil {
		return "", true, f.err
	}
	f.captured = append(f.captured, msg)
	return "8f0e2b4c-2f3a-4c55-9d7e-0c1f6a2b3c4d", true, nil
}

func TestRehearsalPlatformAPICapturesContainedQueueWork(t *testing.T) {
	inAnHour := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	for _, tc := range []struct {
		name, host, path, body string
		contained              bool
		storeErr               error
		status                 int
		passThrough            bool
		want                   *state.ScenarioQueueCapture
	}{
		{name: "service-host-passes-through", host: "worker.svc.gregale:10081", path: "/v1/apps/orders/queues/send", body: `{}`, contained: true, passThrough: true},
		{name: "uncontained-caller-passes-through", host: "api.svc.gregale:10081", path: "/v1/apps/orders/queues/send", body: `{"payload":1}`, passThrough: true},
		{name: "other-platform-route-passes-through", host: "api.svc.gregale", path: "/v1/apps/orders/invoke", body: `{}`, contained: true, passThrough: true},
		{name: "queue-send", host: "api.svc.gregale:10081", path: "/v1/apps/orders/queues/send", body: `{"payload":{"order":1},"queue_name":"imports"}`, contained: true, status: http.StatusCreated,
			want: &state.ScenarioQueueCapture{Kind: api.ScenarioQueueKindQueue, Slug: "orders", QueueName: "imports", Message: json.RawMessage(`{"payload":{"order":1},"queue_name":"imports"}`)}},
		{name: "delayed-task-delay", host: "api.svc.gregale", path: "/v1/apps/billing/delayed-tasks", body: `{"payload":{"invoice":7},"delay_seconds":60}`, contained: true, status: http.StatusCreated,
			want: &state.ScenarioQueueCapture{Kind: api.ScenarioQueueKindDelayedTask, Slug: "billing", Schedule: "delay:60", Message: json.RawMessage(`{"payload":{"invoice":7}}`)}},
		{name: "delayed-task-absolute", host: "api.svc.gregale", path: "/v1/apps/billing/delayed-tasks", body: `{"payload":1,"scheduled_at":"` + inAnHour + `"}`, contained: true, status: http.StatusCreated,
			want: &state.ScenarioQueueCapture{Kind: api.ScenarioQueueKindDelayedTask, Slug: "billing", Schedule: "absolute", Message: json.RawMessage(`{"payload":1}`)}},
		{name: "delayed-task-beyond-a-year", host: "api.svc.gregale", path: "/v1/apps/billing/delayed-tasks", body: `{"payload":1,"scheduled_at":"2999-01-01T00:00:00Z"}`, contained: true, status: http.StatusBadRequest},
		{name: "delayed-task-both-schedules", host: "api.svc.gregale", path: "/v1/apps/billing/delayed-tasks", body: `{"delay_seconds":5,"scheduled_at":"2999-01-01T00:00:00Z"}`, contained: true, status: http.StatusBadRequest},
		{name: "unknown-field", host: "api.svc.gregale", path: "/v1/apps/orders/queues/send", body: `{"payload":1,"surprise":true}`, contained: true, status: http.StatusBadRequest},
		{name: "trailing-data", host: "api.svc.gregale", path: "/v1/apps/orders/queues/send", body: `{"payload":1}{}`, contained: true, status: http.StatusBadRequest},
		{name: "oversized", host: "api.svc.gregale", path: "/v1/apps/orders/queues/send", body: `{"payload":"` + strings.Repeat("x", api.MaxScenarioCapturedQueueMessageBytes) + `"}`, contained: true, status: http.StatusCreated,
			want: &state.ScenarioQueueCapture{Kind: api.ScenarioQueueKindQueue, Slug: "orders", Oversized: true}},
		{name: "unknown-target", host: "api.svc.gregale", path: "/v1/apps/orders/queues/send", body: `{}`, contained: true, storeErr: state.ErrNotFound, status: http.StatusNotFound},
		{name: "sealed-run", host: "api.svc.gregale", path: "/v1/apps/orders/queues/send", body: `{}`, contained: true, storeErr: state.ErrScenarioActionsSealed, status: http.StatusConflict},
		{name: "storage-unavailable", host: "api.svc.gregale", path: "/v1/apps/orders/queues/send", body: `{}`, contained: true, storeErr: errors.New("down"), status: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeRehearsalPlatformAPIStore{err: tc.storeErr}
			if tc.contained {
				store.run = strings.Repeat("a", 32)
			}
			var forwarded string
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				forwarded = string(body)
				w.WriteHeader(http.StatusTeapot)
			})
			resolve := func(context.Context, string) (string, error) { return "caller", nil }
			handler := gateway.WithRehearsalPlatformAPI(next, resolve, store)
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Host = tc.host
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if tc.passThrough {
				if response.Code != http.StatusTeapot || forwarded != tc.body || len(store.captured) != 0 {
					t.Fatalf("request was not passed through unchanged: %d %q %+v", response.Code, forwarded, store.captured)
				}
				return
			}
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body)
			}
			if tc.want == nil {
				if tc.storeErr == nil && len(store.captured) != 0 {
					t.Fatalf("refused request was captured: %+v", store.captured)
				}
				return
			}
			if response.Header().Get("X-Gregale-Rehearsal") != "captured" || len(store.captured) != 1 {
				t.Fatalf("capture = %+v, headers %v", store.captured, response.Header())
			}
			got := store.captured[0]
			if got.Kind != tc.want.Kind || got.Slug != tc.want.Slug || got.QueueName != tc.want.QueueName || got.Schedule != tc.want.Schedule ||
				got.Oversized != tc.want.Oversized || string(got.Message) != string(tc.want.Message) {
				t.Fatalf("captured %+v (%s), want %+v (%s)", got, got.Message, *tc.want, tc.want.Message)
			}
			var body struct {
				ID          string `json:"id"`
				ScheduledAt string `json:"scheduled_at"`
				State       string `json:"state"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.ID == "" {
				t.Fatalf("response body: %+v %v", body, err)
			}
			if got.Kind == api.ScenarioQueueKindDelayedTask && (body.ScheduledAt == "" || body.State != string(state.InvocationPending)) {
				t.Fatalf("delayed task response = %+v", body)
			}
		})
	}
}
