package githubd

import (
	"context"
	"testing"
)

type recordedProjectPreviewEvent struct {
	accountID      string
	installationID int64
	repo           string
	prNumber       int
	headSHA        string
	action         string
}

type recordingProjectPreviewReconciler struct {
	events []recordedProjectPreviewEvent
}

func (r *recordingProjectPreviewReconciler) ReconcileProjectPreviewEnvironment(_ context.Context, accountID string, installationID int64, repo string, prNumber int, headSHA, action string) error {
	r.events = append(r.events, recordedProjectPreviewEvent{accountID, installationID, repo, prNumber, headSHA, action})
	return nil
}

func TestHandlePullRequestReconcilesConfiguredProjectPreviewEnvironment(t *testing.T) {
	ctx := context.Background()
	trig := newPreviewRig(t)
	policy, err := trig.mem.GetGitHubDeployPolicy(ctx, trig.parentProjectID, trig.acct)
	if err != nil {
		t.Fatal(err)
	}
	policy.PreviewEnvironmentFrom = "staging"
	if _, err := trig.mem.UpsertGitHubDeployPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	svc, _ := newPreviewService(t, trig)
	reconciler := &recordingProjectPreviewReconciler{}
	svc.ProjectPreviewEnvironments = reconciler

	for _, body := range [][]byte{
		pullRequestOpenedBody(381, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		pullRequestSyncBody(381, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		pullRequestReopenedBody(381, "cccccccccccccccccccccccccccccccccccccccc"),
		pullRequestClosedBody(381, "cccccccccccccccccccccccccccccccccccccccc"),
	} {
		if _, err := svc.handlePullRequest(ctx, body); err != nil {
			t.Fatalf("handle pull request event: %v", err)
		}
	}
	if len(reconciler.events) != 4 {
		t.Fatalf("reconciled %d events, want 4: %+v", len(reconciler.events), reconciler.events)
	}
	for i, event := range reconciler.events {
		if event.accountID != trig.acct || event.installationID != trig.install || event.repo != "octo/api" || event.prNumber != 381 {
			t.Errorf("event %d identity = %+v", i, event)
		}
	}
	if reconciler.events[0].action != "opened" || reconciler.events[0].headSHA != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" ||
		reconciler.events[1].action != "synchronize" || reconciler.events[1].headSHA != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" ||
		reconciler.events[2].action != "reopened" || reconciler.events[2].headSHA != "cccccccccccccccccccccccccccccccccccccccc" ||
		reconciler.events[3].action != "closed" {
		t.Fatalf("reconciled event sequence = %+v", reconciler.events)
	}
}

func TestHandlePullRequestDoesNotReconcileForkProjectPreview(t *testing.T) {
	ctx := context.Background()
	trig := newPreviewRig(t)
	policy, err := trig.mem.GetGitHubDeployPolicy(ctx, trig.parentProjectID, trig.acct)
	if err != nil {
		t.Fatal(err)
	}
	policy.PreviewEnvironmentFrom = "staging"
	if _, err := trig.mem.UpsertGitHubDeployPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	svc, _ := newPreviewService(t, trig)
	reconciler := &recordingProjectPreviewReconciler{}
	svc.ProjectPreviewEnvironments = reconciler
	_, err = svc.handlePullRequest(ctx, pullRequestForkBody(381, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	if err == nil {
		t.Fatal("fork PR was not refused")
	}
	if len(reconciler.events) != 0 {
		t.Fatalf("fork triggered project preview reconciliation: %+v", reconciler.events)
	}
}
