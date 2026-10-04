package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCreateProjectEnvironmentQualificationAndGatePreview(t *testing.T) {
	testCreateProjectEnvironmentQualificationAndGatePreview(t, false)
}

// adr: 566
func TestCreateProjectEnvironmentQualificationPinsFlagsAndGatePreview(t *testing.T) {
	testCreateProjectEnvironmentQualificationAndGatePreview(t, true)
}

func testCreateProjectEnvironmentQualificationAndGatePreview(t *testing.T, withFlags bool) {
	t.Helper()
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	app, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.RAMMB = 512
	spec, err := store.PutProjectEnvironmentWorkloadSpec(ctx, acct.ID, project.ID, "staging", app.ID, 0, settings)
	if err != nil {
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
	var flagVersion state.FeatureFlagVersion
	var flagScope state.FeatureFlagScope
	if withFlags {
		env, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "staging")
		if err != nil {
			t.Fatal(err)
		}
		flagScope = state.FeatureFlagScope{AccountID: acct.ID, ProjectID: project.ID, EnvironmentID: env.ID}
		flagVersion, err = store.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: flagScope,
			Config: flags.Config{Flags: []flags.Flag{{Key: "checkout", Enabled: true, Default: true}}}, Actor: "developer"})
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot, problem := srv.loadProjectEnvironmentState(ctx, acct, "shop", "staging")
	if problem != nil || len(snapshot.Workloads) != 1 || snapshot.Workloads[0].WorkloadConfigHash != spec.Hash {
		t.Fatalf("state changed deployment settings identity: %v", problem)
	}
	if withFlags && !api.ValidProjectEnvironmentConfigHash(snapshot.FeatureFlagsHash) {
		t.Fatal("state omitted observed flag identity")
	}
	qualifiedHash, err := api.QualificationWorkloadConfigHash(spec.Hash, snapshot.FeatureFlagsHash)
	if err != nil {
		t.Fatal(err)
	}
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
		WorkloadConfigHashes: map[string]string{app.Slug: qualifiedHash},
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
	if withFlags {
		if _, err := store.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: flagScope, ExpectedVersion: flagVersion.Version, Config: flagVersion.Config, Actor: "developer"}); err != nil {
			t.Fatal(err)
		}
		changed, problem := srv.buildProjectEnvironmentPromotionPlan(ctx, acct, "shop", "staging", "production", false)
		if problem != nil || changed.Preview.CanPromote || !strings.Contains(strings.Join(changed.Preview.BlockingReasons, ";"), "feature flags") {
			t.Fatalf("flag-only publish retained qualification: %v", problem)
		}
	}
	settings.RAMMB = 1024
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, acct.ID, project.ID, "staging", app.ID, spec.Revision, settings); err != nil {
		t.Fatal(err)
	}
	changedReq, changedRec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/promotion-preview?from=staging", "shop", nil)
	changedReq.SetPathValue("environment", "production")
	srv.previewProjectEnvironmentPromotion(changedRec, changedReq, acct)
	var changed api.ProjectEnvironmentPromotionPreviewResponse
	if err := json.Unmarshal(changedRec.Body.Bytes(), &changed); err != nil {
		t.Fatal(err)
	}
	if changedRec.Code != http.StatusOK || changed.CanPromote || !strings.Contains(strings.Join(changed.BlockingReasons, ";"), "workload settings changed") {
		t.Fatalf("workload edit did not invalidate receipt: status=%d preview=%+v", changedRec.Code, changed)
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
