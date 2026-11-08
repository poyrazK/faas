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

// adr: 1448 — amendment 1: a person records a check after a release command
// whose outcome is unknown.
func TestReleaseOutcomeCheck(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "migrating", RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	release := func(digest string) state.Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: digest, Status: state.DeploySnapshotting, ReleaseCommand: []string{"bin/migrate"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, d.ID, "/tmp/"+d.ID+".ext4", "apps/migrating/"+d.ID+".ext4", 4096); err != nil {
			t.Fatal(err)
		}
		return d
	}
	lost, unstarted := release("sha256:lost"), release("sha256:unstarted")
	now := time.Now().UTC()
	task, err := store.CreateAppTask(ctx, state.CreateAppTaskParams{AccountID: app.AccountID, AppID: app.ID, DeploymentID: lost.ID,
		Kind: state.AppTaskKindRelease, Command: []string{"bin/migrate"}, CreatedAt: now.Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimNextAppTask(ctx, "schedd-a", now, time.Second)
	if err != nil || claimed.ID != task.ID {
		t.Fatalf("claim = %+v, err=%v", claimed, err)
	}
	if _, err := store.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, now.Add(100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SweepExpiredAppTasks(ctx, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}

	path := "/v1/deployments/" + lost.ID + "/release-outcome"
	apidserver.AssertProblem(t, e.Do(t, http.MethodPost, path, api.RecordReleaseOutcomeCheckRequest{Note: "  "}, nil),
		http.StatusBadRequest, api.CodeValidation)
	apidserver.AssertProblem(t, e.Do(t, http.MethodPost, "/v1/deployments/"+unstarted.ID+"/release-outcome",
		api.RecordReleaseOutcomeCheckRequest{Note: "nothing ran"}, nil), http.StatusConflict, api.CodeConflict)
	rec := e.Do(t, http.MethodPost, path, api.RecordReleaseOutcomeCheckRequest{Note: "migration 42 applied; schema matches"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("check = %d %s, want 200", rec.Code, rec.Body)
	}
	var check api.ReleaseOutcomeCheck
	if err := json.Unmarshal(rec.Body.Bytes(), &check); err != nil {
		t.Fatal(err)
	}
	if check.TaskID != task.ID || check.DeploymentID != lost.ID || check.TaskStatus != string(state.AppTaskFailed) || check.CheckedBy == "" {
		t.Fatalf("check = %+v", check)
	}
	if _, err := store.UncertainReleaseTask(ctx, app.AccountID, app.ID, "default", unstarted.ID); err == nil {
		t.Fatal("the checked release command still holds later ones")
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodPost, path, api.RecordReleaseOutcomeCheckRequest{Note: "again"}, nil),
		http.StatusConflict, api.CodeConflict)
}
