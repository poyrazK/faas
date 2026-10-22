// adr: 20261022191742
package gateway_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRehearsalPlatformAPICapturesRealtimeBatches(t *testing.T) {
	path := realtimeChannelPath + "/publish-batch"
	message := func(extra string) string { return `{"data_base64":"aGk="` + extra + `}` }
	many := make([]string, 33)
	for i := range many {
		many[i] = message("")
	}
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{name: "batch", body: `{"batch_id":"b1","ttl_seconds":60,"messages":[` + message("") + `,{"data_base64":"Ynll","ttl_seconds":5,"metadata":{"k":"v"}}]}`, status: http.StatusOK,
			want: `{"delivery":"retained","messages":[{"data_base64":"aGk=","binary":false,"metadata":null,"ttl_seconds":60},{"data_base64":"Ynll","binary":false,"metadata":{"k":"v"},"ttl_seconds":5}]}`},
		{name: "conditional", body: `{"batch_id":"b1","expected_sequence":7,"messages":[` + message("") + `]}`, status: http.StatusOK,
			want: `{"delivery":"retained","expected_sequence":7,"messages":[{"data_base64":"aGk=","binary":false,"metadata":null,"ttl_seconds":0}]}`},
		{name: "empty", body: `{"batch_id":"b1","messages":[]}`, status: http.StatusBadRequest},
		{name: "too-many", body: `{"batch_id":"b1","messages":[` + strings.Join(many, ",") + `]}`, status: http.StatusBadRequest},
		{name: "message-expected-sequence", body: `{"batch_id":"b1","messages":[` + message(`,"expected_sequence":1`) + `]}`, status: http.StatusBadRequest},
		{name: "untrimmed-batch-id", body: `{"batch_id":" b1","messages":[` + message("") + `]}`, status: http.StatusBadRequest},
		{name: "ttl-too-long", body: `{"batch_id":"b1","ttl_seconds":86401,"messages":[` + message("") + `]}`, status: http.StatusBadRequest},
		{name: "payload-too-large", body: `{"batch_id":"b1","messages":[{"data_base64":"` + strings.Repeat("A", 5468) + `"}]}`, status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32)}
			response := serveRehearsalExecution(t, store, http.MethodPost, path, tc.body, "")
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body)
			}
			if tc.want == "" {
				if len(store.calls) != 0 {
					t.Fatalf("refused batch was captured: %+v", store.calls)
				}
				return
			}
			got := store.calls[0]
			if len(store.calls) != 1 || got.Kind != api.ScenarioPlatformKindRealtimePublish || got.Address != "orders" || got.Name != "updates" || got.IdempotencyKey != "b1" ||
				string(got.Message) != tc.want {
				t.Fatalf("captured %+v (%s), want %s", got, got.Message, tc.want)
			}
			var out api.ManagedRealtimeChannelBatchResponse
			if err := json.NewDecoder(response.Body).Decode(&out); err != nil || out.BatchID != "b1" || !out.Durable || out.Partial || len(out.Messages) == 0 || out.Messages[0].Sequence != 0 {
				t.Fatalf("response = %+v %v", out, err)
			}
		})
	}
	store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32)}
	body := `{"batch_id":"b1","messages":[` + message("") + `]}`
	serveRehearsalExecution(t, store, http.MethodPost, path, body, "")
	if again := serveRehearsalExecution(t, store, http.MethodPost, path, body, ""); again.Code != http.StatusOK || again.Header().Get("Idempotent-Replayed") != "true" || len(store.calls) != 1 {
		t.Fatalf("replayed batch = %d %v, %d captures", again.Code, again.Header(), len(store.calls))
	}
}

func TestRehearsalPlatformAPICapturesPublisherCredentialPublishes(t *testing.T) {
	const endpoint = "7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	path := "/v1/realtime/publishers/endpoints/" + endpoint + "/channels/updates/publish"
	for _, tc := range []struct {
		name, path, body, key string
		status                int
	}{
		{name: "publish", path: path, body: `{"event_type":"order.created","data_base64":"aGk=","metadata":{"k":"v"},"ttl_seconds":60}`, key: "k1", status: http.StatusOK},
		{name: "no-key", path: path, body: `{"event_type":"order.created","data_base64":"aGk="}`, status: http.StatusBadRequest},
		{name: "no-event-type", path: path, body: `{"data_base64":"aGk="}`, key: "k1", status: http.StatusBadRequest},
		{name: "event-type-mismatch", path: path, body: `{"event_type":"order.created","data_base64":"aGk=","metadata":{"event_type":"other"}}`, key: "k1", status: http.StatusBadRequest},
		{name: "unknown-field", path: path, body: `{"event_type":"order.created","data_base64":"aGk=","x":1}`, key: "k1", status: http.StatusBadRequest},
		{name: "unknown-endpoint", path: strings.Replace(path, endpoint, "00000000-0000-4000-8000-000000000404", 1), body: `{"event_type":"order.created","data_base64":"aGk="}`, key: "k1",
			status: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32)}
			response := serveRehearsalExecution(t, store, http.MethodPost, tc.path, tc.body, tc.key)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body)
			}
			if tc.status != http.StatusOK {
				if len(store.calls) != 0 {
					t.Fatalf("refused publish was captured: %+v", store.calls)
				}
				return
			}
			got := store.calls[0]
			want := `{"binary":false,"data_base64":"aGk=","delivery":"retained","event_type":"order.created","metadata":{"event_type":"order.created","k":"v"},"publisher":true,"ttl_seconds":60}`
			if got.Kind != api.ScenarioPlatformKindRealtimePublish || got.Address != "" || got.Endpoint != endpoint || got.IdempotencyKey != "k1" || string(got.Message) != want {
				t.Fatalf("captured %+v (%s)", got, got.Message)
			}
			if body := strings.TrimSpace(response.Body.String()); body != `{"durable":true,"partial":false,"replayed":false,"sequence":"0"}` {
				t.Fatalf("response = %s", body)
			}
		})
	}
}

func TestRehearsalPlatformAPIRefusesDurableEntities(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1/apps/orders/entities/invoke"},
		{http.MethodPost, "/v1/apps/orders/entities/retry"},
		{http.MethodGet, "/v1/apps/orders/entities/inspect?namespace=cart&key=1"},
	} {
		store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32)}
		response := serveRehearsalExecution(t, store, tc.method, tc.path, `{"namespace":"cart","key":"1","request_id":"r","payload":{}}`, "")
		if response.Code != http.StatusNotImplemented || !strings.Contains(response.Body.String(), "Durable entities unavailable in a rehearsal") || len(store.calls) != 0 {
			t.Fatalf("%s %s = %d %s", tc.method, tc.path, response.Code, response.Body)
		}
	}
}

func TestRehearsalPlatformAPIAnswersEventRecipientReads(t *testing.T) {
	accepted := time.Date(2026, 10, 22, 9, 0, 0, 123456000, time.UTC)
	store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32),
		eventRecords: map[[2]string]state.ScenarioCapturedEventRecord{{"orders", "evt-1"}: {Type: "order.created", CreatedAt: accepted}}}
	query := func(values url.Values) string { return values.Encode() }
	recipient := url.Values{"source": {"orders"}, "id": {"evt-1"}, "subscription_id": {"sub-1"}}
	at := func(t time.Time) url.Values {
		values := url.Values{"source": {"orders"}, "id": {"evt-1"}, "subscription_id": {"sub-1"}, "expected_accepted_at": {t.Format(time.RFC3339Nano)}}
		return values
	}
	for _, tc := range []struct {
		name, method, path, body string
		status                   int
		contains                 string
	}{
		{name: "replays", method: http.MethodGet, path: "/v1/events/receipt/replays?" + query(recipient), status: http.StatusNotFound, contains: "Event receipt not found"},
		{name: "attempts", method: http.MethodGet, path: "/v1/events/receipt/attempts?" + query(recipient), status: http.StatusNotFound},
		{name: "replays-without-subscription", method: http.MethodGet, path: "/v1/events/receipt/replays?source=orders&id=evt-1", status: http.StatusBadRequest},
		{name: "replays-with-cursor", method: http.MethodGet, path: "/v1/events/receipt/replays?" + query(recipient) + "&after=x", status: http.StatusBadRequest},
		{name: "attempts-bad-limit", method: http.MethodGet, path: "/v1/events/receipt/attempts?" + query(recipient) + "&limit=201", status: http.StatusBadRequest},
		{name: "blocker", method: http.MethodGet, path: "/v1/events/recipient-blocker?" + query(at(accepted)), status: http.StatusOK, contains: `"reason":"recipient_unavailable"`},
		{name: "blocker-replaced", method: http.MethodGet, path: "/v1/events/recipient-blocker?" + query(at(accepted.Add(time.Second))), status: http.StatusOK,
			contains: `"status":"acceptance_replaced"`},
		{name: "blocker-unknown-event", method: http.MethodGet, path: "/v1/events/recipient-blocker?" + strings.Replace(query(at(accepted)), "evt-1", "evt-2", 1), status: http.StatusOK,
			contains: `"reason":"receipt_unavailable"`},
		{name: "blocker-without-acceptance", method: http.MethodGet, path: "/v1/events/recipient-blocker?" + query(recipient), status: http.StatusBadRequest},
		{name: "blockers", method: http.MethodPost, path: "/v1/events/recipient-blockers",
			body: `{"queries":[{"source":"orders","id":"evt-1","subscription_id":"s","expected_accepted_at":"` + accepted.Format(time.RFC3339Nano) + `"}]}`, status: http.StatusOK,
			contains: `"reason":"recipient_unavailable"`},
		{name: "blockers-empty", method: http.MethodPost, path: "/v1/events/recipient-blockers", body: `{"queries":[]}`, status: http.StatusBadRequest},
		{name: "receipt-replaced", method: http.MethodGet, path: "/v1/events/receipt?source=orders&id=evt-1&expected_accepted_at=" + url.QueryEscape(accepted.Add(time.Second).Format(time.RFC3339Nano)),
			status: http.StatusConflict, contains: "event_acceptance_replaced"},
		{name: "receipt-same-acceptance", method: http.MethodGet, path: "/v1/events/receipt?source=orders&id=evt-1&expected_accepted_at=" + url.QueryEscape(accepted.Format(time.RFC3339Nano)),
			status: http.StatusOK},
		{name: "receipt-with-cursor", method: http.MethodGet, path: "/v1/events/receipt?source=orders&id=evt-1&after=x", status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := serveRehearsalExecution(t, store, tc.method, tc.path, tc.body, "")
			if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.contains) {
				t.Fatalf("status = %d, want %d containing %q: %s", response.Code, tc.status, tc.contains, response.Body)
			}
		})
	}
	if len(store.calls) != 0 {
		t.Fatalf("a read was captured: %+v", store.calls)
	}
}
