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
		DeploymentScope: "staging", Source: state.InvocationQueue, Method: "PUT", Path: "/stored",
		Payload: []byte(`{"job":true}`), DueAt: time.Now()})
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
		if appID != app.ID || inv.ID != row.ID || inv.AccountID != account.ID || inv.DeploymentScope != "staging" ||
			inv.Attempts != claim.Attempts || inv.ReplayGeneration != claim.ReplayGeneration ||
			inv.Method != "POST" || inv.Path != "/_triggers/esm/test" || string(inv.Payload) != `{"job":true}` {
			t.Errorf("batch routing contract: id=%q scope=%q attempt=%d path=%q", inv.ID, inv.DeploymentScope, inv.Attempts, inv.Path)
		}
		inv.State, inv.Result = state.InvocationCompleted, []byte(`{"ok":true}`)
		return inv, http.StatusOK, nil
	}}
	server := gateway.NewSynthServer("", adapter, nil)
	t.Cleanup(func() { _ = server.Stop(ctx) })
	dispatch := func(appID, id string, attempt int, generation int64, payload string) string {
		t.Helper()
		body, err := json.Marshal(map[string]any{"invocation_id": "trigger-test", "app_id": appID,
			"source": "esm", "trigger_id": "test", "records": []map[string]any{{
				"item_identifier": id, "invocation_id": id, "invocation_attempt": attempt, "invocation_replay_generation": generation,
				"payload_b64": base64.StdEncoding.EncodeToString([]byte(payload)),
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
	if status := dispatch(app.ID, row.ID, claim.Attempts, 0, `{"job":true}`); status != "succeeded" || calls != 1 {
		t.Fatalf("valid batch: status=%q calls=%d", status, calls)
	}
	if status := dispatch(app.ID, row.ID, claim.Attempts, 0, `{"job":false}`); status != "retry" || calls != 1 {
		t.Fatalf("tampered payload reached invoke: status=%q calls=%d", status, calls)
	}
	if status := dispatch(uuid.NewString(), row.ID, claim.Attempts, 0, `{"job":true}`); status != "retry" || calls != 1 {
		t.Fatalf("foreign app reached invoke: status=%q calls=%d", status, calls)
	}
	if status := dispatch(app.ID, uuid.NewString(), claim.Attempts, 0, `{"job":true}`); status != "retry" || calls != 1 {
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
	if status := dispatch(app.ID, row.ID, previousAttempt, 0, `{"job":true}`); status != "retry" || calls != 1 {
		t.Fatalf("stale claim reached invoke: status=%q calls=%d", status, calls)
	}
	if status := dispatch(app.ID, row.ID, claim.Attempts, 0, `{"job":true}`); status != "succeeded" || calls != 2 {
		t.Fatalf("replacement claim delivery: status=%q calls=%d", status, calls)
	}
	if err := store.FailInvocation(ctx, row.ID, "exhausted", time.Nanosecond, 2, state.WithClaimAttempt(claim.Attempts)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RetryQueueDeadLetter(ctx, account.ID, row.ID); err != nil {
		t.Fatal(err)
	}
	claim, err = store.ClaimInvocation(ctx, row.ID, "", 30)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Attempts != 1 || claim.ReplayGeneration != 1 {
		t.Fatalf("replay fence=%+v", claim)
	}
	if status := dispatch(app.ID, row.ID, 1, 0, `{"job":true}`); status != "retry" || calls != 2 {
		t.Fatalf("prior replay generation reached invoke: status=%q calls=%d", status, calls)
	}
	if status := dispatch(app.ID, row.ID, 1, 1, `{"job":true}`); status != "succeeded" || calls != 3 {
		t.Fatalf("current replay generation rejected: status=%q calls=%d", status, calls)
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

func TestSynthBatchRestoresDelayedTaskScope(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "batch-delayed-task@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "batch-delayed-task", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	row, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID,
		DeploymentScope: "staging", Source: state.InvocationDelayedTask, Payload: []byte(`{"job":true}`), DueAt: time.Now()})
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
		if appID != app.ID || inv.ID != row.ID || inv.Source != "esm" || inv.DeploymentScope != "staging" ||
			inv.Attempts != claim.Attempts || inv.ReplayGeneration != claim.ReplayGeneration {
			t.Errorf("delayed-task batch identity: id=%q source=%q scope=%q attempt=%d", inv.ID, inv.Source, inv.DeploymentScope, inv.Attempts)
		}
		return inv, http.StatusOK, nil
	}}
	inv := state.Invocation{ID: claim.ID, AppID: app.ID, Source: "esm", Method: http.MethodPost,
		Path: "/_triggers/esm/test", Payload: []byte(`{"job":true}`), Attempts: claim.Attempts,
		ReplayGeneration: claim.ReplayGeneration}
	if _, _, err := adapter.InvokeWithStatus(ctx, app.ID, inv); err != nil || calls != 1 {
		t.Fatalf("delayed-task push delivery: calls=%d err=%v", calls, err)
	}
}
