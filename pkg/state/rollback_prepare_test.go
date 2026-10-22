package state_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStore_PrepareDeploymentRollbackPreservesCurrentLive(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	account, err := m.CreateAccount(ctx, "prepare-rollback@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "prepare-rollback", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	priorStage, _ := json.Marshal(state.StageState{History: []state.StageStateItem{{Name: state.StageReadiness, Status: "completed"}}})
	target := state.Deployment{
		ID: uuid.NewString(), AppID: app.ID, Status: state.DeploySuperseded,
		TrafficPercent: 100, TrafficPercentExplicit: true,
		CanaryPreset: "balanced", CanaryStep: 2, CanaryTotalSteps: 4,
		RolloutState: "aborted", RolloutAbortedAt: &now, RolloutAbortedReason: "old failure",
		StageState: priorStage, CreatedAt: now.Add(-time.Minute),
	}
	current := state.Deployment{ID: uuid.NewString(), AppID: app.ID, Status: state.DeployLive, TrafficPercent: 100, RolloutState: "complete", CreatedAt: now}
	(*m.Deployments())[target.ID] = target
	(*m.Deployments())[current.ID] = current

	prepared, err := m.PrepareDeploymentRollback(ctx, app.ID, target.ID)
	if err != nil {
		t.Fatalf("PrepareDeploymentRollback: %v", err)
	}
	if prepared.Status != state.DeploySnapshotting || prepared.TrafficPercent != 0 || prepared.TrafficPercentExplicit || prepared.CanaryPreset != "none" || prepared.CanaryStep != 0 || prepared.CanaryTotalSteps != 0 || prepared.RolloutState != "pending" || prepared.RolloutStartedAt != nil || prepared.RolloutCompletedAt != nil || prepared.RolloutAbortedAt != nil {
		t.Fatalf("prepared target = %+v", prepared)
	}
	var stage state.StageState
	if err := json.Unmarshal(prepared.StageState, &stage); err != nil {
		t.Fatal(err)
	}
	if stage.Current != state.StageSnapshotPrepare || stage.CurrentStartedAt == nil || len(stage.History) != 1 {
		t.Fatalf("prepared stage = %+v", stage)
	}
	gotCurrent := (*m.Deployments())[current.ID]
	if gotCurrent.Status != state.DeployLive || gotCurrent.TrafficPercent != 100 {
		t.Fatalf("current deployment changed = %+v", gotCurrent)
	}
}

func TestMemStore_PrepareDeploymentRollbackAcceptsZeroTrafficLive(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	account, err := m.CreateAccount(ctx, "prepare-zero-traffic@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "prepare-zero-traffic", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	target := state.Deployment{ID: uuid.NewString(), AppID: app.ID, Status: state.DeployLive, TrafficPercent: 0}
	current := state.Deployment{ID: uuid.NewString(), AppID: app.ID, Status: state.DeployLive, TrafficPercent: 100}
	(*m.Deployments())[target.ID], (*m.Deployments())[current.ID] = target, current
	if _, err := m.GetDeploymentByIDScopedToSuperseded(ctx, app.ID, target.ID); err != nil {
		t.Fatalf("zero-traffic target lookup: %v", err)
	}
	prepared, err := m.PrepareDeploymentRollback(ctx, app.ID, target.ID)
	if err != nil || prepared.Status != state.DeploySnapshotting || prepared.TrafficPercent != 0 {
		t.Fatalf("prepare zero-traffic target = %+v, err=%v", prepared, err)
	}
	if got := (*m.Deployments())[current.ID]; got.Status != state.DeployLive || got.TrafficPercent != 100 {
		t.Fatalf("current serving revision changed: %+v", got)
	}
}
