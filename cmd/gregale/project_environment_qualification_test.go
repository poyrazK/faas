package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestReadProjectEnvironmentQualificationProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qualification.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nworkloads:\n  api:\n    health_path: /healthz\n    smoke_path: /ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile, err := readProjectEnvironmentQualificationProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if profile.TimeoutSeconds != defaultQualificationProbeTimeout || profile.Workloads["api"].HealthPath != "/healthz" {
		t.Fatalf("profile = %+v", profile)
	}
	if err := os.WriteFile(path, []byte("version: 1\nworkloads:\n  api:\n    health_path: https://elsewhere.test/\n    smoke_path: /ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readProjectEnvironmentQualificationProfile(path); err == nil {
		t.Fatal("absolute health path was accepted")
	}
	if err := os.WriteFile(path, []byte("version: 1\nunknown: value\nworkloads: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readProjectEnvironmentQualificationProfile(path); err == nil {
		t.Fatal("unknown profile field was accepted")
	}
}

func TestRunProjectEnvironmentQualificationProbe(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusNoContent)
		case "/ready":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/redirect":
			http.Redirect(w, r, "/healthz", http.StatusTemporaryRedirect)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	passed := runProjectEnvironmentQualificationProbe(context.Background(), client, server.URL, "/healthz")
	if passed.Status != "passed" || passed.HTTPStatus == nil || *passed.HTTPStatus != http.StatusNoContent || passed.ErrorCode != "" {
		t.Fatalf("passed probe = %+v", passed)
	}
	failed := runProjectEnvironmentQualificationProbe(context.Background(), client, server.URL, "/ready")
	if failed.Status != "failed" || failed.HTTPStatus == nil || *failed.HTTPStatus != http.StatusServiceUnavailable || failed.ErrorCode != "unexpected_status" {
		t.Fatalf("failed probe = %+v", failed)
	}
	redirected := runProjectEnvironmentQualificationProbe(context.Background(), client, server.URL, "/redirect")
	if redirected.Status != "failed" || redirected.HTTPStatus == nil || *redirected.HTTPStatus != http.StatusTemporaryRedirect {
		t.Fatalf("redirect probe = %+v", redirected)
	}
	if got := runProjectEnvironmentQualificationProbe(context.Background(), client, "http://127.0.0.1", "/healthz"); got.ErrorCode != "preview_unavailable" {
		t.Fatalf("non-TLS preview probe = %+v", got)
	}
}

type qualificationFakeClient struct {
	snapshot          api.ProjectEnvironmentStateResponse
	previews          map[string]api.DeploymentPreviewURL
	request           api.CreateProjectEnvironmentQualificationRequest
	created           bool
	promotionPreview  api.ProjectEnvironmentPromotionPreviewResponse
	previewProject    string
	previewTarget     string
	previewSource     string
	previewSyncConfig bool
}

func (f *qualificationFakeClient) GetProjectEnvironmentState(context.Context, string, string) (api.ProjectEnvironmentStateResponse, error) {
	return f.snapshot, nil
}

func (f *qualificationFakeClient) GetDeploymentURL(_ context.Context, id string) (api.DeploymentPreviewURL, error) {
	return f.previews[id], nil
}

func (f *qualificationFakeClient) CreateProjectEnvironmentQualification(_ context.Context, _, _ string, request api.CreateProjectEnvironmentQualificationRequest) (api.ProjectEnvironmentQualificationResponse, error) {
	f.request = request
	f.created = true
	status := "passed"
	for _, check := range request.Checks {
		if check.Status != "passed" {
			status = "failed"
		}
	}
	return api.ProjectEnvironmentQualificationResponse{
		ID: "44444444-4444-4444-8444-444444444444", Environment: "staging",
		ReleaseSetID: request.ReleaseSetID, ConfigurationVersion: request.ConfigurationVersion,
		ConfigurationHash: request.ConfigurationHash, SecretRevisionHashes: request.SecretRevisionHashes,
		WorkloadConfigHashes: request.WorkloadConfigHashes,
		Status:               status, Checks: request.Checks,
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(24 * time.Hour),
	}, nil
}

func (f *qualificationFakeClient) GetProjectEnvironmentPromotionPreviewWithConfig(_ context.Context, project, target, source string, syncConfig bool) (api.ProjectEnvironmentPromotionPreviewResponse, error) {
	f.previewProject, f.previewTarget, f.previewSource, f.previewSyncConfig = project, target, source, syncConfig
	return f.promotionPreview, nil
}

func TestQualifyProjectEnvironmentRunsAgainstExactReleaseMembers(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && r.URL.Path != "/ready" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	profilePath := filepath.Join(t.TempDir(), "qualification.yaml")
	if err := os.WriteFile(profilePath, []byte("version: 1\nworkloads:\n  api:\n    health_path: /healthz\n    smoke_path: /ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const (
		appID        = "11111111-1111-4111-8111-111111111111"
		deploymentID = "22222222-2222-4222-8222-222222222222"
		releaseID    = "33333333-3333-4333-8333-333333333333"
	)
	client := &qualificationFakeClient{
		snapshot: api.ProjectEnvironmentStateResponse{
			ProjectSlug: "shop", Environment: "staging", ReleaseSetStatus: "active",
			Configuration: api.ProjectEnvironmentConfigResponse{
				ProjectSlug: "shop", Environment: "staging", Version: 3,
				ConfigHash: api.EmptyProjectEnvironmentConfigHash(),
			},
			ActiveReleaseSet: &api.ProjectReleaseSetResponse{ID: releaseID, Environment: "staging", Active: true,
				Members: []api.ProjectReleaseSetMemberResponse{{AppID: appID, DeploymentID: deploymentID}}},
			Workloads: []api.ProjectEnvironmentStateWorkloadResponse{{AppID: appID, WorkloadSlug: "api",
				Secrets: []api.ProjectEnvironmentSecretResponse{{Key: "STRIPE_KEY", ValueHash: "value-hash-must-not-be-used", Version: 4}},
				Release: api.ProjectEnvironmentReleaseWorkloadResponse{WorkloadSlug: "api", DeploymentID: deploymentID, Status: "live"}}},
		},
		previews: map[string]api.DeploymentPreviewURL{deploymentID: {DeploymentID: deploymentID, URL: server.URL, Alive: true}},
	}
	profile, err := readProjectEnvironmentQualificationProfile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	client.snapshot.Workloads[0].WorkloadConfigHash = api.EmptyProjectEnvironmentConfigHash()
	client.snapshot.Workloads[0].Release.WorkloadConfigHash = api.EmptyProjectEnvironmentConfigHash()
	got, err := qualifyProjectEnvironmentWithProfileAndHTTPClient(context.Background(), client, "shop", "staging", profile, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if !client.created || client.request.ReleaseSetID != releaseID || client.request.ConfigurationVersion != 3 ||
		client.request.ConfigurationHash != api.EmptyProjectEnvironmentConfigHash() || got.Status != "passed" || len(got.Checks) != 2 {
		t.Fatalf("request=%+v response=%+v", client.request, got)
	}
	if client.request.WorkloadConfigHashes["api"] != api.EmptyProjectEnvironmentConfigHash() || got.WorkloadConfigHashes["api"] != api.EmptyProjectEnvironmentConfigHash() {
		t.Fatalf("tested workload hash was not recorded: request=%v receipt=%v", client.request.WorkloadConfigHashes, got.WorkloadConfigHashes)
	}
	expectedSecretHash, err := api.ProjectEnvironmentSecretRevisionHash([]api.ProjectEnvironmentSecretRevision{{Key: "STRIPE_KEY", Version: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if client.request.SecretRevisionHashes["api"] != expectedSecretHash || got.SecretRevisionHashes["api"] != expectedSecretHash ||
		client.request.SecretRevisionHashes["api"] == "value-hash-must-not-be-used" {
		t.Fatalf("secret revision binding = request:%v response:%v", client.request.SecretRevisionHashes, got.SecretRevisionHashes)
	}
	for _, check := range client.request.Checks {
		if check.Status != "passed" || len(check.Results) != 1 || check.Results[0].DeploymentID != deploymentID || check.Results[0].HTTPStatus == nil || *check.Results[0].HTTPStatus != http.StatusNoContent {
			t.Fatalf("check = %+v", check)
		}
	}
	client.created = false
	// ADR-375: flags are observed before probes and included in the submitted
	// fingerprint without changing the deployment's settings hash.
	client.snapshot.FeatureFlagsHash = strings.Repeat("a", 64)
	qualifiedHash, err := api.QualificationWorkloadConfigHash(api.EmptyProjectEnvironmentConfigHash(), client.snapshot.FeatureFlagsHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := qualifyProjectEnvironmentWithProfileAndHTTPClient(context.Background(), client, "shop", "staging", profile, server.Client()); err != nil || client.request.WorkloadConfigHashes["api"] != qualifiedHash {
		t.Fatalf("observed flags omitted from qualification: %v", err)
	}
	client.created = false
	client.snapshot.FeatureFlagsHash = "invalid"
	if _, err := qualifyProjectEnvironmentWithProfileAndHTTPClient(context.Background(), client, "shop", "staging", profile, server.Client()); err == nil || client.created {
		t.Fatal("invalid observed flag identity accepted")
	}
	client.snapshot.FeatureFlagsHash = ""
	client.snapshot.Workloads[0].WorkloadConfigHash = strings.Repeat("1", 64)
	if _, err := qualifyProjectEnvironmentWithProfileAndHTTPClient(context.Background(), client, "shop", "staging", profile, server.Client()); err == nil || !strings.Contains(err.Error(), "untested desired settings") || client.created {
		t.Fatalf("untested desired settings accepted: created=%v err=%v", client.created, err)
	}
}

func TestRunProjectEnvironmentPreflightQualifiesSourceAndChecksPromotion(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	profilePath := filepath.Join(t.TempDir(), "qualification.yaml")
	if err := os.WriteFile(profilePath, []byte("version: 1\nworkloads:\n  api:\n    health_path: /healthz\n    smoke_path: /ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const (
		appID        = "11111111-1111-4111-8111-111111111111"
		deploymentID = "22222222-2222-4222-8222-222222222222"
		releaseID    = "33333333-3333-4333-8333-333333333333"
	)
	client := &qualificationFakeClient{
		snapshot: api.ProjectEnvironmentStateResponse{
			ProjectSlug: "shop", Environment: "staging", ReleaseSetStatus: "active",
			Configuration: api.ProjectEnvironmentConfigResponse{
				ProjectSlug: "shop", Environment: "staging", Version: 3,
				ConfigHash: api.EmptyProjectEnvironmentConfigHash(),
			},
			ActiveReleaseSet: &api.ProjectReleaseSetResponse{ID: releaseID, Environment: "staging", Active: true,
				Members: []api.ProjectReleaseSetMemberResponse{{AppID: appID, DeploymentID: deploymentID}}},
			Workloads: []api.ProjectEnvironmentStateWorkloadResponse{{AppID: appID, WorkloadSlug: "api",
				Secrets: []api.ProjectEnvironmentSecretResponse{{Key: "STRIPE_KEY", ValueHash: "must-not-be-serialized", Version: 4}},
				Release: api.ProjectEnvironmentReleaseWorkloadResponse{WorkloadSlug: "api", DeploymentID: deploymentID, Status: "live"}}},
		},
		previews: map[string]api.DeploymentPreviewURL{deploymentID: {DeploymentID: deploymentID, URL: server.URL, Alive: true}},
	}
	client.promotionPreview = api.ProjectEnvironmentPromotionPreviewResponse{
		CanPromote: true, QualificationRequired: true, ApprovalRequired: true, SyncConfig: true,
		Qualification: &api.ProjectEnvironmentQualificationResponse{ID: "44444444-4444-4444-8444-444444444444", Status: "passed"},
	}

	got, err := runProjectEnvironmentPreflight(context.Background(), client, "shop", "staging", "production", profilePath, true, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ready" || !got.PromotionPreview.CanPromote || !got.PromotionPreview.ApprovalRequired || !got.PromotionPreview.SyncConfig ||
		got.Qualification.Status != "passed" || got.Qualification.ReleaseSetID != releaseID {
		t.Fatalf("preflight = %+v", got)
	}
	if client.previewProject != "shop" || client.previewSource != "staging" || client.previewTarget != "production" || !client.previewSyncConfig {
		t.Fatalf("promotion preview args = project:%q source:%q target:%q sync_config:%t", client.previewProject, client.previewSource, client.previewTarget, client.previewSyncConfig)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "must-not-be-serialized") || strings.Contains(string(encoded), "secret_revision_hashes") {
		t.Fatalf("preflight response exposed secret metadata: %s", encoded)
	}

	client.promotionPreview.Qualification.ID = "55555555-5555-4555-8555-555555555555"
	got, err = runProjectEnvironmentPreflight(context.Background(), client, "shop", "staging", "production", profilePath, false, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "blocked" || !strings.Contains(strings.Join(got.BlockingReasons, " "), "did not resolve this passing qualification") {
		t.Fatalf("stale qualification was not blocked: %+v", got)
	}
	if code := projectEnvironmentPreflightExitCode(got); code != 1 {
		t.Fatalf("blocked preflight exit code = %d, want 1", code)
	}
}

func TestQualifyProjectEnvironmentRejectsReleaseStateDrift(t *testing.T) {
	profilePath := filepath.Join(t.TempDir(), "qualification.yaml")
	if err := os.WriteFile(profilePath, []byte("version: 1\nworkloads:\n  api:\n    health_path: /health\n    smoke_path: /ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &qualificationFakeClient{snapshot: api.ProjectEnvironmentStateResponse{
		ProjectSlug: "shop", Environment: "staging", ReleaseSetStatus: "active",
		Configuration: api.ProjectEnvironmentConfigResponse{
			ProjectSlug: "shop", Environment: "staging", Version: 0,
			ConfigHash: api.EmptyProjectEnvironmentConfigHash(),
		},
		ActiveReleaseSet: &api.ProjectReleaseSetResponse{ID: "33333333-3333-4333-8333-333333333333", Environment: "staging", Active: true,
			Members: []api.ProjectReleaseSetMemberResponse{{AppID: "11111111-1111-4111-8111-111111111111", DeploymentID: "22222222-2222-4222-8222-222222222222"}}},
		Workloads: []api.ProjectEnvironmentStateWorkloadResponse{{AppID: "11111111-1111-4111-8111-111111111111", WorkloadSlug: "api",
			Release: api.ProjectEnvironmentReleaseWorkloadResponse{WorkloadSlug: "api", DeploymentID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Status: "live"}}},
	}}
	if _, err := qualifyProjectEnvironmentWithProfile(context.Background(), client, "shop", "staging", profilePath); err == nil {
		t.Fatal("state whose workload deployment differs from active set was accepted")
	}
	if client.created {
		t.Fatal("qualification was recorded after release-state mismatch")
	}
}
