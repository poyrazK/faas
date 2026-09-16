package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectEnvironmentPromotionRequiresApprovalAndPromotesArtifact(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Scope: "staging", SourceSHA256: "source-staging",
		Status: state.DeployPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, source.ID, "/rootfs/source", "apps/source.ext4", 42); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Scope: "production", SourceSHA256: "source-production",
		Status: state.DeployPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, target.ID, "/rootfs/target", "apps/target.ext4", 42); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, target.ID); err != nil {
		t.Fatal(err)
	}

	previewReq, previewRec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/promotion-preview?from=staging", "shop", nil)
	previewReq.SetPathValue("environment", "production")
	srv.previewProjectEnvironmentPromotion(previewRec, previewReq, acct)
	if previewRec.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", previewRec.Code, previewRec.Body.String())
	}
	var preview api.ProjectEnvironmentPromotionPreviewResponse
	if err := json.Unmarshal(previewRec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}

	requestBody, err := json.Marshal(api.PromoteProjectEnvironmentRequest{
		FromEnvironment: "staging", PromotionToken: preview.PromotionToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	executeReq, executeRec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promote", "shop", requestBody)
	executeReq.SetPathValue("environment", "production")
	srv.promoteProjectEnvironment(executeRec, executeReq, acct)
	if executeRec.Code != http.StatusConflict {
		t.Fatalf("missing approval status=%d body=%s", executeRec.Code, executeRec.Body.String())
	}

	approvalToken, _, problem := srv.issueProjectEnvironmentPromotionApproval(ctx, acct, project.Slug, "production", preview.PromotionToken)
	if problem != nil {
		t.Fatalf("issue promotion approval: %v", problem)
	}
	requestBody, err = json.Marshal(api.PromoteProjectEnvironmentRequest{
		FromEnvironment: "staging", PromotionToken: preview.PromotionToken, ApprovalToken: approvalToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	executeReq, executeRec = projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promote", "shop", requestBody)
	executeReq.SetPathValue("environment", "production")
	srv.promoteProjectEnvironment(executeRec, executeReq, acct)
	if executeRec.Code != http.StatusOK {
		t.Fatalf("promotion status=%d body=%s", executeRec.Code, executeRec.Body.String())
	}
	var response api.ProjectEnvironmentPromotionResponse
	if err := json.Unmarshal(executeRec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Workloads) != 1 || response.Workloads[0].Status != "promoted" {
		t.Fatalf("promotion response=%+v", response)
	}
	live, err := store.LiveDeploymentForScope(ctx, app.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	if live.ID == target.ID || live.SourceSHA256 != source.SourceSHA256 || live.RootfsKey != "apps/source.ext4" {
		t.Fatalf("promoted live deployment=%+v", live)
	}
}

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
