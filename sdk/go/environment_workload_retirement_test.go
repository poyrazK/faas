package faas_test

import (
	"encoding/json"
	"testing"

	"github.com/poyrazK/faas/sdk/go/preview"
)

func TestEnvironmentWorkloadRecoveryKeepsReviewedOriginalIdentity(t *testing.T) {
	const original = "11111111-2222-4333-8444-555555555555"
	var definition preview.EnvironmentDefinition
	if err := json.Unmarshal([]byte(`{"api_version":"gregale.dev/environment/v1","project":"shop","environment":"staging","workload_pruning_policy":"retain","workload_recoveries":{"worker":"`+original+`"},"workloads":{"worker":{"app":"orders-worker"}}}`), &definition); err != nil {
		t.Fatal(err)
	}
	if definition.WorkloadPruningPolicy != "retain" || definition.WorkloadRecoveries["worker"] != original {
		t.Fatal("SDK lost reviewed retirement/recovery authority")
	}
	var status preview.EnvironmentGitOpsStatusResponse
	if err := json.Unmarshal([]byte(`{"workload_retirements":[{"id":"`+original+`","resource":"workload/worker","generation":3,"phase":"recovering","recovery_generation":4}]}`), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.WorkloadRetirements) != 1 || status.WorkloadRetirements[0].ID != original || status.WorkloadRetirements[0].Generation != 3 || status.WorkloadRetirements[0].RecoveryGeneration != 4 {
		t.Fatal("SDK lost original retirement generation")
	}
}
