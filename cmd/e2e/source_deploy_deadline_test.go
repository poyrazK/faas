package e2e_test

// How long a SOURCE deployment may take to reach live.
//
// A source deploy runs a real build inside a builder microVM. The platform's
// own cap on that is api.BuildTimeoutSeconds (900s — "cold rootless Railpack
// export needs headroom"), so any test deadline shorter than it is a deadline
// on the harness rather than on the product.
//
// These waits were 60s–4m, calibrated back when deployments failed within
// seconds for reasons that had nothing to do with building: a dead imaged, a
// rejected base, an account out of app slots. Once those were fixed and builds
// actually ran, gate run 35039361732 failed them all the same way —
//
//	deployment did not reach live: deadline 2m0s reached before deployment
//	<id> reached live (last status=building)
//
// — a test giving up on a build that was still legitimately running. Phase 2
// took ~35 minutes for six builds, so roughly six minutes each; a 2-minute
// deadline could never have covered one.
//
// Derive it from the platform cap, so the BUILD's own timeout fires first and
// the failure arrives as a failed build with its log attached rather than as a
// bare test deadline with nothing to diagnose. That is the reasoning #2624
// applied to TestBuildMetal's context; these are the waits inside it.

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// sourceDeployLiveDeadline outlasts the platform's build cap.
func sourceDeployLiveDeadline() time.Duration {
	return api.BuildTimeoutSeconds*time.Second + time.Minute
}

// sourceDeployCtxTimeout bounds the whole subtest: the wait above, plus room
// for the park/wake assertions that follow it.
func sourceDeployCtxTimeout() time.Duration {
	return sourceDeployLiveDeadline() + 5*time.Minute
}

func TestSourceDeployLiveDeadlineOutlastsTheBuildCap(t *testing.T) {
	cap := api.BuildTimeoutSeconds * time.Second
	if got := sourceDeployLiveDeadline(); got <= cap {
		t.Errorf("source-deploy deadline %s does not outlast the platform build cap %s; "+
			"the test would give up on a build that is still legitimately running, and "+
			"report a bare deadline instead of the failed build and its log", got, cap)
	}
}

func TestSourceDeployCtxOutlastsItsOwnWait(t *testing.T) {
	if sourceDeployCtxTimeout() <= sourceDeployLiveDeadline() {
		t.Error("the enclosing context expires before the deployment wait it contains, " +
			"so the wait can never reach its own deadline")
	}
}

// The 2-minute deadline that failed every streaming subtest must not come back.
func TestSourceDeployDeadlineCoversAnObservedBuild(t *testing.T) {
	// Phase 2 of gate run 35039361732: ~35 minutes for six source builds.
	const observedPerBuild = 6 * time.Minute
	if got := sourceDeployLiveDeadline(); got < observedPerBuild {
		t.Errorf("deadline %s is under the ~%s a real build took on faas-acceptance-1",
			got, observedPerBuild)
	}
}
