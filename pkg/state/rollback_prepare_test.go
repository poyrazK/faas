package state

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStore_PrepareDeploymentRollbackPreservesCurrentLive(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "prepare-rollback@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: account.ID, Slug: "prepare-rollback", Type: AppTypeApp, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	priorStage, _ := json.Marshal(StageState{History: []StageStateItem{{Name: StageReadiness, Status: "completed"}}})
	target := Deployment{
		ID: uuid.NewString(), AppID: app.ID, Status: DeploySuperseded,
		TrafficPercent: 100, TrafficPercentExplicit: true,
		CanaryPreset: "balanced", CanaryStep: 2, CanaryTotalSteps: 4,
		RolloutState: "aborted", RolloutAbortedAt: &now, RolloutAbortedReason: "old failure",
		StageState: priorStage, CreatedAt: now.Add(-time.Minute),
	}
	current := Deployment{ID: uuid.NewString(), AppID: app.ID, Status: DeployLive, TrafficPercent: 100, RolloutState: "complete", CreatedAt: now}
	m.deployments[target.ID] = target
	m.deployments[current.ID] = current

	prepared, err := m.PrepareDeploymentRollback(ctx, app.ID, target.ID)
	if err != nil {
		t.Fatalf("PrepareDeploymentRollback: %v", err)
	}
	if prepared.Status != DeploySnapshotting || prepared.TrafficPercent != 0 || prepared.TrafficPercentExplicit || prepared.CanaryPreset != "none" || prepared.CanaryStep != 0 || prepared.CanaryTotalSteps != 0 || prepared.RolloutState != "pending" || prepared.RolloutStartedAt != nil || prepared.RolloutCompletedAt != nil || prepared.RolloutAbortedAt != nil {
		t.Fatalf("prepared target = %+v", prepared)
	}
	var stage StageState
	if err := json.Unmarshal(prepared.StageState, &stage); err != nil {
		t.Fatal(err)
	}
	if stage.Current != StageSnapshotPrepare || stage.CurrentStartedAt == nil || len(stage.History) != 1 {
		t.Fatalf("prepared stage = %+v", stage)
	}
	gotCurrent := m.deployments[current.ID]
	if gotCurrent.Status != DeployLive || gotCurrent.TrafficPercent != 100 {
		t.Fatalf("current deployment changed = %+v", gotCurrent)
	}
}
