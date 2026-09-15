package e2e_test

// A metal test must not ask for more apps than the plan it seeds allows.
//
// TestCpuFairnessMetal seeded Hobby (DeployedApps: 5) and then created 5 quiet
// + 1 hot = 6 apps, so the sixth returned
//
//	create app hot-0: status=403
//
// and the test failed in 3.66 s having measured nothing. It would fail that way
// on any node, and the 403 looked like an auth problem rather than a plan
// limit.
//
// Untagged deliberately: the test it guards is metal-only, but this is
// arithmetic over two constants and belongs in ordinary CI, where it runs on
// every PR instead of only on hardware.

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCpuFairnessSeedsAPlanThatFitsItsApps(t *testing.T) {
	src, err := os.ReadFile("cpu_fairness_test.go")
	if err != nil {
		t.Fatalf("read cpu_fairness_test.go: %v", err)
	}

	countOf := func(name string) int {
		m := regexp.MustCompile(name + `\s*=\s*(\d+)`).FindSubmatch(src)
		if m == nil {
			t.Fatalf("could not find %s", name)
		}
		n, convErr := strconv.Atoi(string(m[1]))
		if convErr != nil {
			t.Fatalf("parse %s: %v", name, convErr)
		}
		return n
	}
	need := countOf("cpuFairnessQuietCount") + countOf("cpuFairnessHotCount")

	m := regexp.MustCompile(`plan\s*:=\s*api\.(Plan[A-Za-z]+)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("cpu_fairness_test.go no longer selects its plan via `plan := api.Plan...`; " +
			"a hardcoded SeedAccount plan can silently stop fitting the app count")
	}
	planName := string(m[1])

	plans := map[string]api.Plan{
		"PlanFree":  api.PlanFree,
		"PlanHobby": api.PlanHobby,
		"PlanPro":   api.PlanPro,
		"PlanScale": api.PlanScale,
	}
	plan, ok := plans[planName]
	if !ok {
		t.Fatalf("unrecognised plan %s", planName)
	}
	limits, ok := api.LimitsFor(plan)
	if !ok {
		t.Fatalf("no limits for %s", planName)
	}
	if limits.DeployedApps < need {
		t.Errorf("cpu_fairness seeds %s (DeployedApps=%d) but creates %d apps; "+
			"the %dth create returns 403 and the test measures nothing",
			planName, limits.DeployedApps, need, limits.DeployedApps+1)
	}
}
