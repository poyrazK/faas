package state

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// After activation the graph's candidates are live. Reconciling it again must
// keep it prepared: on n2 every pass tried prepared -> preparing, which the
// graph guard refuses, and serving never converged.
func TestActivatedCandidatesKeepTheGraphPrepared(t *testing.T) {
	for _, tc := range []struct {
		name       string
		candidates []EnvironmentWorkloadCandidate
		want       string
	}{
		{"snapshotting", []EnvironmentWorkloadCandidate{{Status: DeploySnapshotting, HasRootfs: true}}, "prepared"},
		{"activated", []EnvironmentWorkloadCandidate{{Status: DeployLive, HasRootfs: true}, {Status: DeployLive, HasRootfs: true}}, "prepared"},
		{"still building", []EnvironmentWorkloadCandidate{{Status: DeployLive, HasRootfs: true}, {Status: DeployBuilding}}, "preparing"},
		{"superseded", []EnvironmentWorkloadCandidate{{Status: DeploySuperseded, HasRootfs: true}}, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if phase, _ := preparationGraphPhase(tc.candidates); phase != tc.want {
				t.Fatalf("phase = %q, want %q", phase, tc.want)
			}
		})
	}
}

// Activation makes the candidate a live deployment of its own app, so inputs
// recomputed after activation inherit from it: on n2 every later GitOps pass
// refused the activated candidate ("stored candidate inputs differ from the
// reviewed intent") and serving never converged. The inherited baseline is
// carried over; every authority field still has to match.
func TestActivatedCandidateInputsIgnoreOnlyTheInheritedBaseline(t *testing.T) {
	enabled := true
	frozen := EnvironmentWorkloadRuntime{
		SourceID: "source", EnvironmentID: "environment", RevisionID: "revision",
		Generation: 1, IntentVersion: 1, Resource: "workload/api", PlanHash: strings.Repeat("b", 64),
		AppID: "app", Scope: "production", AppType: AppTypeApp, Runtime: map[string]json.RawMessage{},
		Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example.com/api@sha256:" + strings.Repeat("a", 64), FullRootfs: true},
	}
	stored, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	// After activation the candidate is the app's live deployment.
	recomputed := frozen
	recomputed.SourceDeployments = []string{"candidate"}
	recomputed.DeploymentInputs = &EnvironmentWorkloadDeploymentInputs{FullRootfs: &enabled}
	if frozenCandidateInputsMatch(stored, recomputed) {
		t.Fatal("fixture: the recomputed baseline should differ")
	}
	if !activatedCandidateInputsMatch(stored, recomputed) {
		t.Fatal("an activated candidate was refused for its own inherited baseline")
	}
	changed := recomputed
	changed.IntentVersion = 2
	if activatedCandidateInputsMatch(stored, changed) {
		t.Fatal("an activated candidate ignored a changed reviewed intent")
	}
	changed = recomputed
	changed.Source = &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example.com/api@sha256:" + strings.Repeat("c", 64), FullRootfs: true}
	if activatedCandidateInputsMatch(stored, changed) {
		t.Fatal("an activated candidate ignored a changed source")
	}
}
