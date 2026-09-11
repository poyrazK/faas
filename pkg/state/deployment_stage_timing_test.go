package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgStoreDeploymentStageTimeline exercises the complete customer-visible
// stage lifecycle. In particular, source_download starts at enqueue time, so
// its history entry includes a long source/build phase instead of the null/0
// duration produced by the schema default alone.
func TestPgStoreDeploymentStageTimeline(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "stage-timing-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "stage-timing-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	started := time.Date(2026, 9, 12, 8, 0, 0, 123000000, time.UTC)
	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + uuid.NewString(),
		Status: state.DeployPending, CreatedAt: started,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	var initial state.StageState
	if err := json.Unmarshal(dep.StageState, &initial); err != nil {
		t.Fatalf("decode initial stage_state: %v", err)
	}
	if initial.Current != state.StageSourceDownload {
		t.Fatalf("initial current = %q, want %q", initial.Current, state.StageSourceDownload)
	}
	if initial.CurrentStartedAt == nil || !initial.CurrentStartedAt.Equal(started) {
		t.Fatalf("initial current_started_at = %v, want %v", initial.CurrentStartedAt, started)
	}

	type transition struct {
		from state.StageName
		to   state.StageName
		at   time.Time
	}
	transitions := []transition{
		{state.StageSourceDownload, state.StageDependencyRestore, started.Add(100 * time.Second)},
		{state.StageDependencyRestore, state.StageImageBuild, started.Add(105 * time.Second)},
		{state.StageImageBuild, state.StageSecurityScan, started.Add(108 * time.Second)},
		{state.StageSecurityScan, state.StageSnapshotPrepare, started.Add(118 * time.Second)},
		{state.StageSnapshotPrepare, state.StageReadiness, started.Add(138 * time.Second)},
	}
	for _, step := range transitions {
		if _, err := s.AppendDeploymentStage(ctx, dep.ID, step.from, step.to, step.at, ""); err != nil {
			t.Fatalf("AppendDeploymentStage(%s -> %s): %v", step.from, step.to, err)
		}
	}
	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	terminal := started.Add(150 * time.Second)
	got, err := s.CloseDeploymentStage(ctx, dep.ID, state.StageReadiness, terminal)
	if err != nil {
		t.Fatalf("CloseDeploymentStage: %v", err)
	}

	var timeline state.StageState
	if err := json.Unmarshal(got.StageState, &timeline); err != nil {
		t.Fatalf("decode terminal stage_state: %v", err)
	}
	if timeline.Current != "" {
		t.Fatalf("terminal current = %q, want empty", timeline.Current)
	}
	if len(timeline.History) != len(transitions)+1 {
		t.Fatalf("terminal history length = %d, want %d", len(timeline.History), len(transitions)+1)
	}
	wantNames := []state.StageName{
		state.StageSourceDownload,
		state.StageDependencyRestore,
		state.StageImageBuild,
		state.StageSecurityScan,
		state.StageSnapshotPrepare,
		state.StageReadiness,
	}
	for i, item := range timeline.History {
		if item.Name != wantNames[i] {
			t.Errorf("history[%d].name = %q, want %q", i, item.Name, wantNames[i])
		}
		if item.StartedAt == nil || item.EndedAt == nil {
			t.Errorf("history[%d] timestamps = started %v ended %v, want both set", i, item.StartedAt, item.EndedAt)
			continue
		}
		if item.DurationMs < 0 {
			t.Errorf("history[%d].duration_ms = %d, want nonnegative", i, item.DurationMs)
		}
		if gotDuration := item.EndedAt.Sub(*item.StartedAt).Milliseconds(); gotDuration != item.DurationMs {
			t.Errorf("history[%d].duration_ms = %d, timestamps imply %d", i, item.DurationMs, gotDuration)
		}
	}
	if got := timeline.History[0].DurationMs; got != 100000 {
		t.Errorf("source_download duration_ms = %d, want 100000 (100-second source phase)", got)
	}
}
