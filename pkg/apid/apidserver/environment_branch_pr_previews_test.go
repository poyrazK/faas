package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ADR-20261113121342 branch-backed PR previews: apid makes a pull request's
// branch for githubd when the project chose them and the platform allows
// them, refreshes and closes only the pull request's own branch, and answers
// a fallback reason whenever the pull request should get an app preview.
func TestEnvironmentBranchPRPreviews(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv, store, acct := *e.S(), *e.Store(), *e.Acct()
	ctx := t.Context()
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop", ProductionBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-web", WorkloadName: "web", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:web"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, parent.ID, "/web.ext4", "layers/web.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(48 * time.Hour)
	ensure := func(t *testing.T, pullRequest int, at time.Time) *githubdpb.EnsurePullRequestBranchResponse {
		t.Helper()
		out, err := srv.EnsurePullRequestBranch(ctx, acct.ID, project.ID, pullRequest, at)
		if err != nil {
			t.Fatalf("ensure #%d: %v", pullRequest, err)
		}
		return out
	}
	fallback := func(t *testing.T, pullRequest int, want string) {
		t.Helper()
		if out := ensure(t, pullRequest, expires); out.GetReady() || !strings.Contains(out.GetFallbackReason(), want) {
			t.Fatalf("ensure #%d = %+v; want a fallback naming %q", pullRequest, out, want)
		}
	}
	patchMode := func(t *testing.T, mode string, want int) {
		t.Helper()
		rec := e.Do(t, http.MethodPatch, "/v1/apps/shop-web/github/deployment-policy", api.GitHubDeploymentPolicyPatch{PreviewMode: &mode}, nil)
		if rec.Code != want {
			t.Fatalf("PATCH preview_mode=%s = %d %s; want %d", mode, rec.Code, rec.Body, want)
		}
	}

	// Off on the platform: no branch, and a project cannot choose them.
	fallback(t, 7, "not enabled")
	patchMode(t, "branch", http.StatusConflict)
	for _, key := range []string{apidserver.RuntimeConfigEnvironmentBranches, apidserver.RuntimeConfigEnvironmentBranchPRPreviews} {
		if err := (*srv.RuntimeConfig()).Apply(key, json.RawMessage("true")); err != nil {
			t.Fatal(err)
		}
	}
	fallback(t, 7, "previews pull requests as apps")
	patchMode(t, "branch", http.StatusOK)

	made := ensure(t, 7, expires)
	slot := state.EnvironmentBranchSlotID(project.ID, "pr-7")
	if !made.GetReady() || !made.GetCreated() || made.GetEnvironment() != "pr-7" || len(made.GetWorkloads()) != 1 ||
		made.GetWorkloads()[0].GetAppId() != app.ID || made.GetWorkloads()[0].GetUrl() != apidserver.ProjectEnvironmentWorkloadURL(slot, app.ID) {
		t.Fatalf("ensure #7 = %+v; want branch pr-7 made with its stable workload URL", made)
	}
	branch, err := store.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-7")
	if err != nil || branch.PullRequestNumber != 7 || branch.ParentSlug != "production" {
		t.Fatalf("branch pr-7 = %+v, %v; want a branch of production for pull request 7", branch, err)
	}

	// A push refreshes the same branch's expiry.
	again := ensure(t, 7, expires.Add(24*time.Hour))
	refreshed, err := store.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-7")
	if !again.GetReady() || again.GetCreated() || err != nil || refreshed.EnvironmentID != branch.EnvironmentID || !refreshed.ExpiresAt.After(branch.ExpiresAt) {
		t.Fatalf("second ensure = %+v, branch %+v (%v); want the same branch with a later expiry than %s", again, refreshed, err, branch.ExpiresAt)
	}

	// A branch the user made under the pull request's name is not adopted.
	if _, _, _, err := store.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
		AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: "pr-8",
		ExpiresAt: expires, EgressMode: state.EnvironmentBranchEgressLive, MaxDepth: api.EnvironmentBranchMaxDepth,
	}, api.MustLimitsFor(acct.Plan)); err != nil {
		t.Fatal(err)
	}
	fallback(t, 8, "not made for this pull request")

	// Past the plan's branch limit the pull request falls back, naming it.
	for _, pullRequest := range []int{9, 10, 11} {
		if out := ensure(t, pullRequest, expires); !out.GetReady() {
			t.Fatalf("ensure #%d = %+v; want a branch", pullRequest, out)
		}
	}
	fallback(t, 12, "limit 5, observed 5")

	// Builds go only into the pull request's own branch.
	for _, tc := range []struct {
		scope       string
		pullRequest int
		ok          bool
	}{{"pr-7", 7, true}, {"pr-8", 8, false}, {"pr-7", 9, false}, {"pr-404", 404, false}} {
		err := srv.PullRequestBranchBuild(ctx, app, tc.scope, tc.pullRequest)
		if (err == nil) != tc.ok || (err != nil && status.Code(err) != codes.FailedPrecondition) {
			t.Fatalf("build into %s for #%d: %v; want ok=%v", tc.scope, tc.pullRequest, err, tc.ok)
		}
	}

	// Closing starts the grace; a pull request without a branch closes nothing.
	grace := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	if closed, err := srv.ClosePullRequestBranch(ctx, acct.ID, project.ID, 7, grace); err != nil || !closed {
		t.Fatalf("close #7 = %v, %v; want closed", closed, err)
	}
	if closing, err := store.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-7"); err != nil || closing.ExpiresAt.After(grace) {
		t.Fatalf("closed branch = %+v, %v; want it to expire by %s", closing, err, grace)
	}
	for _, pullRequest := range []int{8, 404} {
		if closed, err := srv.ClosePullRequestBranch(ctx, acct.ID, project.ID, pullRequest, grace); err != nil || closed {
			t.Fatalf("close #%d = %v, %v; want nothing closed", pullRequest, closed, err)
		}
	}
	if kept, err := store.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-8"); err != nil || !kept.ExpiresAt.After(grace) {
		t.Fatalf("hand-made branch after close = %+v, %v; want its own expiry", kept, err)
	}
}

// An apid without branch previews wired answers their RPCs Unimplemented,
// which githubd treats as a fallback, and refuses a malformed request.
func TestEnvironmentBranchPRPreviewsBridge(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv := *e.S()
	ctx := context.Background()
	request := &githubdpb.EnsurePullRequestBranchRequest{AccountId: "a", ProjectId: "p", PullRequestNumber: 1, ExpiresAtUnix: time.Now().Unix()}
	if _, err := srv.PullRequestBranchBridge(false).EnsurePullRequestBranch(ctx, request); status.Code(err) != codes.Unimplemented {
		t.Fatalf("unwired ensure: %v; want Unimplemented", err)
	}
	if _, err := srv.PullRequestBranchBridge(false).ClosePullRequestBranch(ctx, &githubdpb.ClosePullRequestBranchRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("unwired close: %v; want Unimplemented", err)
	}
	if _, err := srv.PullRequestBranchBridge(true).EnsurePullRequestBranch(ctx, &githubdpb.EnsurePullRequestBranchRequest{AccountId: "a"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("malformed ensure: %v; want InvalidArgument", err)
	}
	if _, err := srv.PullRequestBranchBridge(true).EnsurePullRequestBranch(ctx, request); status.Code(err) != codes.NotFound {
		t.Fatalf("ensure for an unknown project: %v; want NotFound", err)
	}
}
