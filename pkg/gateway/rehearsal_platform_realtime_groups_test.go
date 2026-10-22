// adr: 20261022183526
package gateway_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	rehearsalGroupEndpoint = "8d3b7a52-1c4e-4f6a-9b2d-5e7f0a1c3b4d"
	rehearsalGroupID       = "2f6e9c1a-7b3d-4e5f-8a9b-0c1d2e3f4a5b"
	rehearsalGroupPath     = "/v1/apps/chat/realtime/endpoints/" + rehearsalGroupEndpoint + "/channels/orders/groups/billing"
	rehearsalWorkerPath    = "/v1/realtime/workers/groups/" + rehearsalGroupID
)

func rehearsalGroupStore(mutate func(*state.ScenarioRealtimeGroup)) *fakeRehearsalPlatformAPIStore {
	group := state.ScenarioRealtimeGroup{GroupID: rehearsalGroupID, EndpointID: rehearsalGroupEndpoint, Channel: "orders", Name: "billing", EndpointEnabled: true,
		LeaseSeconds: 30, MaxAttempts: 5, DeadLetterPolicy: "block", CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Filter: state.RealtimeConsumerGroupFilter{EventTypes: []string{}, Metadata: map[string]string{}}}
	if mutate != nil {
		mutate(&group)
	}
	return &fakeRehearsalPlatformAPIStore{run: strings.Repeat("a", 32), groups: map[string]state.ScenarioRealtimeGroup{
		"chat/" + rehearsalGroupEndpoint + "/orders/billing": group, rehearsalGroupID: group}}
}

func validClaimToken() string { return base64.RawURLEncoding.EncodeToString(make([]byte, 32)) }

func TestRehearsalPlatformAPIAnswersConsumerGroupsAsIdle(t *testing.T) {
	settle := `{"sequence":"5","claim_token":"` + validClaimToken() + `","action":"complete"}`
	for _, tc := range []struct {
		name, method, path, body string
		mutate                   func(*state.ScenarioRealtimeGroup)
		status                   int
		check                    func(*testing.T, []byte)
	}{
		{name: "status", method: http.MethodGet, path: rehearsalGroupPath, status: http.StatusOK, check: func(t *testing.T, body []byte) {
			var group state.RealtimeConsumerGroup
			if err := json.Unmarshal(body, &group); err != nil || group.GroupID != rehearsalGroupID || group.LeaseSeconds != 30 || group.MaxAttempts != 5 ||
				group.Ready != 0 || group.Unassigned != "0" || group.LatestSequence != "0" || group.Filter.EventTypes == nil {
				t.Fatalf("status = %s %v", body, err)
			}
		}},
		{name: "worker-status", method: http.MethodGet, path: rehearsalWorkerPath, status: http.StatusOK},
		{name: "claim", method: http.MethodPost, path: rehearsalGroupPath + "/claims", body: `{"worker":"w1"}`, status: http.StatusNoContent},
		{name: "claim-waits", method: http.MethodPost, path: rehearsalGroupPath + "/claims", body: `{"worker":"w1","wait_seconds":1}`, status: http.StatusNoContent},
		{name: "worker-claim-without-name", method: http.MethodPost, path: rehearsalWorkerPath + "/claims", body: `{}`, status: http.StatusNoContent},
		{name: "claim-without-worker", method: http.MethodPost, path: rehearsalGroupPath + "/claims", body: `{}`, status: http.StatusBadRequest},
		{name: "claim-wait-too-long", method: http.MethodPost, path: rehearsalGroupPath + "/claims", body: `{"worker":"w1","wait_seconds":21}`, status: http.StatusBadRequest},
		{name: "claim-unknown-field", method: http.MethodPost, path: rehearsalGroupPath + "/claims", body: `{"worker":"w1","x":1}`, status: http.StatusBadRequest},
		{name: "claim-trigger-bound", method: http.MethodPost, path: rehearsalGroupPath + "/claims", body: `{"worker":"w1"}`,
			mutate: func(g *state.ScenarioRealtimeGroup) { g.TriggerID = "trigger" }, status: http.StatusConflict},
		{name: "claim-disabled-endpoint", method: http.MethodPost, path: rehearsalGroupPath + "/claims", body: `{"worker":"w1"}`,
			mutate: func(g *state.ScenarioRealtimeGroup) { g.EndpointEnabled = false }, status: http.StatusBadRequest},
		{name: "batch-claim", method: http.MethodPost, path: rehearsalGroupPath + "/claims/batch", body: `{"worker":"w1","limit":16}`, status: http.StatusOK,
			check: func(t *testing.T, body []byte) {
				var batch state.RealtimeConsumerGroupBatch
				if err := json.Unmarshal(body, &batch); err != nil || batch.Claims == nil || len(batch.Claims) != 0 || batch.Paused {
					t.Fatalf("batch = %s %v", body, err)
				}
			}},
		{name: "batch-claim-over-limit", method: http.MethodPost, path: rehearsalGroupPath + "/claims/batch", body: `{"worker":"w1","limit":17}`, status: http.StatusBadRequest},
		{name: "settle", method: http.MethodPost, path: rehearsalGroupPath + "/claims/settle", body: settle, status: http.StatusConflict, check: func(t *testing.T, body []byte) {
			if !strings.Contains(string(body), "realtime_group_lease_lost") {
				t.Fatalf("settle = %s", body)
			}
		}},
		{name: "settle-bad-token", method: http.MethodPost, path: rehearsalGroupPath + "/claims/settle", body: `{"sequence":"5","claim_token":"x","action":"complete"}`, status: http.StatusBadRequest},
		{name: "settle-retry-fields-without-retry", method: http.MethodPost, path: rehearsalGroupPath + "/claims/settle",
			body: `{"sequence":"5","claim_token":"` + validClaimToken() + `","action":"complete","retry_after_seconds":5}`, status: http.StatusBadRequest},
		{name: "batch-settle", method: http.MethodPost, path: rehearsalWorkerPath + "/claims/settle/batch", body: `{"settlements":[` + settle + `]}`, status: http.StatusOK,
			check: func(t *testing.T, body []byte) {
				var result state.RealtimeConsumerGroupBatchSettlementResult
				if err := json.Unmarshal(body, &result); err != nil || len(result.Results) != 1 || result.Results[0].Status != "lease_lost" || result.Results[0].Sequence != "5" {
					t.Fatalf("batch settle = %s %v", body, err)
				}
			}},
		{name: "batch-settle-empty", method: http.MethodPost, path: rehearsalGroupPath + "/claims/settle/batch", body: `{"settlements":[]}`, status: http.StatusBadRequest},
		{name: "heartbeat", method: http.MethodPost, path: rehearsalGroupPath + "/heartbeats", body: `{"worker":"w1","ready":true}`, status: http.StatusOK,
			check: func(t *testing.T, body []byte) {
				var beat state.RealtimeWorkerHeartbeat
				if err := json.Unmarshal(body, &beat); err != nil || beat.ID == "" || beat.Worker != "w1" || !beat.Ready || !beat.Live || beat.Lost {
					t.Fatalf("heartbeat = %s %v", body, err)
				}
			}},
		{name: "heartbeat-without-ready", method: http.MethodPost, path: rehearsalGroupPath + "/heartbeats", body: `{"worker":"w1"}`, status: http.StatusBadRequest},
		{name: "unknown-group", method: http.MethodPost, path: strings.Replace(rehearsalGroupPath, "billing", "other", 1) + "/claims", body: `{"worker":"w1"}`, status: http.StatusNotFound},
		{name: "unknown-worker-group", method: http.MethodGet, path: "/v1/realtime/workers/groups/" + fakeCallID(7), status: http.StatusNotFound},
		{name: "invalid-channel", method: http.MethodGet, path: strings.Replace(rehearsalGroupPath, "orders", "%20orders", 1), status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := rehearsalGroupStore(tc.mutate)
			started := time.Now()
			response := serveRehearsalExecution(t, store, tc.method, tc.path, tc.body, "")
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body)
			}
			if tc.name == "claim-waits" && time.Since(started) < time.Second {
				t.Fatal("an idle claim returned before its wait_seconds")
			}
			if response.Code < 300 && response.Header().Get("X-Gregale-Rehearsal") != "idle" {
				t.Fatalf("headers = %v", response.Header())
			}
			if len(store.calls) != 0 {
				t.Fatalf("a consumer group call was captured: %+v", store.calls)
			}
			if tc.check != nil {
				tc.check(t, response.Body.Bytes())
			}
		})
	}
}
