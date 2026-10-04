//go:build !no_pg

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type starterAsyncEnqueuer struct{ state.Store }

func (e starterAsyncEnqueuer) EnqueueAsyncRoute(ctx context.Context, req gateway.AsyncRouteRequest) (gateway.AsyncRouteAccepted, error) {
	return gateway.EnqueueAsyncRoute(ctx, e.Store, req)
}

func testCustomerPlatformQueuedWork(t *testing.T, e pgHandlerEnv, backend *tenantIngressBackend, address string,
	alice, bob api.ApplyPlatformTenantResponse, aliceKey, bobKey string) {
	t.Helper()
	ctx := context.Background()
	app, err := e.store.AppBySlug(ctx, backend.slug)
	if err != nil {
		t.Fatal(err)
	}
	edge := gateway.NewHandlerWith(backend, gateway.NewMetrics(), nil).
		WithConsumerAuth(tenantIngressConsumerStore{e.store}).
		WithEdgeRules(starterAsyncMatcher{app.ID, e.acct.ID}, nil, nil).
		WithAsyncRouteEnqueuer(starterAsyncEnqueuer{e.store})
	token := func(tenantID string) string {
		t.Helper()
		response := e.do(t, "POST", "/v1/account/platform-tenants/"+tenantID+"/access-tokens", api.CreatePlatformTenantAccessTokenRequest{
			Name: "queued work", Scopes: []string{api.ScopePlatformTenantInvocationsRead, api.ScopePlatformTenantInvocationsManage}}, nil)
		if response.Code != http.StatusCreated {
			t.Fatalf("job token: %d %s", response.Code, response.Body)
		}
		var issued api.CreatePlatformTenantAccessTokenResponse
		if err := json.Unmarshal(response.Body.Bytes(), &issued); err != nil {
			t.Fatal(err)
		}
		return issued.Token
	}
	aliceToken, bobToken := token(alice.TenantID), token(bob.TenantID)
	enqueue := func(key, idempotency, title string) api.AsyncInvokeResponse {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"title": title, "content": "queued private data"})
		r := httptest.NewRequest("POST", "http://"+backend.slug+".gregale.dev/documents", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", idempotency)
		r.Header.Set(api.PlatformTenantIDHeader, "forged")
		w := httptest.NewRecorder()
		edge.ServeHTTP(w, r)
		if w.Code != http.StatusAccepted {
			t.Fatalf("enqueue: %d %s", w.Code, w.Body)
		}
		var accepted api.AsyncInvokeResponse
		if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
			t.Fatal(err)
		}
		if accepted.StatusURL != "/v1/platform-tenant-self/invocations/"+accepted.ID {
			t.Fatalf("wrong customer status URL: %s", accepted.StatusURL)
		}
		return accepted
	}
	a, b := enqueue(aliceKey, "same-key", "Alice queued"), enqueue(bobKey, "same-key", "Bob queued")
	if a.ID == b.ID || enqueue(aliceKey, "same-key", "Alice queued").ID != a.ID {
		t.Fatal("tenant idempotency boundary failed")
	}
	if response := e.do(t, "PATCH", "/v1/account/platform-tenants/"+alice.TenantID, map[string]string{"status": state.PlatformTenantSuspended}, nil); response.Code != http.StatusOK {
		t.Fatalf("suspend queued customer: %d %s", response.Code, response.Body)
	}
	if _, err := e.store.ClaimInvocationWithCap(ctx, a.ID, "", 30, 1); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("suspended worker admission: %v", err)
	}
	held, _ := e.store.InvocationByID(ctx, a.ID)
	if held.Attempts != 0 || held.State != state.InvocationPending {
		t.Fatal("suspension consumed queued work")
	}
	// Delivery uses the same persisted admission check and guest identity renderer
	// as gatewayd-internal, with a warm loopback Node process replacing the VM.
	deliver := func(id string, retryFirst bool) string {
		t.Helper()
		claimed, err := e.store.ClaimInvocationWithCap(ctx, id, "", 30, 1)
		if err != nil {
			t.Fatal(err)
		}
		tenantID := claimed.PlatformTenantID
		if retryFirst {
			if err := e.store.FailInvocation(ctx, id, "temporary transport failure", time.Millisecond, 3, state.WithClaimAttempt(claimed.Attempts)); err != nil {
				t.Fatal(err)
			}
			claimed, err = e.store.ClaimInvocationWithCap(ctx, id, "", 30, 1)
			if err != nil || claimed.Attempts != 2 || claimed.PlatformTenantID != tenantID {
				t.Fatalf("retry identity: %+v %v", claimed, err)
			}
		}
		admitted, err := state.AdmitPlatformTenantInvocation(ctx, e.store, app.ID, claimed)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequestWithContext(ctx, admitted.Method, "http://"+address+admitted.Path, bytes.NewReader(admitted.Payload))
		if err != nil {
			t.Fatal(err)
		}
		var headers map[string]string
		if err := json.Unmarshal(admitted.Headers, &headers); err != nil {
			t.Fatal(err)
		}
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		(api.PlatformIdentity{AppID: app.ID, PlatformTenantID: admitted.PlatformTenantID}).ApplyGuestHeaders(request.Header)
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != http.StatusCreated {
			t.Fatalf("queued guest response: %d %s %v", response.StatusCode, body, err)
		}
		if err := e.store.CompleteInvocation(ctx, id, body); err != nil {
			t.Fatal(err)
		}
		var document struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &document); err != nil || document.ID == "" {
			t.Fatal("queued guest did not return a document")
		}
		return document.ID
	}
	bobDoc := deliver(b.ID, false)
	if response := e.do(t, "PATCH", "/v1/account/platform-tenants/"+alice.TenantID, map[string]string{"status": state.PlatformTenantActive}, nil); response.Code != http.StatusOK {
		t.Fatalf("resume queued customer: %d %s", response.Code, response.Body)
	}
	aliceDoc := deliver(a.ID, true)
	check := func(method, path, token string, want int) []byte {
		t.Helper()
		response := e.do(t, method, path, nil, map[string]string{"Authorization": "Bearer " + token, "Idempotency-Key": "replay-once"})
		if response.Code != want {
			t.Fatalf("job self-service %s %s: %d %s; want %d", method, path, response.Code, response.Body, want)
		}
		return response.Body.Bytes()
	}
	own := check("GET", a.StatusURL, aliceToken, http.StatusOK)
	var result api.PlatformTenantInvocationResponse
	if err := json.Unmarshal(own, &result); err != nil || result.State != "completed" || result.Attempts != 2 || !bytes.Contains(result.Result, []byte(aliceDoc)) {
		t.Fatalf("own result: %s %v", own, err)
	}
	for _, suffix := range []string{"", "/cancel", "/replay"} {
		method := "POST"
		if suffix == "" {
			method = "GET"
		}
		check(method, a.StatusURL+suffix, bobToken, http.StatusNotFound)
	}
	check("GET", b.StatusURL, aliceToken, http.StatusNotFound)
	// The actual starter DB cannot serve either customer's queued document to the other.
	syncEdge := gateway.NewHandlerWith(backend, gateway.NewMetrics(), nil).WithConsumerAuth(tenantIngressConsumerStore{e.store})
	for _, tc := range []struct {
		key, id string
		want    int
	}{{aliceKey, aliceDoc, 200}, {bobKey, bobDoc, 200}, {aliceKey, bobDoc, 404}, {bobKey, aliceDoc, 404}} {
		request := httptest.NewRequest("GET", "http://"+backend.slug+".gregale.dev/documents/"+tc.id, nil)
		request.Header.Set("Authorization", "Bearer "+tc.key)
		response := httptest.NewRecorder()
		syncEdge.ServeHTTP(response, request)
		if response.Code != tc.want {
			t.Fatalf("queued document isolation: %d %s; want %d", response.Code, response.Body, tc.want)
		}
	}
	failed := enqueue(aliceKey, "failure", "Replay me")
	if err := e.store.FailInvocation(ctx, failed.ID, "failed delivery", 0, 0); err != nil {
		t.Fatal(err)
	}
	var replay api.AsyncInvokeResponse
	response := check("POST", failed.StatusURL+"/replay", aliceToken, http.StatusAccepted)
	if err := json.Unmarshal(response, &replay); err != nil {
		t.Fatal(err)
	}
	if repeated := check("POST", failed.StatusURL+"/replay", aliceToken, http.StatusAccepted); !bytes.Equal(response, repeated) {
		t.Fatal("manual replay retry duplicated work")
	}
	check("POST", failed.StatusURL+"/replay", bobToken, http.StatusNotFound)
	deliver(replay.ID, false)
	cancelled := enqueue(aliceKey, "cancel", "Do not execute")
	check("POST", cancelled.StatusURL+"/cancel", aliceToken, http.StatusOK)
	if _, err := e.store.ClaimInvocationWithCap(ctx, cancelled.ID, "", 30, 1); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cancelled job claimed: %v", err)
	}
}
