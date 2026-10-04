// adr: 581
package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStageAdmissionKeepsIndependentScaleOutHistory(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	settings.WarmPoolSize = 0
	settings.ScalingPolicy = &state.ScalingPolicy{ScaleOutCooldownS: 600}
	production := stageReaperPolicyDeployment(t, f, "production", settings)
	stage := stageReaperPolicyDeployment(t, f, "stage", settings)
	engine := newEngine(t, f.store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	first, err := engine.AdmitInstance(ctx, f.app.ID, "", "stage", "")
	if err != nil || first.InstanceID == "" || first.AtCapacity {
		t.Fatalf("initial stage admission: %+v %v", first, err)
	}
	app, err := f.store.AppByID(ctx, f.app.ID)
	if err != nil || app.LastScaleOutAt != nil {
		t.Fatalf("stage admission wrote production history: %+v %v", app, err)
	}
	history, err := f.store.RuntimeScalingStateForDeployment(ctx, f.account.ID, f.app.ID, stage.ID)
	resolved, policyErr := state.ResolveAppForDeployment(ctx, f.store, f.app, stage)
	if err != nil || history.LastScaleOutAt == nil || policyErr != nil || resolved.ScalingPolicy == nil || resolved.ScalingPolicy.ScaleOutCooldownS != 600 || engine.ledger.Concurrency(f.app.ID) != 1 {
		t.Fatalf("stage admission did not retain clock/policy/ledger: %+v policy=%+v concurrency=%d err=%v policyErr=%v", history, resolved.ScalingPolicy, engine.ledger.Concurrency(f.app.ID), err, policyErr)
	}
	second, err := engine.AdmitInstance(ctx, f.app.ID, "", "stage", "")
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodeWaitForWarm || second.InstanceID != "" {
		t.Fatalf("stage ignored its cooldown: %+v %v", second, err)
	}
	prod, err := engine.AdmitInstance(ctx, f.app.ID, "", "production", "")
	if err != nil || prod.InstanceID == "" || prod.AtCapacity {
		t.Fatalf("stage clock held production admission: %+v %v", prod, err)
	}
	second, err = engine.AdmitInstance(ctx, f.app.ID, "", "production", "")
	if !errors.As(err, &problem) || problem.Code != api.CodeWaitForWarm || second.InstanceID != "" {
		t.Fatalf("production ignored its cooldown: %+v %v", second, err)
	}
	prodHistory, err := f.store.RuntimeScalingStateForDeployment(ctx, f.account.ID, f.app.ID, production.ID)
	if err != nil || prodHistory.LastScaleOutAt == nil {
		t.Fatalf("production clock missing: %+v %v", prodHistory, err)
	}
}

func TestReaperUsesEnvironmentScaleInAndScaleOutHistory(t *testing.T) {
	for _, scope := range []string{"production", "stage"} {
		for _, direction := range []string{"in", "out"} {
			t.Run(scope+"/"+direction, func(t *testing.T) {
				ctx := t.Context()
				f := seedStageSnapshotPolicy(t, 0, false)
				settings, err := state.WorkloadSettingsFromApp(f.app)
				if err != nil {
					t.Fatal(err)
				}
				settings.WarmPoolSize = 0
				settings.ScalingPolicy = &state.ScalingPolicy{ScaleInCooldownS: 600}
				production := stageReaperPolicyDeployment(t, f, "production", settings)
				stage := stageReaperPolicyDeployment(t, f, "stage", settings)
				dep := production
				if scope == "stage" {
					dep = stage
				}
				if direction == "in" {
					err = f.store.StampDeploymentScaleIn(ctx, dep.ID)
				} else {
					err = f.store.StampDeploymentScaleOut(ctx, dep.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now().Add(180 * time.Second)
				rows := []InstanceInfo{
					{Instance: "production", AppID: f.app.ID, DeploymentID: production.ID, State: state.StateRunning, Plan: api.PlanPro, Started: now.Add(-time.Hour), LastRequest: now.Add(-time.Hour)},
					{Instance: "stage", AppID: f.app.ID, DeploymentID: stage.ID, State: state.StateRunning, Plan: api.PlanPro, Started: now.Add(-time.Hour), LastRequest: now.Add(-time.Hour)},
				}
				loop := NewLoop(nil, newEngine(t, f.store, &fakeVMM{}, &fakeNotifier{}, "1.10.0"), testLog())
				loop.enrichReaperEnvironmentPolicies(ctx, []state.App{f.app}, rows, nil)
				ids := ReapIdle(now, rows, nil, nil)
				want := "stage"
				if scope == "stage" {
					want = "production"
				}
				if len(ids) != 1 || ids[0] != want {
					t.Fatalf("%s clock held sibling or missed own cooldown: parked=%v rows=%+v", scope, ids, rows)
				}
			})
		}
	}
}

type invalidScalingStateStore struct {
	*state.MemStore
	foreign bool
}

func (s invalidScalingStateStore) RuntimeScalingStateForDeployment(ctx context.Context, accountID, appID, deploymentID string) (state.RuntimeScalingState, error) {
	if !s.foreign {
		return state.RuntimeScalingState{}, state.ErrConflict
	}
	row, err := s.MemStore.RuntimeScalingStateForDeployment(ctx, accountID, appID, deploymentID)
	row.DeploymentID = "foreign-deployment"
	return row, err
}

func TestStageScalingHistoryReadFailureHoldsAdmissionAndReaping(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "read-failure", true: "foreign-owner"}[foreign], func(t *testing.T) {
			f := seedStageSnapshotPolicy(t, 0, false)
			store := invalidScalingStateStore{MemStore: f.store, foreign: foreign}
			engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
			result, err := engine.AdmitInstance(t.Context(), f.app.ID, f.stage.ID, "stage", "")
			if !errors.Is(err, state.ErrConflict) || result.InstanceID != "" {
				t.Fatalf("unowned clocks authorized admission: %+v %v", result, err)
			}
			now := time.Now()
			rows := []InstanceInfo{{Instance: "stage", AppID: f.app.ID, DeploymentID: f.stage.ID, State: state.StateRunning, Plan: api.PlanPro, Started: now.Add(-time.Hour), LastRequest: now.Add(-time.Hour)}}
			loop := NewLoop(nil, engine, testLog())
			loop.enrichReaperEnvironmentPolicies(t.Context(), []state.App{f.app}, rows, nil)
			if !rows[0].PolicyUnavailable || len(ReapIdle(now, rows, nil, nil)) != 0 {
				t.Fatalf("unowned clocks authorized reaping: %+v", rows)
			}
		})
	}
}
