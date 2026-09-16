package e2e_test

// Source deploys wait on PROGRESS, not a clock — see pkg/e2etest/buildprogress.go.
// The only clock left is the enclosing context, which must outlast the build
// ceiling plus the park/wake assertions that follow it.

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/e2etest"
)

func sourceDeployCtxTimeout() time.Duration {
	return e2etest.DefaultBuildCeiling + 5*time.Minute
}

func TestSourceDeployCtxOutlastsTheBuildCeiling(t *testing.T) {
	if sourceDeployCtxTimeout() <= e2etest.DefaultBuildCeiling {
		t.Error("the enclosing context expires before the build ceiling, so the " +
			"progress wait can never report the ceiling itself")
	}
}
