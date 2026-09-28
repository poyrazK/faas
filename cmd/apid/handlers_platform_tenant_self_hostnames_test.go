package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPlatformTenantSelfHostnameIsPolicyBoundAndRetrySafe(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	tenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "hostname-self", "Hostname self", 250)
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "hostname-self-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	limits, ok := api.LimitsFor(e.acct.Plan)
	if !ok {
		t.Fatal("missing account plan limits")
	}
	surface, err := e.store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: e.acct.ID, AppID: app.ID, Name: "hostname-self-surface",
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.LinkPlatformTenantSurface(ctx, e.acct.ID, tenant.ID, surface.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.SetPlatformTenantHostnamePolicy(ctx, e.acct.ID, tenant.ID, []string{"customers.example.com"}, 2); err != nil {
		t.Fatal(err)
	}
	tokenResp := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", api.CreatePlatformTenantAccessTokenRequest{
		Name: "hostname manager", Scopes: []string{api.ScopePlatformTenantHostnamesManage},
	}, nil)
	if tokenResp.Code != http.StatusCreated {
		t.Fatalf("create hostname token: %d %s", tokenResp.Code, tokenResp.Body)
	}
	var token api.CreatePlatformTenantAccessTokenResponse
	if err := json.Unmarshal(tokenResp.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	request := func(hostname string) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(api.CreatePlatformTenantSelfHostnameRequest{SurfaceID: surface.ID, Hostname: hostname})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/platform-tenant-self/hostnames", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token.Token)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	first := request(" Store.Customers.Example.COM ")
	if first.Code != http.StatusAccepted || first.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create hostname: %d %s cache-control=%q", first.Code, first.Body, first.Header().Get("Cache-Control"))
	}
	var created api.PlatformTenantSelfHostnameResponse
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Action != "created" || created.Hostname != "store.customers.example.com" || created.TXTRecord != "_faas-verify.store.customers.example.com" || created.ChallengeToken == "" {
		t.Fatalf("created hostname response=%+v", created)
	}
	if strings.Contains(first.Body.String(), "app_id") || strings.Contains(first.Body.String(), "last_error") || strings.Contains(first.Body.String(), "deployment_id") {
		t.Fatalf("self response exposed upstream metadata: %s", first.Body)
	}
	replay := request("store.customers.example.com")
	var unchanged api.PlatformTenantSelfHostnameResponse
	if replay.Code != http.StatusAccepted || json.Unmarshal(replay.Body.Bytes(), &unchanged) != nil || unchanged.Action != "unchanged" || unchanged.ChallengeToken != created.ChallengeToken {
		t.Fatalf("replay response: %d %+v %s", replay.Code, unchanged, replay.Body)
	}
	if rejected := request("outside.example.net"); rejected.Code != http.StatusForbidden {
		t.Fatalf("outside policy status=%d body=%s", rejected.Code, rejected.Body)
	}
	readOnlyResp := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", api.CreatePlatformTenantAccessTokenRequest{
		Name: "activation reader", Scopes: []string{api.ScopePlatformTenantActivationRead},
	}, nil)
	if readOnlyResp.Code != http.StatusCreated {
		t.Fatalf("create activation token: %d %s", readOnlyResp.Code, readOnlyResp.Body)
	}
	var readOnly api.CreatePlatformTenantAccessTokenResponse
	if err := json.Unmarshal(readOnlyResp.Body.Bytes(), &readOnly); err != nil {
		t.Fatal(err)
	}
	unauthorized := httptest.NewRequest(http.MethodPost, "/v1/platform-tenant-self/hostnames", bytes.NewReader([]byte(`{"surface_id":"`+surface.ID+`","hostname":"read-only.customers.example.com"}`)))
	unauthorized.Header.Set("Authorization", "Bearer "+readOnly.Token)
	unauthorizedRec := httptest.NewRecorder()
	e.h.ServeHTTP(unauthorizedRec, unauthorized)
	if unauthorizedRec.Code != http.StatusForbidden {
		t.Fatalf("read-only token write status=%d body=%s", unauthorizedRec.Code, unauthorizedRec.Body)
	}
}
