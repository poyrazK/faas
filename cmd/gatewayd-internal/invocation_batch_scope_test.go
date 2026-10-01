package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// Exercises the batch HTTP decoder and the production admission adapter, not
// a direct call with the scope already present on the invocation.
func TestSynthBatchStoredScopeAndClaimAdmission(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "batch-scope@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "batch-scope", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	row, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID,
		DeploymentScope: "staging", Source: state.InvocationQueue, Method: "PUT", Path: "/stored", DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimInvocation(ctx, row.ID, "", 30)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	adapter := &synthAdapter{store: store, invokeWithStatus: func(_ context.Context, appID string, inv state.Invocation) (state.Invocation, int, error) {
		calls++
		if appID != app.ID || inv.ID != row.ID || inv.DeploymentScope != "staging" || inv.Attempts != claim.Attempts ||
			inv.Method != "POST" || inv.Path != "/_triggers/esm/test" || string(inv.Payload) != `{"job":true}` {
			t.Errorf("batch routing contract: id=%q scope=%q attempt=%d path=%q", inv.ID, inv.DeploymentScope, inv.Attempts, inv.Path)
		}
		inv.State, inv.Result = state.InvocationCompleted, []byte(`{"ok":true}`)
		return inv, http.StatusOK, nil
	}}
	server := gateway.NewSynthServer("", adapter, nil)
	t.Cleanup(func() { _ = server.Stop(ctx) })
	dispatch := func(appID, id string, attempt int) string {
		t.Helper()
		body, err := json.Marshal(map[string]any{"invocation_id": "trigger-test", "app_id": appID,
			"source": "esm", "trigger_id": "test", "records": []map[string]any{{
				"item_identifier": id, "invocation_id": id, "invocation_attempt": attempt,
				"payload_b64": base64.StdEncoding.EncodeToString([]byte(`{"job":true}`)),
			}}})
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		server.Mux().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch_batch", bytes.NewReader(body)))
		var result struct {
			Results []struct {
				Status string `json:"status"`
			} `json:"results"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK || len(result.Results) != 1 {
			t.Fatalf("batch HTTP response: status=%d err=%v", response.Code, err)
		}
		return result.Results[0].Status
	}
	if status := dispatch(app.ID, row.ID, claim.Attempts); status != "succeeded" || calls != 1 {
		t.Fatalf("valid batch: status=%q calls=%d", status, calls)
	}
	if status := dispatch(uuid.NewString(), row.ID, claim.Attempts); status != "retry" || calls != 1 {
		t.Fatalf("foreign app reached invoke: status=%q calls=%d", status, calls)
	}
	if status := dispatch(app.ID, uuid.NewString(), claim.Attempts); status != "retry" || calls != 1 {
		t.Fatalf("missing durable row reached invoke: status=%q calls=%d", status, calls)
	}
	if err := store.FailInvocation(ctx, claim.ID, "retry", time.Nanosecond, 3, state.WithClaimAttempt(claim.Attempts)); err != nil {
		t.Fatal(err)
	}
	previousAttempt := claim.Attempts
	claim, err = store.ClaimInvocation(ctx, row.ID, "", 30)
	if err != nil {
		t.Fatal(err)
	}
	if status := dispatch(app.ID, row.ID, previousAttempt); status != "retry" || calls != 1 {
		t.Fatalf("stale claim reached invoke: status=%q calls=%d", status, calls)
	}
	if status := dispatch(app.ID, row.ID, claim.Attempts); status != "succeeded" || calls != 2 {
		t.Fatalf("replacement claim delivery: status=%q calls=%d", status, calls)
	}
}

func TestSynthBatchDurableIdentityRequiresStore(t *testing.T) {
	adapter := &synthAdapter{invokeWithStatus: func(_ context.Context, _ string, inv state.Invocation) (state.Invocation, int, error) {
		t.Fatal("durable delivery reached invoke without its admission store")
		return inv, http.StatusOK, nil
	}}
	_, _, err := adapter.InvokeWithStatus(context.Background(), uuid.NewString(), state.Invocation{ID: uuid.NewString(), Source: "esm", Attempts: 1})
	if err == nil {
		t.Fatal("missing durable admission store accepted")
	}
}
