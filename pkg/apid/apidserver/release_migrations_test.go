package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// releaseHistory is v1 (no release command) superseded by v2, whose release
// ran while v1 served, superseded by v3, whose release ran while v2 served.
type releaseHistory struct {
	e          apidserver.TestEnv
	app        state.App
	v1, v2, v3 state.Deployment
}

func newReleaseHistory(t *testing.T, slug string) releaseHistory {
	t.Helper()
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	store := *e.Store()
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: slug, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	deploy := func(command []string) state.Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + slug + time.Now().Format("150405.000000000"),
			Status: state.DeploySnapshotting, ReleaseCommand: command,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []state.DeploymentStatus{state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting} {
			if err := store.UpdateDeploymentStatus(ctx, d.ID, s, ""); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.SetDeploymentRootfs(ctx, d.ID, "/tmp/r.ext4", "apps/"+slug+"/"+d.ID+".ext4", 4096); err != nil {
			t.Fatal(err)
		}
		if len(command) > 0 {
			runRelease(t, store, (*e.Acct()).ID, app.ID, d.ID)
		}
		if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		got, err := store.DeploymentByID(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	h := releaseHistory{e: e, app: app}
	h.v1 = deploy(nil)
	h.v2 = deploy([]string{"bin/migrate"})
	h.v3 = deploy([]string{"bin/migrate"})
	for _, d := range []state.Deployment{h.v1, h.v2} {
		if got, _ := store.DeploymentByID(ctx, d.ID); got.Status != state.DeploySuperseded {
			t.Fatalf("fixture: %s status %q, want superseded", d.ID, got.Status)
		}
	}
	return h
}

// runRelease runs the deployment's release task to success now.
func runRelease(t *testing.T, store state.Store, accountID, appID, deploymentID string) {
	t.Helper()
	tasks, ok := store.(state.AppTaskStore)
	if !ok {
		t.Fatal("store has no app tasks")
	}
	ctx := context.Background()
	now := time.Now().UTC()
	task, err := tasks.CreateAppTask(ctx, state.CreateAppTaskParams{
		AccountID: accountID, AppID: appID, DeploymentID: deploymentID,
		Kind: state.AppTaskKindRelease, Command: []string{"bin/migrate"}, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := tasks.ClaimNextAppTask(ctx, "schedd", now, time.Minute)
	if err != nil || claimed.ID != task.ID {
		t.Fatalf("claim release task = %+v, %v", claimed, err)
	}
	if _, err := tasks.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	exit := 0
	if _, err := tasks.CompleteAppTask(ctx, state.CompleteAppTaskParams{
		ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskSucceeded, ExitCode: &exit, FinishedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

func (h releaseHistory) rollback(t *testing.T, req api.RollbackRequest) (int, api.Problem) {
	t.Helper()
	rec := h.e.Do(t, http.MethodPost, "/v1/apps/"+h.app.Slug+"/rollback", req, nil)
	var p api.Problem
	if rec.Code >= 400 {
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("problem body %s: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, p
}

// TestRollbackToPreviousReleaseCrossesNothing: v3's release ran while v2
// served, so the usual rollback stays one request (ADR-1448).
func TestRollbackToPreviousReleaseCrossesNothing(t *testing.T) {
	h := newReleaseHistory(t, "release-previous")
	if code, p := h.rollback(t, api.RollbackRequest{TargetDeploymentID: &h.v2.ID}); code != http.StatusAccepted {
		t.Fatalf("rollback to v2 = %d %+v, want 202", code, p)
	}
}

// TestRollbackOverReleaseNeedsAcknowledgement: v3's release ran after v1
// last served; v2's ran while v1 served and is not listed.
func TestRollbackOverReleaseNeedsAcknowledgement(t *testing.T) {
	h := newReleaseHistory(t, "release-older")
	code, p := h.rollback(t, api.RollbackRequest{TargetDeploymentID: &h.v1.ID})
	if code != http.StatusConflict || p.Code != api.CodeRollbackCrossesRelease {
		t.Fatalf("rollback to v1 = %d %s, want 409 %s", code, p.Code, api.CodeRollbackCrossesRelease)
	}
	if len(p.CrossedReleases) != 1 || p.CrossedReleases[0].DeploymentID != h.v3.ID || p.CrossedReleases[0].Status != "succeeded" {
		t.Fatalf("crossed releases = %+v, want only v3", p.CrossedReleases)
	}
	if got, _ := (*h.e.Store()).DeploymentByID(context.Background(), h.v1.ID); got.Status != state.DeploySuperseded {
		t.Fatalf("refused rollback changed v1 to %q", got.Status)
	}

	current := h.v3.ID
	reason := "back to v1"
	checked := api.RollbackRequest{TargetDeploymentID: &h.v1.ID, ExpectedCurrentDeploymentID: &current, Reason: reason}
	if code, p := h.rollback(t, checked); code != http.StatusConflict || p.Code != api.CodeRollbackCrossesRelease {
		t.Fatalf("checked rollback to v1 = %d %s, want 409 %s", code, p.Code, api.CodeRollbackCrossesRelease)
	}

	if code, p := h.rollback(t, api.RollbackRequest{TargetDeploymentID: &h.v1.ID, AcknowledgeReleases: true}); code != http.StatusAccepted {
		t.Fatalf("acknowledged rollback to v1 = %d %+v, want 202", code, p)
	}
	audits, err := (*h.e.Store()).ListDeploymentAudit(context.Background(), h.v3.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range audits {
		var data struct {
			CrossedReleases []string `json:"crossed_releases"`
		}
		if a.Kind == state.DeployRolledBack && json.Unmarshal(a.Data, &data) == nil && len(data.CrossedReleases) == 1 && data.CrossedReleases[0] == h.v3.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("rollback audit lacks crossed_releases [v3]: %+v", audits)
	}
}

// TestDeploymentReportsItsRelease: single reads carry the release run.
func TestDeploymentReportsItsRelease(t *testing.T) {
	h := newReleaseHistory(t, "release-report")
	for _, tc := range []struct {
		d    state.Deployment
		want string
	}{{h.v3, "succeeded"}, {h.v1, ""}} {
		rec := h.e.Do(t, http.MethodGet, "/v1/deployments/"+tc.d.ID, nil, nil)
		var got api.DeploymentResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
			t.Fatalf("get %s = %d %s", tc.d.ID, rec.Code, rec.Body.String())
		}
		switch {
		case tc.want == "" && got.Release != nil:
			t.Fatalf("deployment without a release command reports %+v", got.Release)
		case tc.want != "" && (got.Release == nil || got.Release.Status != tc.want || got.Release.StartedAt == "" || got.Release.FinishedAt == ""):
			t.Fatalf("release = %+v, want %s with times", got.Release, tc.want)
		}
	}
}
