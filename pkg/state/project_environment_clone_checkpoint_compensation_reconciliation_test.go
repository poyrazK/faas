package state

import (
	"reflect"
	"testing"
)

func TestCheckpointCompensationReconciliationReportsUnverifiedOwnership(t *testing.T) {
	op := ProjectEnvironmentCloneOperation{ID: "operation", Revision: 4, SourceEnvironment: "production", SourceRevisionHash: "frozen", TargetEnvironment: "stage", TargetReleaseSetID: "owned-release", Resources: []ProjectEnvironmentCloneResource{
		{Kind: "data_checkpoint", Name: "production", Status: "compensated"},
		{Kind: "managed_postgres", Name: "owned-source", SourceID: "owned-source", Status: "compensated"},
		{Kind: "managed_postgres", Name: "foreign-source", SourceID: "foreign-source", Status: "compensated"},
		{Kind: "data_checkpoint_future", Name: "production", Status: "compensated"},
		{Kind: "workload", Name: "app", TargetID: "deployment", Status: "compensated"},
	}}
	in := ProjectEnvironmentCloneCheckpointCaptureRetirement{ObservationSHA256: "capture-proof", Plan: ProjectEnvironmentCloneCheckpointCaptureRetirementPlan{SourceCompensation: ProjectEnvironmentCloneCheckpointSourceCompensation{ObservationSHA256: "source-proof", Plan: ProjectEnvironmentCloneCheckpointSourceCompensationPlan{Capture: ProjectEnvironmentCloneCheckpointPlan{Sources: []ProjectEnvironmentCloneCheckpointSource{{Kind: "managed_postgres", ID: "owned-source"}}}}}}}
	records := []projectCloneWorkloadRecord{{ProjectEnvironmentCloneWorkload: ProjectEnvironmentCloneWorkload{AppID: "hidden-app", TargetDeploymentID: "hidden-deployment"}}}
	out := checkpointCompensationReconciliation(op, in, records)
	if out.OperationID != op.ID || out.Revision != op.Revision || out.ResourcesSHA256 != cloneCheckpointSHA(op.Resources) || out.SourceRevisionHash != op.SourceRevisionHash || out.SourceCompensationSHA256 != "source-proof" || out.CaptureRetirementSHA256 != "capture-proof" {
		t.Fatal("report lost frozen proof binding", out)
	}
	if len(out.Blockers) != 5 {
		t.Fatal("labels hid unverified ownership", out.Blockers)
	}
	if !reflect.DeepEqual(out, checkpointCompensationReconciliation(op, in, records)) {
		t.Fatal("report is not deterministic")
	}
	for _, r := range op.Resources {
		if r.Status != "compensated" {
			t.Fatal("reconciliation mutated inventory")
		}
	}
	// Neither checkpoint-prefixed future kinds nor a foreign data source are
	// covered by the current capture proof; both remain visible blockers.
	seen := map[string]bool{}
	for _, b := range out.Blockers {
		seen[b.Kind+":"+b.Name] = true
	}
	for _, key := range []string{"data_checkpoint_future:production", "managed_postgres:foreign-source", "workload:app", "workload:hidden-app", "release_set:stage"} {
		if !seen[key] {
			t.Fatal("missing blocker", key)
		}
	}
}
