// adr: 1420 — proven worker deployments characterize briefly.
package fcvm

import (
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProvenWorkerBootArgOnlyForDeclaredWorkers(t *testing.T) {
	tests := []struct {
		name   string
		mode   string
		proven bool
		want   bool
	}{
		{name: "proven worker", mode: api.ExecutionModeWorker, proven: true, want: true},
		{name: "unproven worker", mode: api.ExecutionModeWorker},
		{name: "proven flag on a request app", mode: api.ExecutionModeRequest, proven: true},
		{name: "proven flag on a job", mode: api.ExecutionModeJob, proven: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validColdSpec()
			spec.ExecutionMode, spec.ProvenWorker = tt.mode, tt.proven
			args := strings.Fields(BuildColdBootConfig(spec, 0).BootSource.BootArgs)
			if got := slices.Contains(args, api.ProvenWorkerBootArg); got != tt.want {
				t.Fatalf("boot args %q contain %s = %v, want %v", args, api.ProvenWorkerBootArg, got, tt.want)
			}
		})
	}
}

// A deployment is proven only by a cold boot that characterized as a worker,
// and a failed cold boot sends the next one back through the full window.
func TestManagerProvenWorkerDeployments(t *testing.T) {
	m := &Manager{}
	worker := WakeRequest{ExecutionMode: api.ExecutionModeWorker, DeploymentID: "dep-1"}
	if m.workerProven(worker) {
		t.Fatal("a deployment no boot characterized is proven")
	}
	booted := func(method WakeMethod, mode, class string) *Instance {
		return &Instance{Method: method, ExecutionMode: mode, DeploymentID: "dep-1",
			Characterization: api.CharacterizationReport{ObservedClass: class}}
	}
	for name, inst := range map[string]*Instance{
		"restore":         booted(WakeRestore, api.ExecutionModeWorker, api.ExecutionModeWorker),
		"request mode":    booted(WakeColdBoot, api.ExecutionModeRequest, api.ExecutionModeWorker),
		"no worker class": booted(WakeColdBoot, api.ExecutionModeWorker, ""),
	} {
		m.recordProvenWorkerLocked(inst)
		if m.workerProven(worker) {
			t.Fatalf("%s boot proved the deployment", name)
		}
	}
	m.recordProvenWorkerLocked(booted(WakeColdBoot, api.ExecutionModeWorker, api.ExecutionModeWorker))
	if !m.workerProven(worker) {
		t.Fatal("a cold boot characterized as a worker did not prove its deployment")
	}
	if m.workerProven(WakeRequest{ExecutionMode: api.ExecutionModeJob, DeploymentID: "dep-1"}) {
		t.Fatal("a job wake of a proven deployment used the short window")
	}
	m.forgetProvenWorker("dep-1")
	if m.workerProven(worker) {
		t.Fatal("a failed cold boot left the deployment proven")
	}
}
