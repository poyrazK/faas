package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCreateProjectEnvironmentQualificationAndGatePreview(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", ImageDigest: "sha256:qualified", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	release, err := store.PublishProjectReleaseSet(ctx, acct.ID, project.ID, "staging", 1800,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: deployment.ID}})
	if err != nil {
		t.Fatal(err)
	}
	httpStatus := http.StatusNoContent
	probeResult := api.ProjectEnvironmentQualificationResult{
		WorkloadSlug: app.Slug, DeploymentID: deployment.ID, Status: "passed", HTTPStatus: &httpStatus,
	}
	emptySecretHash, err := api.ProjectEnvironmentSecretRevisionHash(nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(api.CreateProjectEnvironmentQualificationRequest{
		ReleaseSetID: release.ID, ConfigurationVersion: 0,
		ConfigurationHash:    api.EmptyProjectEnvironmentConfigHash(),
		SecretRevisionHashes: map[string]string{app.Slug: emptySecretHash},
		Checks: []api.ProjectEnvironmentQualificationCheck{
			{Name: "smoke", Status: "passed", Results: []api.ProjectEnvironmentQualificationResult{probeResult}},
			{Name: "health", Status: "passed", Results: []api.ProjectEnvironmentQualificationResult{probeResult}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/staging/qualifications", "shop", body)
	req.SetPathValue("environment", "staging")
	srv.createProjectEnvironmentQualification(rec, req, acct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("qualification status=%d body=%s", rec.Code, rec.Body.String())
	}
	var qualification api.ProjectEnvironmentQualificationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &qualification); err != nil {
		t.Fatal(err)
	}
	if qualification.Status != "passed" || qualification.ReleaseSetID != release.ID || qualification.ConfigurationVersion != 0 ||
		qualification.ConfigurationHash != api.EmptyProjectEnvironmentConfigHash() || len(qualification.Checks) != 2 ||
		qualification.ExpiresAt.Sub(qualification.CreatedAt) != state.ProjectEnvironmentQualificationTTL {
		t.Fatalf("qualification response=%+v", qualification)
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
	if !preview.CanPromote || !preview.QualificationRequired || preview.Qualification == nil || preview.Qualification.ID != qualification.ID {
		t.Fatalf("promotion preview=%+v", preview)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, acct.ID, app.ID, "staging", "TOKEN", "age1test", "value-hash-not-bound", []byte("sealed-token")); err != nil {
		t.Fatal(err)
	}
	stalePreviewReq, stalePreviewRec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/promotion-preview?from=staging", "shop", nil)
	stalePreviewReq.SetPathValue("environment", "production")
	srv.previewProjectEnvironmentPromotion(stalePreviewRec, stalePreviewReq, acct)
	if stalePreviewRec.Code != http.StatusOK {
		t.Fatalf("stale secret preview status=%d body=%s", stalePreviewRec.Code, stalePreviewRec.Body.String())
	}
	var stalePreview api.ProjectEnvironmentPromotionPreviewResponse
	if err := json.Unmarshal(stalePreviewRec.Body.Bytes(), &stalePreview); err != nil {
		t.Fatal(err)
	}
	if stalePreview.CanPromote || !strings.Contains(strings.Join(stalePreview.BlockingReasons, ";"), "secret revisions changed") {
		t.Fatalf("secret rotation did not stale qualification: %+v", stalePreview)
	}
	staleSecretReq, staleSecretRec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/staging/qualifications", "shop", body)
	staleSecretReq.SetPathValue("environment", "staging")
	srv.createProjectEnvironmentQualification(staleSecretRec, staleSecretReq, acct)
	if staleSecretRec.Code != http.StatusConflict || !strings.Contains(staleSecretRec.Body.String(), "secret revisions changed") {
		t.Fatalf("stale secret qualification status=%d body=%s", staleSecretRec.Code, staleSecretRec.Body.String())
	}
	createProjectEnvironmentConfigFixture(t, store, acct.ID, project.ID, "staging", `{"region":"eu"}`)
	staleReq, staleRec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/staging/qualifications", "shop", body)
	staleReq.SetPathValue("environment", "staging")
	srv.createProjectEnvironmentQualification(staleRec, staleReq, acct)
	if staleRec.Code != http.StatusConflict {
		t.Fatalf("stale configuration qualification status=%d body=%s", staleRec.Code, staleRec.Body.String())
	}
}

func TestCreateProjectEnvironmentQualificationRejectsOpenSchema(t *testing.T) {
	srv, _, acct, _, _ := newProjectLifecycleFixture(t)
	req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/staging/qualifications", "shop",
		[]byte(`{"release_set_id":"00000000-0000-4000-8000-000000000001","checks":[],"logs":"should not persist"}`))
	req.SetPathValue("environment", "staging")
	srv.createProjectEnvironmentQualification(rec, req, acct)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", rec.Code, rec.Body.String())
	}
}
