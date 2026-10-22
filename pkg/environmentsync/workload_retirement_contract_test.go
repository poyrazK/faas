package environmentsync

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkloadRetirementContract(t *testing.T) {
	const original = "11111111-2222-4333-8444-555555555555"
	definition := func() api.EnvironmentDefinition {
		return api.EnvironmentDefinition{APIVersion: APIVersion, Project: "shop", Environment: "staging",
			Workloads: map[string]api.EnvironmentWorkload{"worker": {App: "orders-worker"}}}
	}
	for _, tc := range []struct {
		name, policy, workload, id string
		valid                      bool
	}{
		{"unchanged default", "", "", "", true},
		{"retained retirement", "retain", "", "", true},
		{"destructive policy", "delete", "", "", false},
		{"reviewed recovery", "retain", "worker", original, true},
		{"compact identity", "retain", "worker", "11111111222243338444555555555555", true},
		{"unknown workload", "retain", "other", original, false},
		{"empty identity", "retain", "worker", "", false},
		{"nil identity", "retain", "worker", "00000000-0000-0000-0000-000000000000", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := definition()
			d.WorkloadPruningPolicy = tc.policy
			if tc.workload != "" {
				d.WorkloadRecoveries = map[string]string{tc.workload: tc.id}
			}
			got, err := Compile(d)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t, error=%v", tc.valid, err)
			}
			if err == nil && tc.workload != "" && got.Definition.WorkloadRecoveries[tc.workload] != original {
				t.Fatalf("recovery identity was not canonical: %+v", got.Definition.WorkloadRecoveries)
			}
		})
	}
	t.Run("review digest binds disposition and recovery", func(t *testing.T) {
		d := definition()
		first, err := Compile(d)
		if err != nil {
			t.Fatal(err)
		}
		d.WorkloadPruningPolicy = "retain"
		second, err := Compile(d)
		if err != nil {
			t.Fatal(err)
		}
		d.WorkloadRecoveries = map[string]string{"worker": original}
		third, err := Compile(d)
		if err != nil {
			t.Fatal(err)
		}
		if first.Digest == second.Digest || second.Digest == third.Digest || first.Digest == third.Digest {
			t.Fatal("review digest omitted retirement or recovery authority")
		}
		d.WorkloadRecoveries["worker"] = "66666666-7777-4888-8999-aaaaaaaaaaaa"
		if third.Definition.WorkloadRecoveries["worker"] != original {
			t.Fatal("compiled recovery aliases caller memory")
		}
	})
}

func TestWorkloadRetirementPlanPreservesConfiguration(t *testing.T) {
	prior, err := Compile(api.EnvironmentDefinition{APIVersion: APIVersion, Project: "shop", Environment: "staging",
		Workloads: map[string]api.EnvironmentWorkload{
			"api": {}, "worker": {Variables: map[string]string{"MODE": "reviewed"}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	d := prior.Definition
	delete(d.Workloads, "worker")
	d.WorkloadPruningPolicy = "retain"
	desired, err := Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	owners := make([]Ownership, 0, len(prior.Fields))
	for _, field := range prior.Fields {
		owners = append(owners, Ownership{Field: field, Manager: "source"})
	}
	observed := ObservedState{Version: 1, Fields: append([]Field(nil), prior.Fields...)}
	observed.Fields = append(observed.Fields, Field{Resource: "workload/worker", Path: "variables/MANUAL", Value: json.RawMessage(`"retained"`)})
	options := PlanOptions{Manager: "source", Revision: "revision", Generation: 1, Prune: true, Now: time.Now()}
	plan, err := BuildPlan(desired, observed, owners, options)
	if err != nil || !plan.CanApply() {
		t.Fatalf("retirement plan: %+v %v", plan, err)
	}
	actions := map[string]string{}
	for _, change := range plan.Changes {
		actions[(Field{Resource: change.Resource, Path: change.Path}).Key()] = change.Action
	}
	if actions["workload/worker#presence"] != "remove" || actions["workload/worker#variables/MODE"] != "keep" ||
		actions["workload/worker#variables/MANUAL"] != "retain_unmanaged" || actions["workload/api#presence"] != "keep" {
		t.Fatalf("retirement rewrote retained configuration or a neighbor: %+v", plan.Changes)
	}
	options.Overrides = []Override{{Resource: "workload/worker", Path: "variables/MODE", ExpiresAt: options.Now.Add(time.Hour)}}
	blocked, err := BuildPlan(desired, observed, owners, options)
	if err != nil || blocked.CanApply() {
		t.Fatalf("active override allowed whole-workload retirement: %+v %v", blocked, err)
	}
	options.Overrides = nil
	options.Prune = false
	blocked, err = BuildPlan(desired, observed, owners, options)
	if err != nil || blocked.CanApply() {
		t.Fatalf("retirement bypassed prune opt-in: %+v %v", blocked, err)
	}
}
