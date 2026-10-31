// adr: 20261021180000
package gateway_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const realtimeChannelPath = "/v1/apps/orders/realtime/endpoints/7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d/channels/updates"

func TestRehearsalPlatformAPICapturesContainedRealtimePublish(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, key string
		status                int
		message               string
	}{
		{name: "live", path: realtimeChannelPath + "/publish", body: `{"data_base64":"aGk="}`, status: http.StatusOK,
			message: `{"binary":false,"data_base64":"aGk=","delivery":"live","expected_sequence":null,"metadata":null,"ttl_seconds":0}`},
		{name: "retained", path: realtimeChannelPath + "/publish?delivery=retained", body: `{"data_base64":"aGk=","ttl_seconds":60}`, key: "k1", status: http.StatusOK,
			message: `{"binary":false,"data_base64":"aGk=","delivery":"retained","expected_sequence":null,"metadata":null,"ttl_seconds":60}`},
		{name: "retained-without-key", path: realtimeChannelPath + "/publish?delivery=retained", body: `{"data_base64":"aGk="}`, status: http.StatusBadRequest},
		{name: "ttl-on-live", path: realtimeChannelPath + "/publish", body: `{"data_base64":"aGk=","ttl_seconds":60}`, status: http.StatusBadRequest},
		{name: "bad-delivery", path: realtimeChannelPath + "/publish?delivery=later", body: `{"data_base64":"aGk="}`, status: http.StatusBadRequest},
		{name: "bad-base64", path: realtimeChannelPath + "/publish", body: `{"data_base64":"***"}`, status: http.StatusBadRequest},
		{name: "bad-key", path: realtimeChannelPath + "/publish", body: `{"data_base64":"aGk="}`, key: "has space", status: http.StatusBadRequest},
		{name: "unknown-endpoint", path: "/v1/apps/missing/realtime/endpoints/7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d/channels/updates/publish", body: `{"data_base64":"aGk="}`, status: http.StatusNotFound},
		{name: "batch-passes-through", path: realtimeChannelPath + "/publish-batch", body: `{}`, status: http.StatusTeapot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32)}
			response := serveRehearsalPlatformWithKey(t, store, tc.path, tc.body, tc.key)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body)
			}
			if tc.message == "" {
				if len(store.calls) != 0 {
					t.Fatalf("refused publish was captured: %+v", store.calls)
				}
				return
			}
			got := store.calls[0]
			if len(store.calls) != 1 || got.Kind != api.ScenarioPlatformKindRealtimePublish || got.Address != "orders" || got.Endpoint != "7a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d" ||
				got.Name != "updates" || got.IdempotencyKey != tc.key || string(got.Message) != tc.message {
				t.Fatalf("captured %+v (%s)", store.calls, got.Message)
			}
			var out api.ManagedRealtimePublishResponse
			if err := json.NewDecoder(response.Body).Decode(&out); err != nil || out.Queued != 0 || out.Subscribers != 0 || response.Header().Get("X-Gregale-Rehearsal") != "captured" {
				t.Fatalf("publish response = %+v %v", out, err)
			}
		})
	}
}

func TestRehearsalPlatformAPICapturesContainedRealtimeSignals(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, channel string
		status                    int
	}{
		{name: "signal", path: realtimeChannelPath + "/signals", body: `{"data":{"typing":true},"name":"typing"}`, channel: "updates", status: http.StatusOK},
		{name: "unnamed", path: realtimeChannelPath + "/signals", body: `{"data":{"x":1}}`, channel: "updates", status: http.StatusOK},
		{name: "ttl-without-name", path: realtimeChannelPath + "/signals", body: `{"data":{"x":1},"ttl_ms":10}`, status: http.StatusBadRequest},
		{name: "ttl-too-long", path: realtimeChannelPath + "/signals", body: `{"data":{"x":1},"name":"t","ttl_ms":30001}`, status: http.StatusBadRequest},
		{name: "empty-data", path: realtimeChannelPath + "/signals", body: `{"name":"t"}`, status: http.StatusBadRequest},
		{name: "oversized-data", path: realtimeChannelPath + "/signals", body: `{"data":"` + strings.Repeat("x", 2100) + `"}`, status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32)}
			response := serveRehearsalPlatformWithKey(t, store, tc.path, tc.body, "")
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body)
			}
			if tc.channel == "" {
				if len(store.calls) != 0 {
					t.Fatalf("refused signal was captured: %+v", store.calls)
				}
				return
			}
			if len(store.calls) != 1 || store.calls[0].Kind != api.ScenarioPlatformKindRealtimeSignal || store.calls[0].Name != tc.channel {
				t.Fatalf("captured %+v", store.calls)
			}
			var out api.ManagedRealtimeSignalResponse
			if err := json.NewDecoder(response.Body).Decode(&out); err != nil || !out.Accepted || out.MemberID != "backend" {
				t.Fatalf("signal response = %+v %v", out, err)
			}
		})
	}
}
