package e2e_test

// The build e2e test must never give a build less time than the platform does.
//
// runBuildSubtest used a hardcoded 6-minute deadline while
// api.BuildTimeoutSeconds grants a build 900 s — and the constant's own
// comment says why: "cold rootless Railpack export needs headroom". A build
// that took 7 minutes was therefore within spec and still failed the test.
//
// On a cold acceptance node every go124 subtest died at exactly 360.00 s with
// the deployment still `building`, while the guest console showed buildkit
// healthy and pulling the Railpack frontend from ghcr.io. Nothing was broken
// except the test's own budget.
//
// Untagged deliberately: the test it guards is metal-only, but the invariant
// is arithmetic over two constants and belongs in ordinary CI, where it runs
// on every PR instead of only on hardware.

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildSubtestDeadlineIsNotTighterThanThePlatformBudget(t *testing.T) {
	src, err := os.ReadFile("build_metal_test.go")
	if err != nil {
		t.Fatalf("read build_metal_test.go: %v", err)
	}

	// A literal minute-valued context deadline in the build path is the shape
	// that caused this: it cannot track api.BuildTimeoutSeconds.
	literal := regexp.MustCompile(`context\.WithTimeout\(context\.Background\(\), (\d+)\s*\*\s*time\.Minute\)`)
	for _, m := range literal.FindAllSubmatch(src, -1) {
		mins, convErr := strconv.Atoi(string(m[1]))
		if convErr != nil {
			continue
		}
		if mins*60 < api.BuildTimeoutSeconds {
			t.Errorf("build_metal_test.go uses a hardcoded %d-minute deadline, but the "+
				"platform grants a build %d s (api.BuildTimeoutSeconds). A build inside "+
				"its own budget would fail this test. Derive the deadline from the "+
				"constant instead.", mins, api.BuildTimeoutSeconds)
		}
	}

	// And it must actually reference the constant, or the next edit reverts to
	// a literal without tripping the check above.
	if !regexp.MustCompile(`api\.BuildTimeoutSeconds`).Match(src) {
		t.Error("build_metal_test.go no longer derives its deadline from " +
			"api.BuildTimeoutSeconds; a hardcoded budget will drift from the platform's")
	}
}

// The poll must outlast the platform cap, so the BUILD's own timeout fires
// first and the failure arrives as a failed build with its log rather than as
// a bare test deadline with nothing to diagnose.
func TestBuildPollOutlastsThePlatformCap(t *testing.T) {
	src, err := os.ReadFile("build_metal_test.go")
	if err != nil {
		t.Fatalf("read build_metal_test.go: %v", err)
	}
	if !regexp.MustCompile(`buildPoll\s*:=\s*api\.BuildTimeoutSeconds\*time\.Second\s*\+`).Match(src) {
		t.Error("the build poll is not derived as api.BuildTimeoutSeconds + headroom; " +
			"a poll shorter than the platform cap reports a test deadline instead of a build failure")
	}
}
