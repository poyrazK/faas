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

func TestFinalizeActiveDeploymentStageEdgeCases(t *testing.T) {
	if finalizeActiveDeploymentStage(nil, time.Time{}, time.Time{}, stageHistoryStatusFailed, "") {
		t.Fatal("nil stage state reported an active stage")
	}
	if finalizeActiveDeploymentStage(&StageState{}, time.Time{}, time.Time{}, stageHistoryStatusFailed, "") {
		t.Fatal("empty stage state reported an active stage")
	}

	at := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	startedAfterEnd := at.Add(time.Hour)
	state := StageState{
		Current:          StageImageBuild,
		CurrentStartedAt: &startedAfterEnd,
		History:          make([]StageStateItem, MaxStageHistory),
	}
	if !finalizeActiveDeploymentStage(&state, at.Add(-time.Hour), at, stageHistoryStatusFailed, "boom") {
		t.Fatal("active stage was not finalized")
	}
	if state.Current != "" || state.CurrentStartedAt != nil {
		t.Fatalf("active stage survived finalization: %+v", state)
	}
	if len(state.History) != MaxStageHistory {
		t.Fatalf("history length = %d, want %d", len(state.History), MaxStageHistory)
	}
	last := state.History[len(state.History)-1]
	if last.Status != stageHistoryStatusFailed || last.Reason != "boom" || last.DurationMs != 0 {
		t.Fatalf("finalized stage = %+v", last)
	}

	// A zero timestamp uses the current UTC time and an absent start falls
	// back to the deployment creation time.
	zeroAt := StageState{Current: StageSourceDownload}
	if !finalizeActiveDeploymentStage(&zeroAt, at, time.Time{}, stageHistoryStatusFailed, "") {
		t.Fatal("zero-time active stage was not finalized")
	}
	if len(zeroAt.History) != 1 || zeroAt.History[0].DurationMs < 0 {
		t.Fatalf("zero-time stage history = %+v", zeroAt.History)
	}
}
