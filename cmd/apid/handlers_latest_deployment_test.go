package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGetLatestAppDeploymentReturnsNewest(t *testing.T) {
	e := setup(t, api.PlanPro)
	first := mustSeedDeployment(t, e, "latest-app")
	app, err := e.store.AppBySlug(context.Background(), "latest-app")
	if err != nil {
		t.Fatal(err)
	}
	latest, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:" + repeat("a", 64), Kind: state.DeploymentKindImage,
		Status: state.DeployLive, CreatedAt: first.CreatedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, http.MethodGet, "/v1/apps/latest-app/deployments/latest", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ID != latest.ID || got.AppID != app.ID {
		t.Errorf("latest deployment = (%q, %q), want (%q, %q)", got.ID, got.AppID, latest.ID, app.ID)
	}
}

func TestGetLatestAppDeploymentNeverDeployedReturns404(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "never-deployed")

	rec := e.do(t, http.MethodGet, "/v1/apps/never-deployed/deployments/latest", nil, nil)
	assertProblem(t, rec, http.StatusNotFound, api.CodeNotFound)
}

func TestGetLatestAppDeploymentForeignAppReturns404(t *testing.T) {
	e := setup(t, api.PlanPro)
	foreign, err := e.store.CreateAccount(context.Background(), "foreign-latest@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	mustSeedAppFor(t, e.store, foreign.ID, "foreign-latest")

	rec := e.do(t, http.MethodGet, "/v1/apps/foreign-latest/deployments/latest", nil, nil)
	assertProblem(t, rec, http.StatusNotFound, api.CodeNotFound)
}
