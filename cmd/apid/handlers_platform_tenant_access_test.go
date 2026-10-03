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
	"github.com/onebox-faas/faas/pkg/state"
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
	activation := request(http.MethodGet, "/v1/platform-tenant-self/activation")
	if activation.Code != http.StatusForbidden {
		t.Fatalf("out-of-scope activation status=%d, want 403: %s", activation.Code, activation.Body)
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

func TestPlatformTenantSelfActivationIsScopedAndRedacted(t *testing.T) {
	withTenantSurfacesEnabled(t)
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	tenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "activation-self", "Activation self", 250)
	if err != nil {
		t.Fatal(err)
	}
	appID := mustSeedApp(t, e, "activation-self")
	surfaceID := seedTenantSurface(t, e, appID, "Customer web")
	if _, err := e.store.LinkPlatformTenantSurface(ctx, e.acct.ID, tenant.ID, surfaceID); err != nil {
		t.Fatal(err)
	}
	otherTenant, _, err := e.store.CreatePlatformTenant(ctx, e.acct.ID, "activation-other", "Other tenant", 250)
	if err != nil {
		t.Fatal(err)
	}
	otherSurfaceID := seedTenantSurface(t, e, appID, "Other customer web")
	if _, err := e.store.LinkPlatformTenantSurface(ctx, e.acct.ID, otherTenant.ID, otherSurfaceID); err != nil {
		t.Fatal(err)
	}
	limits, ok := api.LimitsFor(e.acct.Plan)
	if !ok {
		t.Fatalf("LimitsFor(%q) returned false", e.acct.Plan)
	}
	const challenge = "dns-challenge-must-not-leak"
	if _, err := e.store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{
		SurfaceID: surfaceID, Hostname: "activation.customer.example", ChallengeToken: challenge,
	}, limits); err != nil {
		t.Fatal(err)
	}
	const providerFailure = "private certificate provider response"
	if err := e.store.MarkTenantHostnameCheckFailed(ctx, "activation.customer.example", providerFailure); err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateTenantSurfaceCert(ctx, state.UpdateSurfaceCertParams{
		SurfaceID: surfaceID, CertState: state.CertStateFailed, LastError: providerFailure,
	}); err != nil {
		t.Fatal(err)
	}
	const privateCommit = "0123456789abcdef0123456789abcdef01234567"
	deployment, err := e.store.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:private-image-digest",
		CommitSHA: privateCommit, Error: "private deployment failure details", Status: state.DeployFailed,
	})
	if err != nil {
		t.Fatal(err)
	}
	created := e.do(t, http.MethodPost, "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", api.CreatePlatformTenantAccessTokenRequest{
		Name: "activation portal", Scopes: []string{api.ScopePlatformTenantActivationRead},
	}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create activation token: %d %s", created.Code, created.Body)
	}
	var token api.CreatePlatformTenantAccessTokenResponse
	if err := json.Unmarshal(created.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	request := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token.Token)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	response := request("/v1/platform-tenant-self/activation")
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("self activation: %d %s; cache-control=%q", response.Code, response.Body, response.Header().Get("Cache-Control"))
	}
	var snapshot api.PlatformTenantSelfActivationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Ready || len(snapshot.Surfaces) != 1 || snapshot.Surfaces[0].ID != surfaceID || snapshot.Surfaces[0].CertState != string(state.CertStateFailed) ||
		snapshot.Surfaces[0].Ready || len(snapshot.Surfaces[0].Hostnames) != 1 ||
		snapshot.Surfaces[0].Hostnames[0].Verified {
		t.Fatalf("self activation snapshot = %+v", snapshot)
	}
	deploymentView := snapshot.Surfaces[0].LatestDeployment
	if deploymentView == nil || deploymentView.Status != string(deployment.Status) || deploymentView.Revision != deployment.Revision ||
		deploymentView.StartedAt != deployment.CreatedAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("self activation deployment status = %+v, want safe projection of %+v", deploymentView, deployment)
	}
	body := response.Body.String()
	for _, forbidden := range []string{"tenant_id", "app_id", appID, deployment.ID, deployment.ImageDigest, privateCommit,
		"cert_last_error", "last_error", "challenge_token", "txt_record", challenge, providerFailure, deployment.Error} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("self activation leaked %q: %s", forbidden, body)
		}
	}
	accountReq := httptest.NewRequest(http.MethodGet, "/v1/account/platform-tenants/"+tenant.ID, nil)
	accountReq.Header.Set("Authorization", "Bearer "+token.Token)
	accountRec := httptest.NewRecorder()
	e.h.ServeHTTP(accountRec, accountReq)
	if accountRec.Code != http.StatusForbidden {
		t.Fatalf("activation token reached account route: %d %s", accountRec.Code, accountRec.Body)
	}
	spoofedSelector := request("/v1/platform-tenant-self/activation?tenant_id=" + otherTenant.ID)
	var selected api.PlatformTenantSelfActivationResponse
	if spoofedSelector.Code != http.StatusOK || json.Unmarshal(spoofedSelector.Body.Bytes(), &selected) != nil ||
		len(selected.Surfaces) != 1 || selected.Surfaces[0].ID != surfaceID {
		t.Fatalf("caller-selected tenant changed the self snapshot: %d %s", spoofedSelector.Code, spoofedSelector.Body)
	}
}
