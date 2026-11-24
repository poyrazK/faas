package builderd

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// spec: §4.6
// ADR-20261124160000: the builder sends the plan's app layer cap, which the
// standalone build step measures the app against, and builds Next.js apps
// with standalone output only on the shared runner base.
func TestNextStandaloneBuildInputs(t *testing.T) {
	for plan, want := range map[string]int{
		string(api.PlanFree): 256,
		"no-such-plan":       0,
	} {
		if got := planAppLayerMaxMB(plan); got != want {
			t.Errorf("planAppLayerMaxMB(%q) = %d, want %d", plan, got, want)
		}
	}
	env := func(values map[string]string) func(string) string {
		return func(k string) string { return values[k] }
	}
	if !nextStandaloneBuild("node24", env(nil)) {
		t.Fatal("shared runtime build without the switch set")
	}
	if nextStandaloneBuild("", env(nil)) {
		t.Fatal("own runtime base")
	}
	if nextStandaloneBuild("node24", env(map[string]string{"FAAS_NEXT_STANDALONE": "0"})) {
		t.Fatal("switched off")
	}
}
