package apidserver_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Turning previews off in the GitHub policy made githubd ignore the PRs'
// pushes while their open previews served the last commit to the end of
// their lease. The PATCH now retires the project's open PR previews.
func TestPatchGitHubDeployPolicy_PreviewsOffRetiresOpenPreviews(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	store, acct := *e.Store(), *e.Acct()
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "policy-off"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "policy-web", WorkloadName: "web",
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	preview, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "pr-5-policy-web", WorkloadName: "web",
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive, PreviewOfSlug: "policy-web",
		PreviewPrNumber: 5, PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}

	off := false
	rec := e.Do(t, "PATCH", "/v1/apps/policy-web/github/deployment-policy", api.GitHubDeploymentPolicyPatch{PreviewEnabled: &off}, nil)
	if rec.Code != 200 {
		t.Fatalf("PATCH policy status %d: %s", rec.Code, rec.Body)
	}
	got, err := store.AppByID(ctx, preview.ID)
	if err != nil || got.PreviewPrState != state.PreviewPrStateStale {
		t.Fatalf("preview after previews off = (%q, %v), want stale", got.PreviewPrState, err)
	}
}
