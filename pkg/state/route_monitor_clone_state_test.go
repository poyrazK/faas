package state_test

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261028110044 amendment 6: route_monitors holds runtime state beside
// its configuration. A clone must start without the source's open incident,
// deployments, recovery state or schedule, so the table gains clone
// capabilities only once those columns are no longer copied with it.
func TestRouteMonitorCloneCopiesNoRuntimeState(t *testing.T) {
	policies, err := state.ProjectEnvironmentCloneSchemaPolicies()
	if err != nil {
		t.Fatal(err)
	}
	runtime := []string{"active_incident_id", "last_deployment_id", "last_healthy_deployment", "customer_recovery_state", "next_check_at"}
	for _, policy := range policies {
		if policy.TableName != "route_monitors" {
			continue
		}
		if policy.Capabilities == nil || len(*policy.Capabilities) == 0 {
			return
		}
		for _, column := range runtime {
			if slices.Contains(policy.Columns, column) {
				t.Fatalf("route_monitors gained clone capabilities %v while it still copies runtime column %s", *policy.Capabilities, column)
			}
		}
		return
	}
	t.Fatal("route_monitors is missing from the clone registry")
}
