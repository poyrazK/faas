package githubd

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func setPreviewPolicy(t *testing.T, rig *previewTestRig, change func(*state.GitHubDeployPolicy)) {
	t.Helper()
	policy := state.DefaultGitHubDeployPolicy(rig.parentProjectID, rig.acct)
	change(&policy)
	if _, err := rig.mem.UpsertGitHubDeployPolicy(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
}

func assertPreviewRetired(t *testing.T, rig *previewTestRig, id string) {
	t.Helper()
	preview, err := rig.mem.AppByID(context.Background(), id)
	if err != nil || preview.PreviewPrState == state.PreviewPrStateOpen ||
		preview.PreviewExpiresAt == nil || preview.PreviewExpiresAt.After(time.Now()) {
		t.Fatalf("preview = (%+v, %v), want it retired with its lease ended", preview, err)
	}
}

// With draft previews off, a PR turned back into a draft is skipped from
// then on; its preview served the last ready head to the end of its lease
// while the PR's checks said the preview was skipped.
func TestHandlePullRequest_ConvertedToDraftRetiresPreview(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	svc, _ := newSourcePreviewService(t, rig, []reposcan.Workload{{Name: "api"}})
	neutral := recordNeutralChecks(svc)
	setPreviewPolicy(t, rig, func(p *state.GitHubDeployPolicy) { p.PreviewDraftPRs = false })
	head := strings.Repeat("a", 40)
	opened, err := svc.handlePullRequest(ctx, pullRequestBody("opened", 42, head, "User", false))
	if err != nil || len(opened.Added) != 1 {
		t.Fatalf("open = (%+v, %v)", opened, err)
	}

	result, err := svc.handlePullRequest(ctx, pullRequestBody("converted_to_draft", 42, head, "User", true))
	if !IsIgnored(err) || !result.WasIgnored {
		t.Fatalf("converted to draft = (%+v, %v), want ignored", result, err)
	}
	assertPreviewRetired(t, rig, opened.Added[0].ID)
	if len(*neutral) != 1 || (*neutral)[0].title != "Preview skipped" || !strings.Contains((*neutral)[0].summary, "torn down") {
		t.Fatalf("neutral checks = %+v, want one saying the earlier preview is torn down", *neutral)
	}
}

// When the project previews drafts, turning a PR into a draft changes
// nothing: no build, and the preview keeps serving.
func TestHandlePullRequest_ConvertedToDraftKeepsPreviewWhenDraftsPreviewed(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	svc, _ := newSourcePreviewService(t, rig, []reposcan.Workload{{Name: "api"}})
	enqueuer := svc.Enqueuer.(*previewDependencyEnqueuer)
	head := strings.Repeat("a", 40)
	opened, err := svc.handlePullRequest(ctx, pullRequestBody("opened", 42, head, "User", false))
	if err != nil || len(opened.Added) != 1 {
		t.Fatalf("open = (%+v, %v)", opened, err)
	}
	builds := len(enqueuer.specs)
	if _, err := svc.handlePullRequest(ctx, pullRequestBody("converted_to_draft", 42, head, "User", true)); !IsIgnored(err) {
		t.Fatalf("converted to draft = %v, want ignored", err)
	}
	preview, err := rig.mem.AppByID(ctx, opened.Added[0].ID)
	if err != nil || preview.PreviewPrState != state.PreviewPrStateOpen || len(enqueuer.specs) != builds {
		t.Fatalf("preview = (%q, %v), builds %d→%d; want it serving, nothing rebuilt",
			preview.PreviewPrState, err, builds, len(enqueuer.specs))
	}
}

// A delayed event sent while the PR was a draft must not skip the preview of
// a PR that is ready for review now: the PR's draft state comes from GitHub.
func TestHandlePullRequest_DelayedDraftEventUsesCurrentDraftState(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	svc, _ := newSourcePreviewService(t, rig, []reposcan.Workload{{Name: "api"}})
	setPreviewPolicy(t, rig, func(p *state.GitHubDeployPolicy) { p.PreviewDraftPRs = false })
	head := strings.Repeat("a", 40)
	svc.PullRequests = stubCurrentPullRequests{snapshot: PullRequestSnapshot{
		State: "open", HeadSHA: head, HeadRepoFullName: "octo/api", Draft: false,
	}}
	result, err := svc.handlePullRequest(ctx, pullRequestBody("opened", 42, head, "User", true))
	if err != nil || len(result.Added) != 1 {
		t.Fatalf("delayed draft event of a ready PR = (%+v, %v), want the preview", result, err)
	}
}

// Turning bot previews off retires a bot PR's preview on its next push
// instead of leaving it to serve the old head while the push is skipped.
func TestHandlePullRequest_BotPreviewsOffRetiresPreview(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	svc, _ := newSourcePreviewService(t, rig, []reposcan.Workload{{Name: "api"}})
	setPreviewPolicy(t, rig, func(p *state.GitHubDeployPolicy) { p.PreviewBotPRs = true })
	opened, err := svc.handlePullRequest(ctx, pullRequestBody("opened", 42, strings.Repeat("a", 40), "Bot", false))
	if err != nil || len(opened.Added) != 1 {
		t.Fatalf("open = (%+v, %v)", opened, err)
	}
	setPreviewPolicy(t, rig, func(p *state.GitHubDeployPolicy) { p.PreviewBotPRs = false })
	if _, err := svc.handlePullRequest(ctx, pullRequestBody("synchronize", 42, strings.Repeat("b", 40), "Bot", false)); !IsIgnored(err) {
		t.Fatalf("bot push with bot previews off = %v, want skipped", err)
	}
	assertPreviewRetired(t, rig, opened.Added[0].ID)
}

// A preview serves what production serves: a WebSocket or streaming app's
// preview answered upgrades with 501 and buffered streamed responses. The
// switches follow production on every head.
func TestHandlePullRequest_PreviewKeepsProductionProtocols(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	on, cpu := true, 2000
	if _, err := rig.mem.UpdateApp(ctx, rig.parentID, state.UpdateAppParams{
		WebSocketEnabled: &on, SetWebSocketEnabled: true, StreamingEnabled: &on, SetStreamingEnabled: true,
		CPUMillicores: &cpu,
	}); err != nil {
		t.Fatal(err)
	}
	svc, _ := newSourcePreviewService(t, rig, []reposcan.Workload{{Name: "api"}})
	opened, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, strings.Repeat("a", 40)))
	if err != nil || len(opened.Added) != 1 {
		t.Fatalf("open = (%+v, %v)", opened, err)
	}
	preview, err := rig.mem.AppByID(ctx, opened.Added[0].ID)
	if err != nil || !preview.WebSocketEnabled || !preview.StreamingEnabled || preview.CPUMillicores != cpu {
		t.Fatalf("preview = websocket %t, streaming %t, cpu %d (%v); want production's",
			preview.WebSocketEnabled, preview.StreamingEnabled, preview.CPUMillicores, err)
	}

	off := false
	if _, err := rig.mem.UpdateApp(ctx, rig.parentID, state.UpdateAppParams{WebSocketEnabled: &off, SetWebSocketEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.handlePullRequest(ctx, pullRequestSyncBody(42, strings.Repeat("b", 40))); err != nil {
		t.Fatalf("push: %v", err)
	}
	if preview, err := rig.mem.AppByID(ctx, opened.Added[0].ID); err != nil || preview.WebSocketEnabled {
		t.Fatalf("preview after production turned WebSockets off = (%t, %v), want off", preview.WebSocketEnabled, err)
	}
}

// With draft previews on, marking a previewed draft ready at the same head
// rebuilt every workload; the preview already served that commit.
func TestHandlePullRequest_ReadyForReviewKeepsLivePreview(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	svc, _ := newSourcePreviewService(t, rig, []reposcan.Workload{{Name: "api"}})
	enqueuer := svc.Enqueuer.(*previewDependencyEnqueuer)
	head := strings.Repeat("a", 40)
	opened, err := svc.handlePullRequest(ctx, pullRequestBody("opened", 42, head, "User", true))
	if err != nil || len(opened.Added) != 1 {
		t.Fatalf("open draft = (%+v, %v)", opened, err)
	}
	if _, err := rig.mem.CreateDeployment(ctx, state.Deployment{AppID: opened.Added[0].ID, Kind: state.DeploymentKindPreview,
		CommitSHA: head, Status: state.DeployLive}); err != nil {
		t.Fatal(err)
	}
	builds := len(enqueuer.specs)
	if _, err := svc.handlePullRequest(ctx, pullRequestBody("ready_for_review", 42, head, "User", false)); err != nil {
		t.Fatalf("ready for review: %v", err)
	}
	if len(enqueuer.specs) != builds {
		t.Fatalf("ready for review queued %d builds, want none for the live head", len(enqueuer.specs)-builds)
	}
}
