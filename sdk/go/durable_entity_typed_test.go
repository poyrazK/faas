// ADR-938.
package faas_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestTypedEntityCallInitializesLocallyAndPreservesAlarm(t *testing.T) {
	request := entityHandlerFixture(t)
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	request.State.AlarmAt = &at
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	initial := map[string]int{"count": 7}
	call, err := faas.DecodeDurableEntityCall[map[string]int, map[string]any](body, initial)
	if err != nil || call.State["count"] != 7 || call.Version != 0 {
		t.Fatal(call, err)
	}
	call.State["count"]++
	if initial["count"] != 7 {
		t.Fatal("initial state aliases handler state")
	}
	transition := call.Transition(call.State, map[string]int{"count": 8})
	encoded, err := transition.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var result faas.DurableEntityTransition
	if json.Unmarshal(encoded, &result) != nil || result.AlarmAt == nil || !result.AlarmAt.Equal(at) || result.Outbox != nil {
		t.Fatal(string(encoded))
	}
	if _, err := transition.ClearAlarm().Encode(); err != nil {
		t.Fatal(err)
	}
	encoded, err = call.Transition(call.State, nil).ScheduleAlarm(at.Add(time.Hour)).Encode()
	if err != nil || json.Unmarshal(encoded, &result) != nil || !result.AlarmAt.Equal(at.Add(time.Hour)) {
		t.Fatal(string(encoded), err)
	}
	request.State.Version = 3
	request.State.Data = json.RawMessage(`{"count":"invalid"}`)
	body, _ = json.Marshal(request)
	if _, err := faas.DecodeDurableEntityCall[map[string]int, map[string]any](body, initial); err == nil {
		t.Fatal("committed state was silently reset")
	}
}

func TestTypedEntityCallPoisonsRejectedIntent(t *testing.T) {
	request := entityHandlerFixture(t)
	body, _ := json.Marshal(request)
	call, err := faas.DecodeDurableEntityCall[map[string]any, map[string]any](body, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]int{"count": 1}
	transition := call.Transition(call.State, nil)
	if err := transition.Webhook("6dd283da-3c14-40de-9d47-bb71fb35be9a", "counter.updated", payload); err != nil {
		t.Fatal(err)
	}
	payload["count"] = 9
	encoded, err := transition.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var result faas.DurableEntityTransition
	if json.Unmarshal(encoded, &result) != nil || len(result.Outbox) != 1 || string(result.Outbox[0].Payload) != `{"count":1}` {
		t.Fatal(string(encoded))
	}
	if err := transition.Webhook("invalid", "counter.updated", payload); !errors.Is(err, faas.ErrDurableEntityHandler) {
		t.Fatal(err)
	}
	if _, err := transition.Encode(); err == nil {
		t.Fatal("ignored intent error allowed partial publication")
	}
	request.ProtocolVersion = 1
	request.Limits = nil
	body, _ = json.Marshal(request)
	call, err = faas.DecodeDurableEntityCall[map[string]any, map[string]any](body, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if err := call.Transition(call.State, nil).Webhook("6dd283da-3c14-40de-9d47-bb71fb35be9a", "counter.updated", payload); err == nil {
		t.Fatal("v1 produced outgoing work")
	}
}

func TestTypedEntityHandleKeepsScopeAndCommitMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	calls := 0
	scope := faas.DurableEntityInspectRequest{Namespace: "documents", Key: "document:/?+&雪", Environment: "staging", PlatformTenantID: "tenant"}
	expectedScope := scope
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("key") != expectedScope.Key || r.URL.Query().Get("environment") != expectedScope.Environment || r.URL.Query().Get("platform_tenant_id") != expectedScope.PlatformTenantID {
				t.Error(r.URL)
			}
			_ = json.NewEncoder(w).Encode(faas.DurableEntityInspectResponse{Version: 1})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/retry") {
			var request faas.DurableEntityRetryRequest
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.Key != expectedScope.Key || request.Environment != expectedScope.Environment || request.PlatformTenantID != expectedScope.PlatformTenantID || request.ExpectedRecoveryRevision != strings.Repeat("a", 64) || request.ExpectedVersion != 1 {
				t.Error(request)
			}
			_ = json.NewEncoder(w).Encode(faas.DurableEntityRetryResponse{Version: 1, Target: "outbox", Rearmed: true})
			return
		}
		var request faas.DurableEntityInvokeRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.RequestID != "stable" || request.Key != expectedScope.Key || request.Environment != expectedScope.Environment || request.PlatformTenantID != expectedScope.PlatformTenantID {
			t.Error(request)
		}
		_ = json.NewEncoder(w).Encode(faas.DurableEntityInvokeResponse{Version: ^uint64(0), Replayed: calls > 1, Value: json.RawMessage(`"not-an-integer"`)})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	handle, err := faas.NewDurableEntityHandle[map[string]float64, int](client, "app", scope)
	if err != nil {
		t.Fatal(err)
	}
	scope.Key = "changed"
	result, err := handle.Invoke(ctx, "stable", map[string]float64{"delta": 1})
	if err == nil || result.Version != ^uint64(0) || result.Replayed {
		t.Fatal("committed decoding failure lost metadata", result, err)
	}
	if _, err := handle.Invoke(ctx, "", map[string]float64{}); err == nil {
		t.Fatal("generated an implicit request identity")
	}
	if _, err := handle.Invoke(ctx, "stable", map[string]float64{"delta": math.NaN()}); err == nil {
		t.Fatal("non-JSON payload reached API")
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	if _, err := handle.Inspect(ctx); err != nil {
		t.Fatal(err)
	}
	response, err := handle.Retry(ctx, faas.DurableEntityRetryRequest{Namespace: "foreign", Key: "foreign", Environment: "foreign", PlatformTenantID: "foreign", Target: "outbox", ExpectedVersion: 1, ExpectedRecoveryRevision: strings.Repeat("a", 64), HeadID: "6dd283da-3c14-40de-9d47-bb71fb35be9a"})
	if err != nil || !response.Rearmed || calls != 3 {
		t.Fatal(response, err, calls)
	}
}
