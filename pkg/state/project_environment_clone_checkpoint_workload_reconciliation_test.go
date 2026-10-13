package state

import "testing"

func TestCheckpointClosedWorkloadResourcesRequireFrozenIdentity(t *testing.T) {
	w := ProjectEnvironmentCloneWorkload{AppID: "app-id", WorkloadSlug: "app", SourceDeploymentID: "source-deployment", TargetDeploymentID: "target-deployment", SourceHash: "artifact", SourceSettingsHash: "settings", SourceValuesHash: "values", SourcePoliciesHash: "policies", SourceProjectConfigHash: "project"}
	records := []projectCloneWorkloadRecord{{ProjectEnvironmentCloneWorkload: w}}
	op := ProjectEnvironmentCloneOperation{SourceEnvironment: "production", SourceRevisionHash: "revision"}
	for _, r := range []ProjectEnvironmentCloneResource{
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
				if !checkpointClosedWorkloadResource(op, records, owned) {
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
				if checkpointClosedWorkloadResource(op, records, bad) {
					t.Fatal("foreign entry recognized", field, bad)
				}
			}
			bad := r
			bad.SourceVersion = "foreign"
			op.Resources = []ProjectEnvironmentCloneResource{r, bad}
			b := ProjectEnvironmentCloneCheckpointCompensationBlocker{Kind: r.Kind, Name: r.Name, TargetID: r.TargetID}
			if checkpointClosedWorkloadBlocker(op, records, b) {
				t.Fatal("duplicate labels hid unowned version")
			}
		})
	}
}
