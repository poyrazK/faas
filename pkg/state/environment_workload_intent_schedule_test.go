package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestChangedWorkloadIntentsPersistsReviewedSchedule(t *testing.T) {
	workload := api.EnvironmentWorkload{Runtime: json.RawMessage(`{"execution_mode":"job"}`), Variables: map[string]string{"MODE": "safe"}, Schedule: &api.EnvironmentJobSchedule{
		Cron: "0 3 * * *", Timezone: "Europe/Istanbul",
		SchedulePolicy: &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "skip", MissedRuns: "coalesce_latest"},
		FailureRules:   &workpolicy.FailureRules{Version: workpolicy.Version, Rules: []workpolicy.FailureRule{{ExitCodes: []int{2}, Action: "fail_partition"}}, UnmatchedFailure: "retry", UncertainOutcome: "hold"},
	}}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production",
		Workloads: map[string]api.EnvironmentWorkload{"reports": workload}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := environmentsync.BuildPlan(desired, environmentsync.ObservedState{Version: 1}, nil,
		environmentsync.PlanOptions{Manager: "source", Revision: "revision", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	const appID = "app-reports"
	changed := changedWorkloadIntents(gitOpsIntentSnapshot{}, plan, map[string]string{"workload/reports": appID}, false)
	got, exists := changed[appID]
	if !exists || got.Variables["MODE"] != "safe" || got.Schedule == nil || got.Schedule.Cron != "0 3 * * *" || got.Schedule.Timezone != "Europe/Istanbul" ||
		got.Schedule.SchedulePolicy == nil || got.Schedule.SchedulePolicy.Overlap != "skip" ||
		got.Schedule.FailureRules == nil || len(got.Schedule.FailureRules.Rules) != 1 || got.Schedule.FailureRules.Rules[0].ExitCodes[0] != 2 {
		t.Fatalf("reviewed schedule was not carried into scoped workload intent: %+v", got.Schedule)
	}
	got.Schedule.FailureRules.Rules[0].ExitCodes[0] = 9
	got.Variables["MODE"] = "mutated"
	if workload.Schedule.FailureRules.Rules[0].ExitCodes[0] != 2 {
		t.Fatal("workload intent schedule aliases the desired definition")
	}
	if workload.Variables["MODE"] != "safe" {
		t.Fatal("workload intent variables alias the desired definition")
	}
}

func TestValidateWorkloadIntentRejectsVariableServiceBindingCollision(t *testing.T) {
	const appID = "550e8400-e29b-41d4-a716-446655440002"
	row := EnvironmentWorkloadIntent{AppID: appID, Variables: map[string]string{"DATABASE_URL": "not-a-url"},
		ServiceBindings: map[string]EnvironmentScopedServiceBinding{"database": {
			Workload: "postgres", EnvKey: "DATABASE_URL", TargetAppID: "550e8400-e29b-41d4-a716-446655440003",
		}}}
	if _, err := validateWorkloadIntent(row, App{ID: appID}, "production", api.PlanScale); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("variable collision error = %v, want ErrInvalidArgument", err)
	}
}

func TestEnvironmentWorkloadIntentByJobReturnsFrozenBindings(t *testing.T) {
	store := NewMemStore()
	store.appEnvironmentWorkloadIntents = map[environmentWorkloadIntentKey]EnvironmentWorkloadIntent{
		{AppID: "app-1", EnvironmentID: "env-1"}: {
			AccountID: "account-1", AppID: "app-1", EnvironmentID: "env-1", JobID: "job-1",
			Variables: map[string]string{"MODE": "safe"},
			ServiceBindings: map[string]EnvironmentScopedServiceBinding{
				"database": {Workload: "postgres", EnvKey: "DATABASE_URL", TargetAppID: "app-2"},
			},
		},
	}
	got, err := store.EnvironmentWorkloadIntentByJob(context.Background(), "account-1", "job-1")
	if err != nil || got.JobID != "job-1" || got.Variables["MODE"] != "safe" || got.ServiceBindings["database"].TargetAppID != "app-2" {
		t.Fatalf("intent by Job = %+v, err %v", got, err)
	}
	got.ServiceBindings["database"] = EnvironmentScopedServiceBinding{TargetAppID: "mutated"}
	got.Variables["MODE"] = "mutated"
	stored := store.appEnvironmentWorkloadIntents[environmentWorkloadIntentKey{AppID: "app-1", EnvironmentID: "env-1"}]
	if stored.Variables["MODE"] != "safe" || stored.ServiceBindings["database"].TargetAppID != "app-2" {
		t.Fatal("intent lookup returned mutable store state")
	}
	if _, err := store.EnvironmentWorkloadIntentByJob(context.Background(), "account-1", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing intent error = %v, want ErrNotFound", err)
	}
}
