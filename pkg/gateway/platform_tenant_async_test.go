package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 376
// Async HTTP work preserves verified tenant identity and scopes idempotency to that tenant.
func TestApplyEdgeRuleAsyncTenantIdentity(t *testing.T) {
	enqueuer := &recordingAsyncRouteEnqueuer{}
	h := &Handler{asyncRoutes: enqueuer}
	r := httptest.NewRequest("POST", "http://api.example.com/documents", strings.NewReader(`{"title":"queued"}`))
	r.Header.Set(api.PlatformTenantIDHeader, "forged")
	r.Header.Set("Authorization", "Bearer private")
	r = r.WithContext(withAuthenticated(r.Context(), Authenticated{PlatformTenantID: "verified"}))
	w := httptest.NewRecorder()
	h.applyEdgeRuleAsync(w, r, App{ID: "app", AccountID: "account", Plan: api.PlanHobby, RequestInvocationsEnabled: true}, &EdgeRuleAsyncResolved{ID: "async"})
	if w.Code != http.StatusAccepted || enqueuer.request.PlatformTenantID != "verified" {
		t.Fatalf("tenant enqueue: %d %+v", w.Code, enqueuer.request)
	}
	if _, ok := enqueuer.request.Headers[api.PlatformTenantIDHeader]; ok {
		t.Fatal("forged identity persisted as header")
	}
	var response api.AsyncInvokeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.StatusURL != "/v1/platform-tenant-self/invocations/"+response.ID {
		t.Fatalf("customer status URL: %s", response.StatusURL)
	}
}

func TestAsyncRouteTenantIdempotencyIsolation(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "tenant-async@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "tenant-async", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, name := range []string{"alice", "bob"} {
		tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, name, name, 250)
		if err != nil {
			t.Fatal(err)
		}
		req := AsyncRouteRequest{AppID: app.ID, AccountID: account.ID, PlatformTenantID: tenant.ID, Method: "POST", Path: "/documents", Payload: []byte(`{}`), IdempotencyKey: "same-key"}
		first, err := EnqueueAsyncRoute(ctx, store, req)
		if err != nil {
			t.Fatal(err)
		}
		second, err := EnqueueAsyncRoute(ctx, store, req)
		if err != nil || first.ID != second.ID {
			t.Fatalf("same customer retry: %+v %+v %v", first, second, err)
		}
		ids[name] = first.ID
	}
	if ids["alice"] == ids["bob"] {
		t.Fatal("idempotency key collided across customers")
	}
}
