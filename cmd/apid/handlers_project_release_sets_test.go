package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPublishProjectReleaseSetRequiresCompleteLiveGraph(t *testing.T) {
	srv, store, acct, _, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", ImageDigest: "sha256:release"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	request := api.PublishProjectReleaseSetRequest{TTLSeconds: 1800, Deployments: map[string]string{app.Slug: dep.ID}}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/release-sets", "shop", body)
	req.SetPathValue("environment", "production")
	srv.publishProjectReleaseSet(rec, req, acct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish = %d %s", rec.Code, rec.Body.String())
	}
	var release state.ProjectReleaseSet
	if err := json.Unmarshal(rec.Body.Bytes(), &release); err != nil {
		t.Fatal(err)
	}
	if !release.Active || len(release.Members) != 1 || release.Members[0].DeploymentID != dep.ID {
		t.Fatalf("release = %+v", release)
	}
	dark, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", ImageDigest: "sha256:release-dark",
		TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dark.ID); err != nil {
		t.Fatal(err)
	}
	request.Deployments = map[string]string{app.Slug: dark.ID}
	body, _ = json.Marshal(request)
	req, rec = projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/release-sets", "shop", body)
	req.SetPathValue("environment", "production")
	srv.publishProjectReleaseSet(rec, req, acct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish dark deployment = %d %s", rec.Code, rec.Body.String())
	}
	request.Deployments = map[string]string{app.Slug: "not-a-deployment"}
	body, _ = json.Marshal(request)
	req, rec = projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/release-sets", "shop", body)
	req.SetPathValue("environment", "production")
	srv.publishProjectReleaseSet(rec, req, acct)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid deployment = %d %s", rec.Code, rec.Body.String())
	}
}
