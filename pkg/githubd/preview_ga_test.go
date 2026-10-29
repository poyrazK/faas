package githubd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

type rejectingEnqueuer struct{ err error }

func (e rejectingEnqueuer) Enqueue(context.Context, BuildSpec) (state.Build, error) {
	return state.Build{}, e.err
}

func newSourcePreviewService(t *testing.T, rig *previewTestRig, workloads []reposcan.Workload) (*Service, *previewRecorder) {
	t.Helper()
	svc, rec := newPreviewService(t, rig)
	svc.WritePreviewCommentForInstallation = rec.writePreviewComment
	svc.Source = &stubSource{fsys: fstest.MapFS{"compose.yaml": &fstest.MapFile{Data: []byte("services: {}\n")}}}
	svc.WorkDir = t.TempDir()
	svc.Enqueuer = &previewDependencyEnqueuer{}
	svc.Reconcile.Scan = func(fs.FS) (reposcan.Result, error) {
		return reposcan.Result{Workloads: workloads}, nil
	}
	return svc, rec
}

func lastComment(t *testing.T, rec *previewRecorder) recordedPreviewComment {
	t.Helper()
	if len(rec.comments) == 0 {
		t.Fatal("no PR status comment was written")
	}
	return rec.comments[len(rec.comments)-1]
}

// A head githubd cannot set up fails the PR's check and comment and
// completes the delivery instead of retrying it into a dead letter.
func TestHandlePullRequest_ScanFailureReportsOnPR(t *testing.T) {
	rig := newPreviewRig(t)
	svc, rec := newSourcePreviewService(t, rig, nil)
	svc.Reconcile.Scan = func(fs.FS) (reposcan.Result, error) {
		return reposcan.Result{}, errors.New(`reposcan: .gregale/project.yaml: unknown key "servics"`)
	}
	result, err := svc.handlePullRequest(context.Background(), pullRequestOpenedBody(42, strings.Repeat("a", 40)))
	if !IsIgnored(err) || !result.WasIgnored {
		t.Fatalf("scan failure = (%+v, %v), want a completed, ignored delivery", result, err)
	}
	if len(rec.checks) != 1 || rec.checks[0].phase != githubdgrpc.CheckPhaseFailed ||
		!strings.Contains(rec.checks[0].summary, `could not read the project at this commit: .gregale/project.yaml: unknown key "servics"`) {
		t.Fatalf("checks = %+v, want one failed check naming the scan error", rec.checks)
	}
	comment := lastComment(t, rec)
	if comment.marker != previewCommentMarker("pr-42-demo-app") || !strings.Contains(comment.body, "Gregale preview — failed") ||
		strings.Contains(comment.body, "queued") {
		t.Fatalf("comment = %+v, want the failure on the stable status comment", comment)
	}
}

func TestHandlePullRequest_EnqueueRejectionReportsOnPR(t *testing.T) {
	cases := []struct {
		name, want, forbidden string
		err                   error
	}{
		{name: "rate limit", want: "api could not be built: account deploy rate limit 10 reached",
			err: status.Error(codes.ResourceExhausted, "EnqueueBuild: account deploy rate limit 10 reached; resets at 2026-10-26T10:00:00Z")},
		{name: "manifest", want: "api could not be built: source manifest: port must be 1-65535",
			err: status.Error(codes.InvalidArgument, "EnqueueBuild: source manifest: port must be 1-65535")},
		{name: "account hold", want: "the Gregale account cannot deploy right now", forbidden: "abuse",
			err: status.Error(codes.FailedPrecondition, "EnqueueBuild: account acct-1 is on an abuse hold; deploys are blocked")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newPreviewRig(t)
			svc, rec := newSourcePreviewService(t, rig, []reposcan.Workload{{Name: "api"}})
			svc.Enqueuer = rejectingEnqueuer{err: fmt.Errorf("apid enqueuer: EnqueueBuild: %w", tc.err)}
			result, err := svc.handlePullRequest(context.Background(), pullRequestOpenedBody(42, strings.Repeat("a", 40)))
			if !IsIgnored(err) || !result.WasIgnored {
				t.Fatalf("rejection = (%+v, %v), want a reported setup failure", result, err)
			}
			last := rec.checks[len(rec.checks)-1]
			if last.phase != githubdgrpc.CheckPhaseFailed || !strings.Contains(last.summary, tc.want) ||
				(tc.forbidden != "" && strings.Contains(last.summary, tc.forbidden)) {
				t.Fatalf("checks = %+v, want %q", rec.checks, tc.want)
			}
			set, err := rig.mem.GetPRPreviewSet(context.Background(), rig.install, "octo/api", 42)
			if err != nil || !strings.Contains(set.SetupError, tc.want) {
				t.Fatalf("set = (%+v, %v), want the failure recorded", set, err)
			}
		})
	}
}

func TestEnqueueSetupFailure_KeepsInternalErrorsRetryable(t *testing.T) {
	for _, err := range []error{
		status.Error(codes.InvalidArgument, `EnqueueBuild: source_path "/var/lib/faas/x.tar.gz" is not under staging root "/srv"`),
		status.Error(codes.Internal, "EnqueueBuild: app lookup: connection reset"),
		status.Error(codes.Unavailable, "EnqueueBuild: standards unavailable"),
		errors.New("dial unix /run/faas/apid.sock: connection refused"),
	} {
		if got := enqueueSetupFailure("api", fmt.Errorf("apid enqueuer: EnqueueBuild: %w", err)); got != nil {
			t.Errorf("enqueueSetupFailure(%v) = %v, want a retryable error", err, got)
		}
	}
}

// A fork PR on a repository the account does not preview hears nothing.
func TestHandlePullRequest_ForkRefusalOnlyForPreviewedRepos(t *testing.T) {
	rig := newPreviewRig(t)
	svc, rec := newPreviewService(t, rig)
	unbound := strings.ReplaceAll(string(pullRequestForkBody(42, strings.Repeat("a", 40))),
		`"repository": {"full_name": "octo/api"`, `"repository": {"full_name": "octo/other"`)
	if _, err := svc.handlePullRequest(context.Background(), []byte(unbound)); !IsNoBinding(err) {
		t.Fatalf("unbound fork = %v, want no binding", err)
	}
	policy := state.DefaultGitHubDeployPolicy(rig.parentProjectID, rig.acct)
	policy.PreviewEnabled = false
	if _, err := rig.mem.UpsertGitHubDeployPolicy(context.Background(), policy); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.handlePullRequest(context.Background(), pullRequestForkBody(42, strings.Repeat("a", 40))); !IsIgnored(err) {
		t.Fatalf("fork with previews off = %v, want ignored", err)
	}
	if len(rec.forks) != 0 {
		t.Fatalf("fork checks = %+v, want none outside a previewed repository", rec.forks)
	}
}

// A service the PR adds keeps the bound app's gates and the visibility
// production would give it.
func TestPreviewDependencyParents_NewServiceKeepsGatesAndVisibility(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	allow := []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
	parent, err := rig.mem.AppByID(ctx, rig.parentID)
	if err != nil {
		t.Fatal(err)
	}
	parent.PublicAuthIPAllowlist, parent.RequireSigned = allow, true
	svc, _ := newPreviewService(t, rig)
	parents, err := svc.previewDependencyParents(ctx, parent, reposcan.Result{Workloads: []reposcan.Workload{
		{Name: "api", DependsOn: []string{"admin"}}, {Name: "admin", Unpublished: true},
	}}, 42)
	if err != nil || len(parents) != 1 {
		t.Fatalf("parents = (%+v, %v)", parents, err)
	}
	admin := parents[0]
	if admin.Visibility != api.AppVisibilityInternal || !admin.RequireSigned ||
		len(admin.PublicAuthIPAllowlist) != 1 || admin.PublicAuthIPAllowlist[0] != allow[0] {
		t.Fatalf("new service preview = visibility %q, signed %t, allowlist %v; want internal with the bound app's gates",
			admin.Visibility, admin.RequireSigned, admin.PublicAuthIPAllowlist)
	}
	preview, err := makePRDependencyPreview(admin, 42, time.Now().Add(time.Hour), state.DefaultGitHubDeployPolicy(rig.parentProjectID, rig.acct))
	if err != nil || preview.Visibility != api.AppVisibilityInternal || !preview.RequireSigned {
		t.Fatalf("preview row = (%+v, %v), want the template's visibility and gates", preview, err)
	}
}

// A push that changes only ignored paths keeps a live preview serving.
func TestHandlePullRequest_IgnoredPathsKeepLivePreview(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	svc, rec := newSourcePreviewService(t, rig, []reposcan.Workload{{Name: "api"}})
	enqueuer := svc.Enqueuer.(*previewDependencyEnqueuer)
	headA, headB, headC := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	opened, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, headA))
	if err != nil || len(opened.Added) != 1 {
		t.Fatalf("open = (%+v, %v)", opened, err)
	}
	if _, err := rig.mem.CreateDeployment(ctx, state.Deployment{AppID: opened.Added[0].ID, Kind: state.DeploymentKindPreview,
		CommitSHA: headA, Status: state.DeployLive}); err != nil {
		t.Fatal(err)
	}
	policy := state.DefaultGitHubDeployPolicy(rig.parentProjectID, rig.acct)
	policy.IgnoredPaths = []string{"docs/**"}
	if _, err := rig.mem.UpsertGitHubDeployPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	changed := &stubChangedFiles{files: []string{"docs/readme.md"}}
	svc.ChangedFiles = changed
	builds := len(enqueuer.specs)
	result, err := svc.handlePullRequest(ctx, pullRequestSyncBody(42, headB))
	if err != nil || !result.WasIgnored || len(enqueuer.specs) != builds {
		t.Fatalf("docs-only push = (%+v, %v), builds %d→%d; want the live preview kept", result, err, builds, len(enqueuer.specs))
	}
	if changed.lastBase != headA || changed.lastHead != headB {
		t.Fatalf("compared %s..%s, want the recorded head against the new head", changed.lastBase, changed.lastHead)
	}
	check := rec.checks[len(rec.checks)-1]
	if check.sha != headB || check.phase != githubdgrpc.CheckPhaseLive || !strings.Contains(check.summary, "Preview unchanged") {
		t.Fatalf("check = %+v, want a passing unchanged check on the new head", check)
	}
	changed.files = []string{"docs/readme.md", "src/main.go"}
	if _, err := svc.handlePullRequest(ctx, pullRequestSyncBody(42, headC)); err != nil || len(enqueuer.specs) != builds+1 {
		t.Fatalf("source push = %v, builds %d→%d; want a rebuild", err, builds, len(enqueuer.specs))
	}
}

func TestReportUndeliverablePullRequest_FailsCheckAndComment(t *testing.T) {
	rig := newPreviewRig(t)
	svc, rec := newPreviewService(t, rig)
	svc.WritePreviewCommentForInstallation = rec.writePreviewComment
	svc.ReportUndeliverablePullRequest(context.Background(), WebhookDelivery{
		EventType: "pull_request", Payload: pullRequestOpenedBody(42, strings.Repeat("a", 40)),
	})
	if len(rec.checks) != 1 || rec.checks[0].phase != githubdgrpc.CheckPhaseFailed ||
		!strings.Contains(rec.checks[0].summary, previewDeliveryExhausted) {
		t.Fatalf("checks = %+v, want the exhausted delivery reported", rec.checks)
	}
	if comment := lastComment(t, rec); !strings.Contains(comment.body, "Gregale preview — failed") {
		t.Fatalf("comment = %+v, want a failed status comment", comment)
	}
	rec.checks = nil
	svc.ReportUndeliverablePullRequest(context.Background(), WebhookDelivery{EventType: "push", Payload: []byte(`{}`)})
	svc.ReportUndeliverablePullRequest(context.Background(), WebhookDelivery{
		EventType: "pull_request", Payload: pullRequestClosedBody(42, strings.Repeat("a", 40)),
	})
	if len(rec.checks) != 0 {
		t.Fatalf("checks = %+v, want nothing for a push or a closed PR", rec.checks)
	}
}

func TestPreviewLinksUseDashboardRoutes(t *testing.T) {
	t.Setenv("FAAS_APPS_DOMAIN", "apps.gregale.dev")
	if got := DashboardDeploymentURL("pr-42-api", "dep-1"); got != "https://gregale.dev/dashboard/apps/pr-42-api?deployment=dep-1&tab=Deployments" {
		t.Errorf("deployment URL = %q", got)
	}
	if got := DashboardLogsURL("pr-42-api"); got != "https://gregale.dev/dashboard/apps/pr-42-api?tab=Logs" {
		t.Errorf("logs URL = %q", got)
	}
	body := previewCommentBody(previewCommentMarker("pr-42-api"), "pr-42-api", "https://pr-42-api.gregale.dev",
		strings.Repeat("a", 40), "queued", previewCommentQueued)
	if !strings.Contains(body, "(https://gregale.dev/dashboard/apps/pr-42-api)") || strings.Contains(body, "/destroy") {
		t.Errorf("comment body = %q, want the preview's dashboard page and no dead destroy route", body)
	}
	// The dashboard follows the platform domain when it differs from the
	// apps domain (ADR-20261026100000); preview hosts stay on the apps domain.
	t.Setenv("FAAS_PLATFORM_DOMAIN", "console.example.test")
	if got := DashboardAppURL("pr-42-api"); got != "https://console.example.test/dashboard/apps/pr-42-api" {
		t.Errorf("platform dashboard URL = %q", got)
	}
	if got := previewHostnameForSlug("pr-42-api"); got != "pr-42-api.gregale.dev" {
		t.Errorf("preview host = %q, want the apps domain", got)
	}
}
