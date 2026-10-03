package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPlatformTenantInvocationSelfService(t *testing.T) {
	e := setup(t, api.PlanHobby)
	ctx := context.Background()
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "tenant-work", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	type customer struct {
		tenant state.PlatformTenant
		token  string
	}
	customers := map[string]customer{}
	for _, name := range []string{"alice", "bob"} {
		tenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, name, name, 250)
		if err != nil {
			t.Fatal(err)
		}
		created := e.do(t, "POST", "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", api.CreatePlatformTenantAccessTokenRequest{
			Name: "jobs", Scopes: []string{api.ScopePlatformTenantInvocationsRead, api.ScopePlatformTenantInvocationsManage}}, nil)
		if created.Code != http.StatusCreated {
			t.Fatalf("issue job token: %d %s", created.Code, created.Body)
		}
		var token api.CreatePlatformTenantAccessTokenResponse
		if err := json.Unmarshal(created.Body.Bytes(), &token); err != nil {
			t.Fatal(err)
		}
		customers[name] = customer{tenant, token.Token}
	}
	alice, bob := customers["alice"], customers["bob"]
	enqueue := func(tenant string) state.Invocation {
		t.Helper()
		inv, err := e.store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: e.acct.ID, PlatformTenantID: tenant,
			Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/documents", Headers: []byte(`{"Secret":"private"}`), Payload: []byte(`{"secret":"private"}`), DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	inv, unbound := enqueue(alice.tenant.ID), enqueue("")
	path := "/v1/platform-tenant-self/invocations/" + inv.ID
	call := func(method, path, token string, want int) string {
		t.Helper()
		response := e.do(t, method, path, nil, map[string]string{"Authorization": "Bearer " + token, "Idempotency-Key": "same-key"})
		if response.Code != want {
			t.Fatalf("%s %s: %d %s; want %d", method, path, response.Code, response.Body, want)
		}
		if response.Header().Get("Cache-Control") != "no-store" && want != http.StatusForbidden {
			t.Fatal("tenant result may be cached")
		}
		return response.Body.String()
	}
	own := call("GET", path, alice.token, http.StatusOK)
	for _, forbidden := range []string{"private", "payload", "headers", "account_id", "app_id", "platform_tenant_id"} {
		if strings.Contains(own, forbidden) {
			t.Fatalf("status leaked %s", forbidden)
		}
	}
	call("GET", path, e.key, http.StatusForbidden)
	for _, id := range []string{inv.ID, unbound.ID, uuid.NewString()} {
		for _, suffix := range []string{"", "/cancel", "/replay"} {
			method := "POST"
			if suffix == "" {
				method = "GET"
			}
			call(method, "/v1/platform-tenant-self/invocations/"+id+suffix, bob.token, http.StatusNotFound)
		}
	}
	call("POST", path+"/replay", alice.token, http.StatusConflict)
	if err := e.store.FailInvocation(ctx, inv.ID, "temporary", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.SetPlatformTenantStatus(ctx, e.acct.ID, alice.tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	call("GET", path, alice.token, http.StatusOK)
	call("POST", path+"/replay", alice.token, http.StatusForbidden)
	if _, err := e.store.SetPlatformTenantStatus(ctx, e.acct.ID, alice.tenant.ID, state.PlatformTenantActive); err != nil {
		t.Fatal(err)
	}
	var accepted api.AsyncInvokeResponse
	first := call("POST", path+"/replay", alice.token, http.StatusAccepted)
	if err := json.Unmarshal([]byte(first), &accepted); err != nil {
		t.Fatal(err)
	}
	if second := call("POST", path+"/replay", alice.token, http.StatusAccepted); second != first {
		t.Fatal("replay retry created different work")
	}
	// The same key on the same path cannot expose Alice's cached acceptance.
	call("POST", path+"/replay", bob.token, http.StatusNotFound)
	replay, err := e.store.InvocationByID(ctx, accepted.ID)
	if err != nil || replay.PlatformTenantID != alice.tenant.ID || replay.Attempts != 0 {
		t.Fatalf("replay identity: %+v %v", replay, err)
	}
	call("POST", accepted.StatusURL+"/cancel", alice.token, http.StatusOK)
	call("POST", accepted.StatusURL+"/cancel", alice.token, http.StatusOK)
	cancelled, _ := e.store.InvocationByID(ctx, accepted.ID)
	if cancelled.State != state.InvocationCancelled || cancelled.PlatformTenantID != alice.tenant.ID {
		t.Fatalf("cancelled tenant work: %+v", cancelled)
	}
	ownerReplay := e.do(t, "POST", "/v1/invocations/"+inv.ID+"/replay", nil, nil)
	if ownerReplay.Code != http.StatusAccepted {
		t.Fatalf("owner replay: %d %s", ownerReplay.Code, ownerReplay.Body)
	}
	if err := json.Unmarshal(ownerReplay.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	replay, _ = e.store.InvocationByID(ctx, accepted.ID)
	if replay.PlatformTenantID != alice.tenant.ID {
		t.Fatal("owner replay dropped tenant identity")
	}
}
