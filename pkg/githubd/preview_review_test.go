package githubd

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// newDependencyPreviewService wires the production (fetch, scan, reserve,
// build) PR path over a project whose bound workload "api" depends on
// "worker".
func newDependencyPreviewService(t *testing.T, rig *previewTestRig) (*Service, *previewRecorder) {
	t.Helper()
	if err := rig.mem.UpdateAccountPlan(context.Background(), rig.acct, api.PlanScale); err != nil {
		t.Fatal(err)
	}
	return newSourcePreviewService(t, rig, []reposcan.Workload{
		{Name: "api", DependsOn: []string{"worker"}},
		{Name: "worker", Class: reposcan.ClassWorker, Command: []string{"node", "worker.js"}},
	})
}

// A service a PR introduces is previewed at pr-<N>-<service>, the host of
// <service>'s own PR previews. When another account's app holds that name,
// the PR must not take its preview host.
func TestHandlePullRequest_NewServiceCannotTakeAnotherAppsPreviewHost(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	other, err := rig.mem.CreateAccount(ctx, "victim@example.com", "hobby")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rig.mem.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "worker", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive}); err != nil {
		t.Fatalf("seed the other account's app: %v", err)
	}
	svc, rec := newDependencyPreviewService(t, rig)

	result, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, strings.Repeat("a", 40)))
	if !IsIgnored(err) || !result.WasIgnored {
		t.Fatalf("handlePullRequest = (%+v, %v), want a reported setup failure", result, err)
	}
	if _, err := rig.mem.AppBySlug(ctx, "pr-42-worker"); err == nil {
		t.Fatal("pr-42-worker was reserved over another account's app name")
	}
	reported := false
	for _, check := range rec.checks {
		reported = reported || strings.Contains(check.summary, `"worker" has the name of an existing Gregale app`)
	}
	if !reported {
		t.Fatalf("checks = %+v, want the setup failure explained on the PR", rec.checks)
	}
}

// Renaming a production dependency relinks its PR previews; the next head
// replaces pr-<N>-<old> with pr-<N>-<new> instead of failing on the
// mismatched identity.
func TestHandlePullRequest_DependencyRenameReplacesItsPreview(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	if _, err := rig.mem.CreateApp(ctx, state.App{AccountID: rig.acct, ProjectID: rig.parentProjectID, Slug: "worker",
		WorkloadName: "worker", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	svc, _ := newDependencyPreviewService(t, rig)
	if _, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, strings.Repeat("a", 40))); err != nil {
		t.Fatalf("open PR: %v", err)
	}
	old, err := rig.mem.AppBySlug(ctx, "pr-42-worker")
	if err != nil {
		t.Fatalf("dependency preview: %v", err)
	}
	if _, err := rig.mem.RenameApp(ctx, rig.acct, "worker", "jobs"); err != nil {
		t.Fatalf("rename production dependency: %v", err)
	}
	if marked, err := rig.mem.MarkOrphanedPRPreviewsForTeardown(ctx, 100); err != nil || marked != 0 {
		t.Fatalf("orphan sweep after rename = (%d, %v), want no preview marked", marked, err)
	}

	if _, err := svc.handlePullRequest(ctx, pullRequestSyncBody(42, strings.Repeat("b", 40))); err != nil {
		t.Fatalf("push after rename: %v", err)
	}
	if _, err := rig.mem.AppBySlug(ctx, "pr-42-jobs"); err != nil {
		t.Fatalf("renamed dependency preview: %v", err)
	}
	retired, err := rig.mem.AppByID(ctx, old.ID)
	if err != nil || retired.Status != state.AppDeleted {
		t.Fatalf("old dependency preview = (%+v, %v), want retired", retired, err)
	}
}

// Renaming the production app keeps its open PR previews: they no longer
// read as orphaned to the preview janitor.
func TestRenameProductionAppKeepsPRPreviews(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	svc, _ := newPreviewService(t, rig)
	if _, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, strings.Repeat("a", 40))); err != nil {
		t.Fatalf("open PR: %v", err)
	}
	if _, err := rig.mem.RenameApp(ctx, rig.acct, "demo-app", "demo-renamed"); err != nil {
		t.Fatalf("rename production app: %v", err)
	}
	if marked, err := rig.mem.MarkOrphanedPRPreviewsForTeardown(ctx, 100); err != nil || marked != 0 {
		t.Fatalf("orphan sweep after rename = (%d, %v), want no preview marked", marked, err)
	}
	preview, err := rig.mem.AppBySlug(ctx, "pr-42-demo-app")
	if err != nil || preview.PreviewOfSlug != "demo-renamed" || preview.PreviewPrState != state.PreviewPrStateOpen {
		t.Fatalf("preview after rename = (%+v, %v), want open and linked to demo-renamed", preview, err)
	}
}

// A preview torn down before its PR closes (lease expired, destroyed) keeps a
// tombstone under its slug. The close must not rewrite its PR comment to say
// it keeps serving for 24 hours.
func TestHandlePullRequest_CloseAfterTeardownLeavesCommentAlone(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	svc, rec := newPreviewService(t, rig)
	svc.WritePreviewCommentForInstallation = rec.writePreviewComment
	opened, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, strings.Repeat("a", 40)))
	if err != nil || len(opened.Added) != 1 {
		t.Fatalf("open PR = (%+v, %v)", opened, err)
	}
	id := opened.Added[0].ID
	if _, err := rig.mem.SoftDeleteAppCascade(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.mem.SetPreviewPrState(ctx, id, state.PreviewPrStateTornDown); err != nil {
		t.Fatal(err)
	}
	rec.comments = nil

	result, err := svc.handlePullRequest(ctx, pullRequestClosedBody(42, strings.Repeat("a", 40)))
	if err != nil {
		t.Fatalf("close PR: %v", err)
	}
	if len(rec.comments) != 0 {
		t.Fatalf("comments = %+v, want the torn-down comment left alone", rec.comments)
	}
	if len(result.Added) != 0 {
		t.Fatalf("result = %+v, want nothing provisioned", result)
	}
	tomb, err := rig.mem.AppByID(ctx, id)
	if err != nil || tomb.Status != state.AppDeleted || tomb.PreviewPrState != state.PreviewPrStateTornDown {
		t.Fatalf("tombstone = (%+v, %v), want deleted and torn_down", tomb, err)
	}
}
