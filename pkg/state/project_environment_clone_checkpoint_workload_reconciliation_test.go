package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestCheckpointClosedWorkloadResourcesRequireFrozenIdentity(t *testing.T) {
	w := state.ProjectEnvironmentCloneWorkload{AppID: "app-id", WorkloadSlug: "app", SourceDeploymentID: "source-deployment", TargetDeploymentID: "target-deployment", SourceHash: "artifact", SourceSettingsHash: "settings", SourceValuesHash: "values", SourcePoliciesHash: "policies", SourceProjectConfigHash: "project"}
	records := []state.ProjectCloneWorkloadRecord{{ProjectEnvironmentCloneWorkload: w}}
	op := state.ProjectEnvironmentCloneOperation{SourceEnvironment: "production", SourceRevisionHash: "revision"}
	for _, r := range []state.ProjectEnvironmentCloneResource{
		{Kind: "source_revision", Name: "production", SourceVersion: "revision"},
		{Kind: "project_config", Name: "production", SourceVersion: "project"},
		{Kind: "workload", Name: "app", SourceID: "source-deployment", TargetID: "target-deployment", SourceVersion: "artifact"},
		{Kind: "variables", Name: "app", SourceID: "app-id", TargetID: "app-id", SourceVersion: "values"},
		{Kind: "secrets", Name: "app", SourceID: "app-id", TargetID: "app-id", SourceVersion: "values"},
		{Kind: "workload_settings", Name: "app", SourceVersion: "settings"},
		{Kind: "route_policy", Name: "app", SourceVersion: "policies"},
		{Kind: "edge_policy", Name: "app", SourceVersion: "policies"},
	} {
		t.Run(r.Kind, func(t *testing.T) {
			for _, status := range []string{"captured", "ready", "compensated"} {
				owned := r
				owned.Status = status
				if !state.CheckpointClosedWorkloadResource(op, records, owned) {
					t.Fatal("original entry not recognized", owned)
				}
			}
			for _, field := range []string{"kind", "name", "source", "target", "version", "capture"} {
				bad := r
				switch field {
				case "kind":
					bad.Kind = "future"
				case "name":
					bad.Name = "foreign"
				case "source":
					bad.SourceID = "foreign"
				case "target":
					bad.TargetID = "foreign"
				case "version":
					bad.SourceVersion = "foreign"
				case "capture":
					bad.CapturePoint = "unowned"
				}
				if state.CheckpointClosedWorkloadResource(op, records, bad) {
					t.Fatal("foreign entry recognized", field, bad)
				}
			}
			bad := r
			bad.SourceVersion = "foreign"
			op.Resources = []state.ProjectEnvironmentCloneResource{r, bad}
			b := state.ProjectEnvironmentCloneCheckpointCompensationBlocker{Kind: r.Kind, Name: r.Name, TargetID: r.TargetID}
			if state.CheckpointClosedWorkloadBlocker(op, records, b) {
				t.Fatal("duplicate labels hid unowned version")
			}
		})
	}
}
