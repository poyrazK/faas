// adr: 20261031193635
package main_test

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Slice 6b: a rehearsal workload may come from a simulated branch, only
// under contained actions and never beside its own source.
func TestScenarioFromBranchValidation(t *testing.T) {
	for _, tc := range []struct {
		name, manifest, want string
	}{
		{"contained primary", `
project: shop
simulation: ["true"]
actions: {network: contained, webhooks: capture}
from_branch: {project: shop, environment: sim-1, workload: web}
`, ""},
		{"contained service", `
project: shop
simulation: ["true"]
actions: {network: contained, webhooks: capture}
services:
  api: {from_branch: {project: shop, environment: sim-1, workload: api}}
`, ""},
		{"no containment", `
project: shop
simulation: ["true"]
from_branch: {project: shop, environment: sim-1, workload: web}
`, "actions.network: contained"},
		{"source and branch", `
project: shop
simulation: ["true"]
actions: {network: contained, webhooks: capture}
services:
  api: {source: ./api, from_branch: {project: shop, environment: sim-1, workload: api}}
`, "use one"},
		{"bad environment", `
project: shop
simulation: ["true"]
actions: {network: contained, webhooks: capture}
from_branch: {project: shop, environment: "Not A Slug", workload: web}
`, "valid project, environment and workload"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var scenario main.TestScenario
			if err := yaml.Unmarshal([]byte(tc.manifest), &scenario); err != nil {
				t.Fatal(err)
			}
			_, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{"rehearse": scenario}, t.TempDir(), "rehearse", "real-vm")
			if tc.want == "" {
				if err != nil && strings.Contains(err.Error(), "from_branch") {
					t.Fatalf("valid from_branch refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validation = %v, want %q", err, tc.want)
			}
		})
	}
}
