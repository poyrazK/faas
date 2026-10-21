package conformance

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Environment capture inventories platform evidence without transferring its
// incident history or live image-preparation clocks into the cloned intent.
func TestOperatorCloneSchemaExcludesRuntimeEvidence(t *testing.T) {
	policies, err := state.ProjectEnvironmentCloneSchemaPolicies()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"operator_alert_collection":          false,
		"operator_alert_collection_outcomes": false,
		"operator_alert_episodes":            false,
		"operator_alert_retention_health":    false,
		"operator_alert_rule_observations":   false,
		"deployment_image_preparations":      false,
	}
	for _, policy := range policies {
		if _, ok := want[policy.TableName]; !ok {
			continue
		}
		want[policy.TableName] = true
		if policy.Kind != state.CloneSchemaOperational {
			t.Errorf("%s = %s; runtime evidence must be operational", policy.TableName, policy.Kind)
		}
		if policy.TableName == "deployment_image_preparations" && !slices.Contains(policy.Columns, "phase_started_at") {
			t.Error("image phase clock is absent from the operational inventory")
		}
	}
	for table, found := range want {
		if !found {
			t.Errorf("operator evidence table %s is absent from the clone inventory", table)
		}
	}
}
