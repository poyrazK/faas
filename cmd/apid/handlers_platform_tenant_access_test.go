package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPlatformTenantAccessTokenOwnUsageAndStatementsOnly(t *testing.T) {
	e := setup(t, api.PlanPro)
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID, "self-service", "Self service", 250)
	if err != nil {
		t.Fatal(err)
	}
	created := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", api.CreatePlatformTenantAccessTokenRequest{
		Name: "customer billing", Scopes: []string{api.ScopePlatformTenantStatementsRead},
	}, nil)
	if created.Code != http.StatusCreated || created.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create token: %d %s; cache-control=%q", created.Code, created.Body, created.Header().Get("Cache-Control"))
	}
	var token api.CreatePlatformTenantAccessTokenResponse
	if err := json.Unmarshal(created.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	if !api.ValidPlatformTenantAccessTokenFormat(token.Token) || token.TenantID != tenant.ID {
		t.Fatalf("created tenant token = %+v", token)
	}
	metadata := e.do(t, http.MethodGet, "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", nil, nil)
	if metadata.Code != http.StatusOK || strings.Contains(metadata.Body.String(), token.Token) || strings.Contains(metadata.Body.String(), `"token"`) {
		t.Fatalf("token list returned plaintext or failed: %d %s", metadata.Code, metadata.Body)
	}

	request := func(method, path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token.Token)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	listed := request(http.MethodGet, "/v1/platform-tenant-self/usage-statements?period_start="+start+"&period_end="+end)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"statements":[]`) || strings.Contains(listed.Body.String(), `"lines"`) {
		t.Fatalf("tenant statement list: %d %s", listed.Code, listed.Body)
	}
	usage := request(http.MethodGet, "/v1/platform-tenant-self/usage")
	if usage.Code != http.StatusForbidden {
		t.Fatalf("out-of-scope usage status=%d, want 403: %s", usage.Code, usage.Body)
	}
	accountRoute := request(http.MethodGet, "/v1/account/platform-tenants/"+tenant.ID)
	if accountRoute.Code != http.StatusForbidden {
		t.Fatalf("account route status=%d, want 403: %s", accountRoute.Code, accountRoute.Body)
	}
	writeRoute := request(http.MethodPost, "/v1/platform-tenant-self/usage-statements")
	if writeRoute.Code != http.StatusMethodNotAllowed {
		t.Fatalf("self-service write status=%d, want 405: %s", writeRoute.Code, writeRoute.Body)
	}

	revoked := e.do(t, http.MethodDelete, "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens/"+token.ID, nil, nil)
	if revoked.Code != http.StatusOK {
		t.Fatalf("revoke token: %d %s", revoked.Code, revoked.Body)
	}
	if tokenUse := request(http.MethodGet, "/v1/platform-tenant-self/usage-statements?period_start="+start+"&period_end="+end); tokenUse.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token status=%d, want 401: %s", tokenUse.Code, tokenUse.Body)
	}
}

func TestCreatePlatformTenantAccessTokenRejectsAccountWideScopes(t *testing.T) {
	e := setup(t, api.PlanPro)
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID, "bad-scope", "Bad scope", 250)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(api.CreatePlatformTenantAccessTokenRequest{Name: "too broad", Scopes: []string{api.ScopeAdmin}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+e.key)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("account scope token creation: %d %s", rec.Code, rec.Body)
	}
	if rows, err := e.store.ListPlatformTenantAccessTokens(context.Background(), e.acct.ID, tenant.ID); err != nil || len(rows) != 0 {
		t.Fatalf("invalid scope wrote token rows: %+v, %v", rows, err)
	}
}
