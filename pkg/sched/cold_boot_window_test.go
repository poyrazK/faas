package sched_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// The startup deadline a deploy accepts (api.MaxAppManifestStartupDeadlineS,
// every plan's DefaultStartupDeadlineS) may not exceed what the watchdog lets
// any cold boot take, or an app deploys and then never cold boots again
// (Hunt 12 H12-10, ADR-138 amendment).
func TestStartupDeadlinesFitTheColdBootWindow(t *testing.T) {
	window := time.Duration(api.ColdBootReadyWindowS) * time.Second
	if sched.ColdBootSweepBudget != window {
		t.Fatalf("ColdBootSweepBudget = %v, api.ColdBootReadyWindowS = %v", sched.ColdBootSweepBudget, window)
	}
	if got := time.Duration(api.MaxAppManifestStartupDeadlineS) * time.Second; got > window {
		t.Fatalf("MaxAppManifestStartupDeadlineS = %v exceeds the cold-boot window %v", got, window)
	}
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale} {
		if got := time.Duration(api.MustLimitsFor(plan).DefaultStartupDeadlineS) * time.Second; got > window {
			t.Errorf("%s DefaultStartupDeadlineS = %v exceeds the cold-boot window %v", plan, got, window)
		}
	}
}
