// adr: 20261111114059
package neon

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// An agent's budget prices an always-on database at its class's smallest
// compute; the table in pkg/api must match the profiles Neon provisions.
func TestAgentBudgetMinimumComputeMatchesProfiles(t *testing.T) {
	if len(api.ManagedPostgresMinimumQuarterComputeUnits) != len(profiles) {
		t.Fatalf("budget table has %d classes, Neon profiles %d", len(api.ManagedPostgresMinimumQuarterComputeUnits), len(profiles))
	}
	for class, profile := range profiles {
		got, ok := api.ManagedPostgresMinimumQuarterComputeUnits[string(class)]
		if want := int64(profile.minimumCU * 4); !ok || got != want || float64(want) != profile.minimumCU*4 {
			t.Errorf("class %s: budget minimum %d quarter CU, Neon minimum %.2f CU", class, got, profile.minimumCU)
		}
	}
}
