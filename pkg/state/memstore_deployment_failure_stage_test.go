package state

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestMemStoreSetDeploymentFailedClosesActiveStage(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "stage-failure@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "stage-failure", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendDeploymentStage(ctx, dep.ID, StageSourceDownload, StageDependencyRestore, time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendDeploymentStage(ctx, dep.ID, StageDependencyRestore, StageImageBuild, time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}

	failed, err := store.SetDeploymentFailed(ctx, dep.ID, "app_startup_timeout", "startup timed out")
	if err != nil {
		t.Fatal(err)
	}
	var stages StageState
	if err := json.Unmarshal(failed.StageState, &stages); err != nil {
		t.Fatal(err)
	}
	if failed.Status != DeployFailed {
		t.Fatalf("status = %q, want %q", failed.Status, DeployFailed)
	}
	if stages.Current != "" || stages.CurrentStartedAt != nil {
		t.Fatalf("active stage survived failure: %+v", stages)
	}
	if len(stages.History) != 3 {
		t.Fatalf("history length = %d, want 3", len(stages.History))
	}
	last := stages.History[len(stages.History)-1]
	if last.Name != StageImageBuild || last.Status != stageHistoryStatusFailed || last.Reason != "startup timed out" {
		t.Fatalf("failed stage = %+v", last)
	}
	if last.EndedAt == nil || last.DurationMs < 0 {
		t.Fatalf("failed stage timing = %+v", last)
	}

	// A retry of the terminal write must not append a duplicate stage once
	// Current has been cleared.
	retried, err := store.SetDeploymentFailed(ctx, dep.ID, "app_startup_timeout", "startup timed out again")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(retried.StageState, &stages); err != nil {
		t.Fatal(err)
	}
	if len(stages.History) != 3 {
		t.Fatalf("idempotent failure appended history: len=%d", len(stages.History))
	}
}
