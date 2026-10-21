// adr: 20261021120000
package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func serveRehearsalPlatform(t *testing.T, store *fakeRehearsalPlatformAPIStore, path, body string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	forwarded := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		forwarded = true
		w.WriteHeader(http.StatusTeapot)
	})
	handler := gateway.WithRehearsalPlatformAPI(next, func(context.Context, string) (string, error) { return "caller", nil }, store)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Host = "api.svc.gregale:10081"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response, forwarded
}

func TestRehearsalPlatformAPICapturesContainedEventPublish(t *testing.T) {
	event := `{"id":"evt-1","source":"orders","type":"order.created","data":{"order":1}}`
	for _, tc := range []struct {
		name, body string
		contained  bool
		storeErr   error
		status     int
		captured   int
	}{
		{name: "uncontained-caller-passes-through", body: event, status: http.StatusTeapot},
		{name: "publish", body: event, contained: true, status: http.StatusAccepted, captured: 1},
		{name: "missing-id", body: `{"source":"orders","type":"order.created","data":{}}`, contained: true, status: http.StatusBadRequest},
		{name: "reserved-source", body: `{"id":"e","source":"gregale.deploy","type":"x","data":{}}`, contained: true, status: http.StatusBadRequest},
		{name: "other-account", body: `{"id":"e","source":"orders","type":"x","data":{},"accountid":"6c1f6f0e-8a43-4d0e-9a4f-2d8f7a1c9e02"}`, contained: true, status: http.StatusForbidden},
		{name: "unknown-schema-version", body: `{"id":"e","source":"orders","type":"x","data":{},"schemaversion":"1"}`, contained: true, status: http.StatusUnprocessableEntity},
		// PublishEventRequest's own unmarshaler ignores unknown fields in
		// production too.
		{name: "unknown-field-accepted", body: `{"id":"evt-1","source":"orders","type":"order.created","data":{"order":1},"surprise":1}`, contained: true, status: http.StatusAccepted, captured: 1},
		{name: "too-large", body: `{"id":"e","source":"orders","type":"x","data":"` + strings.Repeat("x", 1<<20) + `"}`, contained: true, status: http.StatusRequestEntityTooLarge},
		{name: "sealed-run", body: event, contained: true, storeErr: state.ErrScenarioActionsSealed, status: http.StatusConflict},
		{name: "storage-unavailable", body: event, contained: true, storeErr: errors.New("down"), status: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeRehearsalPlatformAPIStore{err: tc.storeErr}
			if tc.contained {
				store.run = strings.Repeat("a", 32)
			}
			response, forwarded := serveRehearsalPlatform(t, store, "/v1/events:publish", tc.body)
			if response.Code != tc.status || forwarded != (tc.status == http.StatusTeapot) || len(store.events) != tc.captured {
				t.Fatalf("status=%d forwarded=%v captured=%+v: %s", response.Code, forwarded, store.events, response.Body)
			}
			if tc.captured == 0 {
				return
			}
			var receipt api.PublishEventResponse
			if err := json.NewDecoder(response.Body).Decode(&receipt); err != nil || receipt.ID != "evt-1" || receipt.AccountID != fakeRehearsalAccount ||
				receipt.ReceiptURL != "/v1/events/receipt?id=evt-1&source=orders" || response.Header().Get("Location") != receipt.ReceiptURL ||
				response.Header().Get("X-Gregale-Rehearsal") != "captured" {
				t.Fatalf("receipt = %+v %v, headers %v", receipt, err, response.Header())
			}
			got := store.events[0]
			if store.eventRun != store.run || got.ID != "evt-1" || got.AccountID != fakeRehearsalAccount || got.Source != "orders" || got.Type != "order.created" || string(got.Data) != `{"order":1}` {
				t.Fatalf("captured %+v into run %q", got, store.eventRun)
			}
		})
	}
}

func TestRehearsalPlatformAPICapturesContainedEventBatch(t *testing.T) {
	store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32)}
	body := `{"events":[
		{"id":"evt-1","source":"orders","type":"order.created","data":{"order":1}},
		{"id":"evt-1","source":"orders","type":"order.created","data":{"order":1}},
		{"id":"evt-1","source":"orders","type":"order.created","data":{"order":2}},
		{"id":"evt-2","source":"gregale.deploy","type":"x","data":{}},
		"not an event"]}`
	response, forwarded := serveRehearsalPlatform(t, store, "/v1/events:publish-batch", body)
	if response.Code != http.StatusOK || forwarded {
		t.Fatalf("status=%d forwarded=%v: %s", response.Code, forwarded, response.Body)
	}
	var out api.PublishEventBatchResponse
	if err := json.NewDecoder(response.Body).Decode(&out); err != nil || len(out.Results) != 5 {
		t.Fatalf("batch response = %+v %v", out, err)
	}
	want := []struct {
		status  string
		problem int
	}{{"accepted", 0}, {"duplicate", 0}, {"rejected", http.StatusConflict}, {"rejected", http.StatusBadRequest}, {"rejected", http.StatusBadRequest}}
	for i, result := range out.Results {
		status := 0
		if result.Problem != nil {
			status = result.Problem.Status
		}
		if result.Index != i || result.Status != want[i].status || status != want[i].problem || result.Retryable {
			t.Fatalf("result %d = %+v, want %+v", i, result, want[i])
		}
	}
	if len(store.events) != 1 {
		t.Fatalf("captured %+v, want one event", store.events)
	}
	for _, empty := range []string{`{"events":[]}`, `{"events":[` + strings.TrimSuffix(strings.Repeat(`{},`, 101), ",") + `]}`, `{"events":[],"extra":1}`} {
		if response, _ := serveRehearsalPlatform(t, store, "/v1/events:publish-batch", empty); response.Code != http.StatusBadRequest {
			t.Fatalf("batch %q status = %d", empty[:20], response.Code)
		}
	}
}
