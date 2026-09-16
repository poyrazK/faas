package e2e_test

// The build e2e test must never give a build less time than the platform
// does — and must never give a WEDGED build more time than it needs to be
// diagnosed.
//
// This file has pinned each half of that in turn, and each time the other
// half bit:
//
//   - runBuildSubtest used a hardcoded 6-minute deadline while
//     api.BuildTimeoutSeconds grants a build 900 s. On a cold acceptance node
//     every go124 subtest died at exactly 360.00 s with the deployment still
//     `building`, while the guest console showed buildkit healthy and pulling
//     the Railpack frontend. Nothing was broken except the test's budget.
//   - #2624 fixed that by deriving the poll from api.BuildTimeoutSeconds plus
//     headroom, and this file guarded the derivation. Then #2694 spread the
//     same 16-minute poll across every source deploy — and nine wedged builds
//     pinned phase 2 at its 60-minute ceiling, each reporting nothing but
//     "deadline reached". A clock that is long enough for a healthy cold
//     build is far too long for a wedged one.
//
// The resolution is not a better number. It is to wait on PROGRESS: a healthy
// build writes output continuously, a wedged one goes silent, and
// e2etest.WaitForSourceDeployment fails within DefaultBuildStallWindow of the
// last change with the log tail attached, while keeping the platform budget
// only as a ceiling. These guards pin that shape.
//
// Untagged deliberately: the test they guard is metal-only, but the shape is
// visible in its source and belongs in ordinary CI, where it runs on every PR
// instead of only on hardware.

import (
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

func readBuildMetalSource(t *testing.T) []byte {
	t.Helper()
	src, err := os.ReadFile("build_metal_test.go")
	if err != nil {
		t.Fatalf("read build_metal_test.go: %v", err)
	}
	return src
}

// A literal minute-valued context deadline in the build path cannot track
// the platform budget; it is the shape that failed builds inside their own
// budget.
func TestBuildSubtestHasNoHardcodedDeadlineTighterThanThePlatformBudget(t *testing.T) {
	src := readBuildMetalSource(t)

	literal := regexp.MustCompile(`context\.WithTimeout\(context\.Background\(\), (\d+)\s*\*\s*time\.Minute\)`)
	for _, m := range literal.FindAllSubmatch(src, -1) {
		mins, convErr := strconv.Atoi(string(m[1]))
		if convErr != nil {
			continue
		}
		if mins*60 < api.BuildTimeoutSeconds {
			t.Errorf("build_metal_test.go uses a hardcoded %d-minute deadline, but the "+
				"platform grants a build %d s (api.BuildTimeoutSeconds). A build inside "+
				"its own budget would fail this test.", mins, api.BuildTimeoutSeconds)
		}
	}
}

// The build path must wait on progress, not on a clock. A clock-based wait
// on either row is the shape that sat sixteen minutes on every wedge.
func TestBuildSubtestWaitsOnProgressNotAClock(t *testing.T) {
	src := readBuildMetalSource(t)

	if !regexp.MustCompile(`e2etest\.WaitForSourceDeployment\(`).Match(src) {
		t.Error("build_metal_test.go does not use e2etest.WaitForSourceDeployment; " +
			"a wedged build would burn a fixed deadline and report nothing about where it stopped")
	}
	for _, clockWait := range []string{
		`e2etest\.WaitForBuildStatus\(`,
		`e2etest\.WaitForDeploymentLive\(`,
	} {
		if regexp.MustCompile(clockWait).Match(src) {
			t.Errorf("build_metal_test.go still uses the clock-based %s on the build path; "+
				"it waits a fixed deadline on a wedge instead of failing within "+
				"DefaultBuildStallWindow with the log tail", clockWait)
		}
	}
}

// The ceiling the progress wait keeps as a backstop is the platform's own
// in-guest budget — never a smaller constant that would drift from it.
func TestBuildCeilingIsThePlatformBudget(t *testing.T) {
	if want := time.Duration(api.BuildTimeoutSeconds) * time.Second; e2etest.DefaultBuildCeiling != want {
		t.Errorf("e2etest.DefaultBuildCeiling = %s, want api.BuildTimeoutSeconds = %s; "+
			"a smaller ceiling fails builds the platform would have allowed",
			e2etest.DefaultBuildCeiling, want)
	}
}
