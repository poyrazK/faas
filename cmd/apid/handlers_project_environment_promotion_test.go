package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectEnvironmentPromotionPreviewComparesLiveReleasesAndConfig(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	fromValues, fromHash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"region":"eu"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{
		AccountID: acct.ID, ProjectID: project.ID, EnvironmentSlug: "staging",
		ConfigHash: fromHash, Values: fromValues,
	}); err != nil {
		t.Fatal(err)
	}
	toValues, toHash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"region":"us"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{
		AccountID: acct.ID, ProjectID: project.ID, EnvironmentSlug: "production",
		ConfigHash: toHash, Values: toValues,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Scope: "staging", SourceSHA256: "source-staging", Status: state.DeployLive,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Scope: "production", SourceSHA256: "source-production", Status: state.DeployLive,
	}); err != nil {
		t.Fatal(err)
	}

	req, rec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/promotion-preview?from=staging", "shop", nil)
	req.SetPathValue("environment", "production")
	srv.previewProjectEnvironmentPromotion(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", rec.Code, rec.Body.String())
	}
	var preview api.ProjectEnvironmentPromotionPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.FromEnvironment != "staging" || preview.ToEnvironment != "production" {
		t.Fatalf("environments=%+v", preview)
	}
	if !preview.ToEnvironmentProtected || !preview.ApprovalRequired || !preview.CanPromote {
		t.Fatalf("protection/eligibility=%+v", preview)
	}
	if len(preview.ConfigDiff.Changes) != 1 || preview.ConfigDiff.Changes[0].Key != "region" {
		t.Fatalf("config diff=%+v", preview.ConfigDiff)
	}
	if len(preview.Changes) != 1 || preview.Changes[0].Kind != "update" || preview.Changes[0].SourceRevision != "source-staging" {
		t.Fatalf("release changes=%+v", preview.Changes)
	}
	if len(preview.PromotionHash) != 64 || preview.PromotionToken == "" {
		t.Fatalf("promotion identity=%+v", preview)
	}
}

func TestProjectEnvironmentPromotionPreviewBlocksMissingSourceRelease(t *testing.T) {
	srv, store, acct, project, _ := newProjectLifecycleFixture(t)
	if _, err := store.CreateProjectEnvironment(context.Background(), state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	req, rec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/promotion-preview?from=staging", "shop", nil)
	req.SetPathValue("environment", "production")
	srv.previewProjectEnvironmentPromotion(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", rec.Code, rec.Body.String())
	}
	var preview api.ProjectEnvironmentPromotionPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.CanPromote || len(preview.BlockingReasons) != 1 || preview.Changes[0].Kind != "source_missing" {
		t.Fatalf("blocked preview=%+v", preview)
	}
}
