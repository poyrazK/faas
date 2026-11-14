package sched_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// The startup deadline a deploy accepts may not exceed what every later cold
// boot of the app is allowed, or an app deploys and then never cold boots
// again (Hunt 12 H12-10, ADR-138 amendment). ADR-20261114120000 lets a
// plan's startup window exceed the spec 30 s window, so every cold boot of an
// app with a longer deadline gets an extended RPC budget and watchdog
// reprieve: the window each plan accepts must fit inside them, and the
// gateway's wake hold must be able to cover it.
func TestStartupDeadlinesFitTheColdBootWindow(t *testing.T) {
	window := time.Duration(api.ColdBootReadyWindowS) * time.Second
	if sched.ColdBootSweepBudget != window {
		t.Fatalf("ColdBootSweepBudget = %v, api.ColdBootReadyWindowS = %v", sched.ColdBootSweepBudget, window)
	}
	want := map[api.Plan]int{api.PlanFree: 30, api.PlanHobby: 30, api.PlanPro: 60, api.PlanScale: 120}
	for plan, wantMax := range want {
		limits := api.MustLimitsFor(plan)
		if limits.MaxStartupDeadlineS != wantMax {
			t.Errorf("%s MaxStartupDeadlineS = %d, want %d", plan, limits.MaxStartupDeadlineS, wantMax)
		}
		if limits.DefaultStartupDeadlineS > limits.MaxStartupDeadlineS || limits.DefaultStartupDeadlineS > api.ColdBootReadyWindowS {
			t.Errorf("%s DefaultStartupDeadlineS = %d, want within the spec window and the plan cap %d", plan, limits.DefaultStartupDeadlineS, limits.MaxStartupDeadlineS)
		}
		if limits.MaxStartupDeadlineS > api.MaxAppManifestStartupDeadlineS {
			t.Errorf("%s MaxStartupDeadlineS = %d exceeds the gross cap %d", plan, limits.MaxStartupDeadlineS, api.MaxAppManifestStartupDeadlineS)
		}
		deadline := time.Duration(limits.MaxStartupDeadlineS) * time.Second
		// vmmd starts the readiness clock after the guest boots, so each
		// budget must cover the deadline plus boot time, beyond the spec window.
		if got := sched.ColdBootTimeout + sched.PrimeStartupExtension(int32(limits.MaxStartupDeadlineS)); got < deadline+5*time.Second {
			t.Errorf("%s cold-boot RPC budget %v does not cover its %v startup window", plan, got, deadline)
		}
		if got := sched.ColdBootSweepBudget + sched.PrimeStartupExtension(int32(limits.MaxStartupDeadlineS)); got < deadline {
			t.Errorf("%s watchdog budget %v does not cover its %v startup window", plan, got, deadline)
		}
	}
	longest := time.Duration(api.MaxAppManifestStartupDeadlineS) * time.Second
	if sched.DefaultWakeLeaderBudget < sched.ColdBootTimeout+sched.PrimeStartupExtension(int32(api.MaxAppManifestStartupDeadlineS)) {
		t.Fatalf("DefaultWakeLeaderBudget %v is shorter than the longest cold boot", sched.DefaultWakeLeaderBudget)
	}
	if got := time.Duration(api.WakeQueueMaxWaitSeconds) * time.Second; got < longest {
		t.Fatalf("WakeQueueMaxWaitSeconds %v cannot hold a request through the longest startup window %v", got, longest)
	}
}
