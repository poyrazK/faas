package githubd

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

type recordedNeutralCheck struct {
	sha, title, summary string
}

// pullRequestBody is a GitHub-shaped delivery with the PR author and draft
// flag the preview policy reads.
func pullRequestBody(action string, prNumber int, headSHA, authorType string, draft bool) []byte {
	draftJSON := "false"
	if draft {
		draftJSON = "true"
	}
	prState := "open"
	if action == string(PullRequestActionClosed) {
		prState = "closed"
	}
	return []byte(`{
		"action": "` + action + `",
		"number": ` + itoa(prNumber) + `,
		"pull_request": {
			"state": "` + prState + `",
			"draft": ` + draftJSON + `,
			"user": {"login": "dependabot[bot]", "type": "` + authorType + `"},
			"head": {"ref": "feature/x", "sha": "` + headSHA + `", "repo": {"full_name": "octo/api"}}
		},
		"repository": {"full_name": "octo/api", "name": "api"},
		"installation": {"id": 42},
		"sender": {"login": "alice"}
	}`)
}

func recordNeutralChecks(svc *Service) *[]recordedNeutralCheck {
	checks := &[]recordedNeutralCheck{}
	svc.WritePreviewCheckNeutralForInstallation = func(_ context.Context, _ int64, _, sha, title, summary string) error {
		*checks = append(*checks, recordedNeutralCheck{sha: sha, title: title, summary: summary})
		return nil
	}
	return checks
}

// Dependabot and Renovate pull requests used up the preview allowance (one
// environment on Free, two on Hobby). They are skipped unless the project
// previews bot pull requests, with a neutral check that says why.
func TestHandlePullRequest_BotPullRequestFollowsPolicy(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	svc, _ := newPreviewService(t, rig)
	neutral := recordNeutralChecks(svc)
	head := strings.Repeat("a", 40)

	result, err := svc.handlePullRequest(ctx, pullRequestBody("opened", 42, head, "Bot", false))
	if !IsIgnored(err) || !result.WasIgnored {
		t.Fatalf("bot PR = (%+v, %v), want skipped", result, err)
	}
	if _, err := rig.mem.AppBySlug(ctx, "pr-42-demo-app"); err == nil {
		t.Fatal("a bot PR reserved a preview")
	}
	if len(*neutral) != 1 || (*neutral)[0].title != "Preview skipped" || !strings.Contains((*neutral)[0].summary, "--preview-bots") {
		t.Fatalf("neutral checks = %+v, want one explaining the skip", *neutral)
	}

	policy := state.DefaultGitHubDeployPolicy(rig.parentProjectID, rig.acct)
	policy.PreviewBotPRs = true
	if _, err := rig.mem.UpsertGitHubDeployPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	result, err = svc.handlePullRequest(ctx, pullRequestBody("opened", 42, head, "Bot", false))
	if err != nil || len(result.Added) != 1 {
		t.Fatalf("bot PR with bot previews on = (%+v, %v), want a preview", result, err)
	}
}

// With draft previews off, a draft is skipped and gets its preview when it
// is marked ready for review.
func TestHandlePullRequest_DraftPreviewedWhenReady(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	svc, _ := newPreviewService(t, rig)
	neutral := recordNeutralChecks(svc)
	policy := state.DefaultGitHubDeployPolicy(rig.parentProjectID, rig.acct)
	policy.PreviewDraftPRs = false
	if _, err := rig.mem.UpsertGitHubDeployPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("a", 40)

	result, err := svc.handlePullRequest(ctx, pullRequestBody("opened", 42, head, "User", true))
	if !IsIgnored(err) || len(*neutral) != 1 || !strings.Contains((*neutral)[0].summary, "draft") {
		t.Fatalf("draft PR = (%+v, %v), neutral %+v; want skipped and explained", result, err, *neutral)
	}
	result, err = svc.handlePullRequest(ctx, pullRequestBody("ready_for_review", 42, head, "User", false))
	if err != nil || len(result.Added) != 1 || result.Added[0].Slug != "pr-42-demo-app" {
		t.Fatalf("ready for review = (%+v, %v), want the preview", result, err)
	}
}

// A PR reopened within its close grace at the head its preview still serves
// is reopened in place: no rebuild, and the check says it is live.
func TestHandlePullRequest_ReopenReusesLivePreview(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	svc, rec := newSourcePreviewService(t, rig, []reposcan.Workload{{Name: "api"}})
	enqueuer := svc.Enqueuer.(*previewDependencyEnqueuer)
	head := strings.Repeat("a", 40)
	opened, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, head))
	if err != nil || len(opened.Added) != 1 {
		t.Fatalf("open = (%+v, %v)", opened, err)
	}
	if _, err := rig.mem.CreateDeployment(ctx, state.Deployment{AppID: opened.Added[0].ID, Kind: state.DeploymentKindPreview,
		CommitSHA: head, Status: state.DeployLive}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.handlePullRequest(ctx, pullRequestClosedBody(42, head)); err != nil {
		t.Fatalf("close: %v", err)
	}
	builds := len(enqueuer.specs)

	if _, err := svc.handlePullRequest(ctx, pullRequestReopenedBody(42, head)); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(enqueuer.specs) != builds {
		t.Fatalf("reopen queued %d builds, want none for the live head", len(enqueuer.specs)-builds)
	}
	preview, err := rig.mem.AppByID(ctx, opened.Added[0].ID)
	if err != nil || preview.PreviewPrState != state.PreviewPrStateOpen {
		t.Fatalf("preview after reopen = (%+v, %v), want open", preview, err)
	}
	set, err := rig.mem.GetPRPreviewSet(ctx, rig.install, "octo/api", 42)
	if err != nil || set.Closed {
		t.Fatalf("set after reopen = (%+v, %v), want open", set, err)
	}
	check := rec.checks[len(rec.checks)-1]
	if check.phase != githubdgrpc.CheckPhaseLive || !strings.Contains(check.summary, "Preview reopened") {
		t.Fatalf("check = %+v, want a live reopened check", check)
	}

	// A new head after reopening builds as usual.
	if _, err := svc.handlePullRequest(ctx, pullRequestSyncBody(42, strings.Repeat("b", 40))); err != nil || len(enqueuer.specs) != builds+1 {
		t.Fatalf("push after reopen = %v, builds %d→%d; want a build", err, builds, len(enqueuer.specs))
	}
}

// A preview runs with GREGALE_PREVIEW, its PR number, branch and commit, so
// app code can tell it from production.
func TestHandlePullRequest_PreviewKnowsItIsAPreview(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	svc, _ := newSourcePreviewService(t, rig, []reposcan.Workload{{Name: "api"}})
	head := strings.Repeat("a", 40)
	opened, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, head))
	if err != nil || len(opened.Added) != 1 {
		t.Fatalf("open = (%+v, %v)", opened, err)
	}
	preview, err := rig.mem.AppByID(ctx, opened.Added[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"GREGALE_PREVIEW": "1", "GREGALE_PR_NUMBER": "42", "GREGALE_GIT_BRANCH": "feature/x", "GREGALE_GIT_COMMIT": head,
	} {
		if got := preview.Manifest.Env[key]; got != want {
			t.Fatalf("%s = %q, want %q (env %v)", key, got, want, preview.Manifest.Env)
		}
	}
}
