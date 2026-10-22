// adr: 590
package sched_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestStageAdmissionKeepsIndependentScaleOutHistory(t *testing.T) {
	ctx := t.Context()
	f := sched.SeedStageSnapshotPolicy(t, 0, false)
	settings, err := state.WorkloadSettingsFromApp((*f.App()))
	if err != nil {
		t.Fatal(err)
	}
	settings.WarmPoolSize = 0
	settings.ScalingPolicy = &state.ScalingPolicy{ScaleOutCooldownS: 600}
	production := sched.StageReaperPolicyDeployment(t, f, "production", settings)
	stage := sched.StageReaperPolicyDeployment(t, f, "stage", settings)
	engine := sched.XNewEngine(t, (*f.Store()), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	first, err := engine.AdmitInstance(ctx, (*f.App()).ID, "", "stage", "")
	if err != nil || first.InstanceID == "" || first.AtCapacity {
		t.Fatalf("initial stage admission: %+v %v", first, err)
	}
	app, err := (*f.Store()).AppByID(ctx, (*f.App()).ID)
	if err != nil || app.LastScaleOutAt != nil {
		t.Fatalf("stage admission wrote production history: %+v %v", app, err)
	}
	history, err := (*f.Store()).RuntimeScalingStateForDeployment(ctx, (*f.Account()).ID, (*f.App()).ID, stage.ID)
	resolved, policyErr := state.ResolveAppForDeployment(ctx, (*f.Store()), (*f.App()), stage)
	if err != nil || history.LastScaleOutAt == nil || policyErr != nil || resolved.ScalingPolicy == nil || resolved.ScalingPolicy.ScaleOutCooldownS != 600 || (*engine.XLedger()).Concurrency((*f.App()).ID) != 1 {
		t.Fatalf("stage admission did not retain clock/policy/ledger: %+v policy=%+v concurrency=%d err=%v policyErr=%v", history, resolved.ScalingPolicy, (*engine.XLedger()).Concurrency((*f.App()).ID), err, policyErr)
	}
	second, err := engine.AdmitInstance(ctx, (*f.App()).ID, "", "stage", "")
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodeWaitForWarm || second.InstanceID != "" {
		t.Fatalf("stage ignored its cooldown: %+v %v", second, err)
	}
	prod, err := engine.AdmitInstance(ctx, (*f.App()).ID, "", "production", "")
	if err != nil || prod.InstanceID == "" || prod.AtCapacity {
		t.Fatalf("stage clock held production admission: %+v %v", prod, err)
	}
	second, err = engine.AdmitInstance(ctx, (*f.App()).ID, "", "production", "")
	if !errors.As(err, &problem) || problem.Code != api.CodeWaitForWarm || second.InstanceID != "" {
		t.Fatalf("production ignored its cooldown: %+v %v", second, err)
	}
	prodHistory, err := (*f.Store()).RuntimeScalingStateForDeployment(ctx, (*f.Account()).ID, (*f.App()).ID, production.ID)
	if err != nil || prodHistory.LastScaleOutAt == nil {
		t.Fatalf("production clock missing: %+v %v", prodHistory, err)
	}
}

func TestReaperUsesEnvironmentScaleInAndScaleOutHistory(t *testing.T) {
	for _, scope := range []string{"production", "stage"} {
		for _, direction := range []string{"in", "out"} {
			t.Run(scope+"/"+direction, func(t *testing.T) {
				ctx := t.Context()
				f := sched.SeedStageSnapshotPolicy(t, 0, false)
				settings, err := state.WorkloadSettingsFromApp((*f.App()))
				if err != nil {
					t.Fatal(err)
				}
				settings.WarmPoolSize = 0
				settings.ScalingPolicy = &state.ScalingPolicy{ScaleInCooldownS: 600}
				production := sched.StageReaperPolicyDeployment(t, f, "production", settings)
				stage := sched.StageReaperPolicyDeployment(t, f, "stage", settings)
				dep := production
				if scope == "stage" {
					dep = stage
				}
				if direction == "in" {
					err = (*f.Store()).StampDeploymentScaleIn(ctx, dep.ID)
				} else {
					err = (*f.Store()).StampDeploymentScaleOut(ctx, dep.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now().Add(180 * time.Second)
				rows := []sched.InstanceInfo{
					{Instance: "production", AppID: (*f.App()).ID, DeploymentID: production.ID, State: state.StateRunning, Plan: api.PlanPro, Started: now.Add(-time.Hour), LastRequest: now.Add(-time.Hour)},
					{Instance: "stage", AppID: (*f.App()).ID, DeploymentID: stage.ID, State: state.StateRunning, Plan: api.PlanPro, Started: now.Add(-time.Hour), LastRequest: now.Add(-time.Hour)},
				}
				loop := sched.NewLoop(nil, sched.XNewEngine(t, (*f.Store()), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0"), sched.TestLog())
				loop.EnrichReaperEnvironmentPolicies(ctx, []state.App{(*f.App())}, rows, nil)
				ids := sched.ReapIdle(now, rows, nil, nil)
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
	MemStore *state.MemStore
	foreign  bool
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
			f := sched.SeedStageSnapshotPolicy(t, 0, false)
			store := invalidScalingStateStore{MemStore: (*f.Store()), foreign: foreign}
			engine := sched.XNewEngine(t, statetest.NewMem(store), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
			result, err := engine.AdmitInstance(t.Context(), (*f.App()).ID, (*f.Stage()).ID, "stage", "")
			if !errors.Is(err, state.ErrConflict) || result.InstanceID != "" {
				t.Fatalf("unowned clocks authorized admission: %+v %v", result, err)
			}
			now := time.Now()
			rows := []sched.InstanceInfo{{Instance: "stage", AppID: (*f.App()).ID, DeploymentID: (*f.Stage()).ID, State: state.StateRunning, Plan: api.PlanPro, Started: now.Add(-time.Hour), LastRequest: now.Add(-time.Hour)}}
			loop := sched.NewLoop(nil, engine, sched.TestLog())
			loop.EnrichReaperEnvironmentPolicies(t.Context(), []state.App{(*f.App())}, rows, nil)
			if !rows[0].PolicyUnavailable || len(sched.ReapIdle(now, rows, nil, nil)) != 0 {
				t.Fatalf("unowned clocks authorized reaping: %+v", rows)
			}
		})
	}
}
