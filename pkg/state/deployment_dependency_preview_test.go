//go:build !no_pg

// adr: 685
package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// PreviewDeploymentDependencies lets imaged stop a candidate whose pinned
// dependency already failed before build/scan/snapshot work, without
// starting the gate's deadline or writing progress the activation check
// owns.
func TestPreviewDeploymentDependenciesIsReadOnly(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newDependencyGateFixture(t, backend, state.DeployImaging)
			preview := f.store.(state.DeploymentDependencyPreviewStore)
			now := time.Now().UTC()

			assertDependencyGateError(t, preview.PreviewDeploymentDependencies(f.ctx, f.candidate.ID, now), state.CodeDependencyNotReady)
			// A pending preview far past the gate timeout is still only
			// pending: the deadline starts at activation, not here.
			assertDependencyGateError(t, preview.PreviewDeploymentDependencies(f.ctx, f.candidate.ID, now.Add(24*time.Hour)), state.CodeDependencyNotReady)
			assertNoGateProgress(t, f)

			if _, err := f.store.SetDeploymentFailed(f.ctx, f.dependency.ID, "app_startup_timeout", "cold_boot_timeout"); err != nil {
				t.Fatal(err)
			}
			assertDependencyGateError(t, preview.PreviewDeploymentDependencies(f.ctx, f.candidate.ID, now), state.CodeDependencyFailed)
			assertNoGateProgress(t, f)
			candidate, err := f.store.DeploymentByID(f.ctx, f.candidate.ID)
			if err != nil || candidate.Status != state.DeployImaging {
				t.Fatalf("preview changed the candidate: %+v, %v", candidate, err)
			}
		})
	}
}

func assertNoGateProgress(t *testing.T, f dependencyGateFixture) {
	t.Helper()
	dep, err := f.store.DeploymentByID(f.ctx, f.candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stages state.StageState
	if len(dep.StageState) > 0 {
		if err := json.Unmarshal(dep.StageState, &stages); err != nil {
			t.Fatal(err)
		}
	}
	if stages.DependencyGate != nil && (stages.DependencyGate.StartedAt != nil || stages.DependencyGate.Status == "failed") {
		t.Fatalf("preview wrote gate progress: %s", dep.StageState)
	}
}
