//go:build linux

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestNormalizeWorkloadDependencies_AddsInitPrerequisites(t *testing.T) {
	roster := workloadRoster{
		Main: workloadSpec{Name: "main", Type: "main"},
		Sidecars: []workloadSpec{
			{Name: "migrate", Type: "init"},
			{Name: "metrics", Type: "sidecar"},
		},
	}
	deps, err := normalizeWorkloadDependencies(roster)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(deps["main"]) != 1 || deps["main"][0].Name != "migrate" || deps["main"][0].Condition != api.WorkloadDependencyCompletedSuccessfully {
		t.Fatalf("main deps = %#v, want migrate/completed_successfully", deps["main"])
	}
	if len(deps["metrics"]) != 1 || deps["metrics"][0].Name != "migrate" {
		t.Fatalf("metrics deps = %#v, want migrate prerequisite", deps["metrics"])
	}
	order, err := workloadStartOrder(roster, deps)
	if err != nil {
		t.Fatalf("start order: %v", err)
	}
	if strings.Join(order, ",") != "migrate,main,metrics" {
		t.Fatalf("start order = %v, want migrate,main,metrics", order)
	}
}

func TestNormalizeWorkloadDependencies_RejectsCycle(t *testing.T) {
	roster := workloadRoster{
		Main: workloadSpec{Name: "main", Type: "main"},
		Sidecars: []workloadSpec{
			{Name: "migrate", Type: "init", DependsOn: []api.WorkloadDependency{{Name: "metrics"}}},
			{Name: "metrics", Type: "sidecar", DependsOn: []api.WorkloadDependency{{Name: "migrate"}}},
		},
	}
	_, err := normalizeWorkloadDependencies(roster)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("normalize error = %v, want cycle", err)
	}
}

func TestWaitForWorkloadDependency_Conditions(t *testing.T) {
	state := newWorkloadDependencyState()
	close(state.started)
	close(state.healthy)
	close(state.completedSuccessfully)
	for _, condition := range []api.WorkloadDependencyCondition{
		api.WorkloadDependencyStarted,
		api.WorkloadDependencyHealthy,
		api.WorkloadDependencyCompletedSuccessfully,
	} {
		if err := waitForWorkloadDependency(context.Background(), api.WorkloadDependency{Name: "x", Condition: condition}, state); err != nil {
			t.Errorf("condition %q: %v", condition, err)
		}
	}
}

func TestWaitForWorkloadDependency_CompletedSuccessAfterDone(t *testing.T) {
	state := newWorkloadDependencyState()
	state.setResult(nil)
	if err := waitForWorkloadDependency(context.Background(), api.WorkloadDependency{Name: "x", Condition: api.WorkloadDependencyCompletedSuccessfully}, state); err != nil {
		t.Fatalf("completed_successfully = %v, want nil", err)
	}
}

func TestSupervisorLifecycleHooksFireOnce(t *testing.T) {
	s := &Supervisor{}
	started, healthy := 0, 0
	s.onStart = func() { started++ }
	s.onHealthy = func() { healthy++ }
	s.markStarted()
	s.markStarted()
	s.markHealthy()
	s.markHealthy()
	if started != 1 || healthy != 1 {
		t.Fatalf("hooks = started:%d healthy:%d, want once", started, healthy)
	}
}

func TestNewSupervisorFor_InitNeverRestarts(t *testing.T) {
	sup := newSupervisorFor(workloadSpec{Name: "migrate", Type: "init", Essential: true}, nil, nil, nil, nil)
	if sup.Max != 0 {
		t.Fatalf("init Max = %d, want 0", sup.Max)
	}
}

func TestRunStartupHealthcheck(t *testing.T) {
	ok := api.AppManifest{Entrypoint: []string{"/bin/true"}, Healthcheck: &api.AppManifestHealthcheck{Test: []string{"CMD", "/bin/true"}, TimeoutS: 1}}
	if err := runStartupHealthcheck(ok, nil, "", "", 0, nil, nil); err != nil {
		t.Fatalf("successful healthcheck = %v", err)
	}
	fail := api.AppManifest{Entrypoint: []string{"/bin/true"}, Healthcheck: &api.AppManifestHealthcheck{Test: []string{"CMD", "/bin/false"}, TimeoutS: 1}}
	if err := runStartupHealthcheck(fail, nil, "", "", 0, nil, nil); err == nil {
		t.Fatal("failing healthcheck returned nil")
	}
}
