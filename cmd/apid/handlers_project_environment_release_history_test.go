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

func TestProjectEnvironmentReleasesReportsLiveAndUndeployedWorkloads(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	other, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-worker", WorkloadName: "worker", Status: state.AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Scope: "production", BuildID: "build-1", ImageDigest: "sha256:release",
		SourceURL: "github:onebox/shop", CommitSHA: "abc123", SourceSHA256: "source-1",
		Status: state.DeployPending, TrafficPercent: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}

	req, rec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/releases", "shop", nil)
	req.SetPathValue("environment", "production")
	srv.getProjectEnvironmentReleases(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response api.ProjectEnvironmentReleaseListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ProjectSlug != project.Slug || response.Environment != "production" || len(response.Workloads) != 2 {
		t.Fatalf("release inventory=%+v", response)
	}
	if response.Workloads[0].WorkloadSlug != app.Slug || response.Workloads[0].Status != "live" || response.Workloads[0].DeploymentID != deployment.ID {
		t.Fatalf("live workload=%+v", response.Workloads[0])
	}
	if response.Workloads[1].WorkloadSlug != other.Slug || response.Workloads[1].Status != "not_deployed" {
		t.Fatalf("undeployed workload=%+v", response.Workloads[1])
	}
}

func TestListProjectEnvironmentPromotionsPaginatesAndFilters(t *testing.T) {
	srv, store, acct, project, _ := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-time.Hour)
	for i, promotion := range []struct {
		from   string
		status string
	}{
		{from: "staging", status: "succeeded"},
		{from: "staging", status: "failed"},
		{from: "dev", status: "succeeded"},
		{from: "staging", status: "succeeded"},
	} {
		createdAt := base.Add(time.Duration(i) * time.Minute)
		if _, _, err := store.CreateProjectEnvironmentPromotion(ctx, state.ProjectEnvironmentPromotion{
			AccountID: acct.ID, ProjectID: project.ID, ProjectSlug: project.Slug,
			FromEnvironment: promotion.from, ToEnvironment: "production",
			PromotionHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			IdempotencyKey: "history-" + string(rune('a'+i)), Status: promotion.status,
			CreatedAt: createdAt, UpdatedAt: createdAt,
		}, nil); err != nil {
			t.Fatal(err)
		}
	}

	req, rec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/promotions?limit=2&from=staging", "shop", nil)
	req.SetPathValue("environment", "production")
	srv.listProjectEnvironmentPromotions(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("page 1 status=%d body=%s", rec.Code, rec.Body.String())
	}
	var first api.ProjectEnvironmentPromotionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.Items[0].FromEnvironment != "staging" || first.Items[1].FromEnvironment != "staging" || first.NextBefore == "" {
		t.Fatalf("page 1=%+v", first)
	}

	req, rec = projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/promotions?limit=2&from=staging&before="+first.NextBefore, "shop", nil)
	req.SetPathValue("environment", "production")
	srv.listProjectEnvironmentPromotions(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("page 2 status=%d body=%s", rec.Code, rec.Body.String())
	}
	var second api.ProjectEnvironmentPromotionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].FromEnvironment != "staging" || second.NextBefore != "" {
		t.Fatalf("page 2=%+v, want final filtered history item", second)
	}

	req, rec = projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/promotions?status=bad", "shop", nil)
	req.SetPathValue("environment", "production")
	srv.listProjectEnvironmentPromotions(rec, req, acct)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}
