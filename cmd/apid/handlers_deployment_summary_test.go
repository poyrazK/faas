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

func TestGetAppDeploymentSummaryIncludesDiffAndRollbackTarget(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "summary-app")
	base := time.Now().UTC().Add(-time.Minute)
	previous, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID: appID, ImageDigest: "sha256:previous", CommitSHA: "1111111", SourceURL: "https://github.com/acme/app",
		Kind: state.DeploymentKindImage, Status: state.DeployPending, CreatedAt: base, Scope: "production", TrafficPercent: 100,
	})
	if err != nil {
		t.Fatalf("create previous: %v", err)
	}
	if err := e.store.MarkDeploymentLive(context.Background(), previous.ID); err != nil {
		t.Fatalf("promote previous: %v", err)
	}
	current, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID: appID, ImageDigest: "sha256:current", CommitSHA: "2222222", SourceURL: "https://github.com/acme/app",
		Kind: state.DeploymentKindImage, Status: state.DeployPending, CreatedAt: base.Add(time.Second), Scope: "production", TrafficPercent: 50,
	})
	if err != nil {
		t.Fatalf("create current: %v", err)
	}
	if err := e.store.MarkDeploymentLive(context.Background(), current.ID); err != nil {
		t.Fatalf("promote current: %v", err)
	}

	rec := e.do(t, http.MethodGet, "/v1/apps/summary-app/deployments/"+current.ID+"/summary", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got api.DeploymentSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if got.Deployment.ID != current.ID || got.Deployment.CommitSHA != "2222222" {
		t.Fatalf("deployment = %+v", got.Deployment)
	}
	if got.Previous == nil || got.Previous.ID != previous.ID {
		t.Fatalf("previous = %+v, want %s", got.Previous, previous.ID)
	}
	if got.RollbackTargetID != previous.ID {
		t.Fatalf("rollback_target_id = %q, want %q", got.RollbackTargetID, previous.ID)
	}
	changed := make(map[string]api.DeploymentChange, len(got.Changes))
	for _, change := range got.Changes {
		changed[change.Field] = change
	}
	for _, field := range []string{"status", "image_digest", "commit_sha", "traffic_percent"} {
		if _, ok := changed[field]; !ok {
			t.Errorf("missing change %q in %+v", field, got.Changes)
		}
	}
}

func TestGetAppDeploymentSummaryInitialRelease(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "summary-initial")
	deployment, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID: appID, ImageDigest: "sha256:first", Kind: state.DeploymentKindImage, Status: state.DeployLive,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodGet, "/v1/apps/summary-initial/deployments/"+deployment.ID+"/summary", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got api.DeploymentSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Previous != nil || got.RollbackTargetID != "" || got.Changes == nil || len(got.Changes) != 0 {
		t.Fatalf("initial summary = %+v, want no predecessor, rollback, or changes", got)
	}
}

func TestGetAppDeploymentSummaryRejectsMismatchedDeployment(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "summary-owned")
	deployment, err := e.store.CreateDeployment(context.Background(), state.Deployment{AppID: appID, ImageDigest: "sha256:x"})
	if err != nil {
		t.Fatal(err)
	}
	mustSeedApp(t, e, "summary-other")
	rec := e.do(t, http.MethodGet, "/v1/apps/summary-other/deployments/"+deployment.ID+"/summary", nil, nil)
	assertProblem(t, rec, http.StatusNotFound, api.CodeNotFound)
}
