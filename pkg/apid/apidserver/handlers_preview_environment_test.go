package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGetPreviewEnvironmentStatus_CurrentHeadAndScope(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	production := make(map[string]state.App)
	for _, slug := range []string{"api", "worker"} {
		app, err := (*e.Store()).CreateAppIfUnderQuota(ctx, state.App{
			AccountID: (*e.Acct()).ID, Slug: slug, Type: "stateless", Runtime: "node22",
			RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30, Status: state.AppActive,
		}, api.Limits{DeployedApps: 10000})
		if err != nil {
			t.Fatalf("create production app %q: %v", slug, err)
		}
		production[slug] = app
	}
	root := apidserver.SeedPreviewAppForTest(t, e, "pr-42-api", "api", 42)
	worker := apidserver.SeedPreviewAppForTest(t, e, "pr-42-worker", "worker", 42)
	sha := strings.Repeat("a", 40)
	set := state.PRPreviewSet{InstallationID: 77, RepoFullName: "octo/api", PRNumber: 42,
		CommitSHA: sha, RootAppID: root.ID, MemberAppIDs: []string{root.ID, worker.ID}}
	if err := (*e.Store()).PutPRPreviewSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	create := func(appID, commit string, status state.DeploymentStatus, at time.Time) {
		t.Helper()
		if _, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindPreview,
			ImageDigest: "sha256:" + strings.Repeat("b", 64), CommitSHA: commit, Status: status, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	for _, slug := range []string{"api", "worker"} {
		if _, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{AppID: production[slug].ID,
			Kind: state.DeploymentKindGitHub, ImageDigest: "sha256:" + strings.Repeat("c", 64),
			CommitSHA: strings.Repeat("d", 40), Status: state.DeployLive, CreatedAt: now}); err != nil {
			t.Fatalf("create production deployment for %q: %v", slug, err)
		}
	}
	create(root.ID, sha, state.DeployLive, now)
	create(worker.ID, sha, state.DeployBuilding, now)
	read := func() api.PreviewEnvironmentStatusResponse {
		t.Helper()
		rec := e.Do(t, http.MethodGet, "/v1/preview/pr-42-api/environment", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		var got api.PreviewEnvironmentStatusResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := read()
	if got.Ready || got.Phase != "building" || got.LiveWorkloads != 1 || got.TotalWorkloads != 2 ||
		len(got.Members) != 2 || got.Members[1].DeploymentStatus != "building" || got.CommitSHA != sha {
		t.Fatalf("partial environment = %+v", got)
	}
	rootResource := got.Members[0]
	if rootResource.ExpiresAt == nil || rootResource.Links == nil || rootResource.Links.URL == "" ||
		rootResource.Links.Logs != "/v1/apps/pr-42-api/logs" || rootResource.Changes == nil ||
		!rootResource.Changes.ArtifactChanged || rootResource.Changes.PreviewArtifact.CommitSHA != sha ||
		rootResource.Changes.ProductionArtifact.CommitSHA != strings.Repeat("d", 40) {
		t.Fatalf("root preview resources = %+v, want current-head diff and diagnostic links", rootResource)
	}
	if !strings.Contains(strings.Join(rootResource.Changes.ConfigurationChangedGroups, ","), "runtime") {
		t.Fatalf("root config change groups = %v, want runtime", rootResource.Changes.ConfigurationChangedGroups)
	}
	create(worker.ID, sha, state.DeployFailed, now.Add(time.Second))
	got = read()
	if got.Ready || got.Phase != "failed" || !strings.Contains(got.Summary, "worker") {
		t.Fatalf("failed sibling environment = %+v", got)
	}
	// A newer deployment for the old head must not satisfy the new head.
	set.CommitSHA = strings.Repeat("c", 40)
	if err := (*e.Store()).PutPRPreviewSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	got = read()
	if got.Ready || got.LiveWorkloads != 0 || got.Members[0].DeploymentStatus != "missing" {
		t.Fatalf("new head inherited old readiness: %+v", got)
	}
	if got.Members[0].Changes == nil || got.Members[0].Changes.PreviewArtifact.DeploymentID != "" {
		t.Fatalf("new head inherited an old preview artifact: %+v", got.Members[0].Changes)
	}
	create(root.ID, set.CommitSHA, state.DeployLive, now.Add(time.Second))
	create(worker.ID, set.CommitSHA, state.DeployLive, now.Add(time.Second))
	got = read()
	if !got.Ready || got.Phase != "live" || got.LiveWorkloads != 2 || got.Members[1].DeploymentID == "" {
		t.Fatalf("all-live environment = %+v", got)
	}
	if err := (*e.Store()).ClosePRPreviewSet(ctx, 77, "octo/api", 42); err != nil {
		t.Fatal(err)
	}
	got = read()
	if got.Ready || got.Phase != "closed" {
		t.Fatalf("closed environment = %+v", got)
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/preview/pr-42-worker/environment", nil, nil), http.StatusNotFound, "preview_environment_not_found")
	legacy := apidserver.SeedPreviewAppForTest(t, e, "pr-7-legacy", "legacy", 7)
	if legacy.ID == "" {
		t.Fatal("missing legacy preview id")
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/preview/pr-7-legacy/environment", nil, nil), http.StatusNotFound, "preview_environment_not_found")
	foreign, err := (*e.Store()).CreateAccount(ctx, "foreign-preview-environment@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	apidserver.MustSeedAppFor(t, (*e.Store()), foreign.ID, "foreign-preview-environment")
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/preview/foreign-preview-environment/environment", nil, nil), http.StatusNotFound, api.CodeNotFound)
}
