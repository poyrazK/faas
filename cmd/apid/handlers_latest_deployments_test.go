package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestListLatestDeploymentsByAppReturnsNewestOwnedRows(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	base := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)

	appA := mustSeedApp(t, e, "latest-batch-a")
	oldA, err := e.store.CreateDeployment(ctx, state.Deployment{
		AppID: appA, ImageDigest: "sha256:old-a", Kind: state.DeploymentKindImage,
		Status: state.DeployLive, CreatedAt: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	newA, err := e.store.CreateDeployment(ctx, state.Deployment{
		AppID: appA, ImageDigest: "sha256:new-a", Kind: state.DeploymentKindImage,
		Status: state.DeployLive, CreatedAt: base.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}

	appB := mustSeedApp(t, e, "latest-batch-b")
	newB, err := e.store.CreateDeployment(ctx, state.Deployment{
		AppID: appB, ImageDigest: "sha256:new-b", Kind: state.DeploymentKindImage,
		Status: state.DeployBuilding, CreatedAt: base.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}

	deletedApp := mustSeedApp(t, e, "latest-batch-deleted")
	if _, err := e.store.CreateDeployment(ctx, state.Deployment{
		AppID: deletedApp, ImageDigest: "sha256:deleted", Kind: state.DeploymentKindImage,
		Status: state.DeployLive, CreatedAt: base.Add(3 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.DeleteApp(ctx, deletedApp); err != nil {
		t.Fatal(err)
	}

	foreign, err := e.store.CreateAccount(ctx, "foreign-latest-batch@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreignApp := mustSeedAppFor(t, e.store, foreign.ID, "latest-batch-foreign")
	if _, err := e.store.CreateDeployment(ctx, state.Deployment{
		AppID: foreignApp, ImageDigest: "sha256:foreign", Kind: state.DeploymentKindImage,
		Status: state.DeployLive, CreatedAt: base.Add(4 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, http.MethodGet, "/v1/deployments/latest-by-app", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Items []api.DeploymentResponse `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("items = %d, want 2: %+v", len(out.Items), out.Items)
	}
	if out.Items[0].ID != newB.ID || out.Items[1].ID != newA.ID {
		t.Errorf("item order = [%q, %q], want newest-first [%q, %q]", out.Items[0].ID, out.Items[1].ID, newB.ID, newA.ID)
	}
	byApp := make(map[string]string, len(out.Items))
	for _, deployment := range out.Items {
		byApp[deployment.AppID] = deployment.ID
	}
	if byApp[appA] != newA.ID {
		t.Errorf("app A latest = %q, want %q (old was %q)", byApp[appA], newA.ID, oldA.ID)
	}
	if byApp[appB] != newB.ID {
		t.Errorf("app B latest = %q, want %q", byApp[appB], newB.ID)
	}
	if _, ok := byApp[deletedApp]; ok {
		t.Error("soft-deleted app leaked into latest deployments")
	}
	if _, ok := byApp[foreignApp]; ok {
		t.Error("foreign account deployment leaked into latest deployments")
	}
}

func TestListLatestDeploymentsByAppEmptyAccountReturnsArray(t *testing.T) {
	e := setup(t, api.PlanPro)

	rec := e.do(t, http.MethodGet, "/v1/deployments/latest-by-app", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Items []api.DeploymentResponse `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Items == nil || len(out.Items) != 0 {
		t.Fatalf("items = %#v, want non-nil empty array", out.Items)
	}
}

func TestListLatestDeploymentsByAppRequiresAuthenticationAndReadScope(t *testing.T) {
	e := setup(t, api.PlanPro)

	unauthenticated := httptest.NewRecorder()
	e.h.ServeHTTP(
		unauthenticated,
		httptest.NewRequest(http.MethodGet, "/v1/deployments/latest-by-app", nil),
	)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", unauthenticated.Code)
	}

	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(
		context.Background(), e.acct.ID, hash, "usage-only", []string{api.ScopeUsageRead},
	); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/deployments/latest-by-app", nil)
	request.Header.Set("Authorization", "Bearer "+plain)
	forbidden := httptest.NewRecorder()
	e.h.ServeHTTP(forbidden, request)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("usage-only status = %d, want 403: %s", forbidden.Code, forbidden.Body)
	}
}
