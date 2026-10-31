// adr: 1151, 1162 — a thaw honours the account's spend cap.
package sched_test

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// Hunt 18: thawing a frozen VM ran before the wake's request gates, so it
// skipped the spend cap. Frozen time is not billed and a thaw starts billing
// again, so an account at its cap kept serving from frozen VMs until the
// reaper parked them. The thaw now leaves the wake to the ordinary gate.
func TestFrozenThawHonoursTheSpendCap(t *testing.T) {
	f := sched.NewFrozenFixture(t, true)
	e := *f.E()
	var reached atomic.Bool
	e.WithOverageChecker(switchableSpendCap(&reached))
	e.EnableFrozenTier()
	if handled, err := e.FreezeIdle(t.Context(), f.Ins().ID, 150); err != nil || !handled {
		t.Fatalf("FreezeIdle: %v, %v", handled, err)
	}
	reached.Store(true)
	_, err := e.Wake(t.Context(), f.App().ID, "", "", "")
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodeAdmissionRefused {
		t.Fatalf("Wake at the spend cap = %v, want the admission refusal", err)
	}
	if thaws := *(*f.Vmm()).Thaws(); thaws != 0 {
		t.Fatalf("a wake at the spend cap thawed %d frozen VM(s)", thaws)
	}

	reached.Store(false)
	result, err := e.Wake(t.Context(), f.App().ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake under the cap: %v", err)
	}
	if result.InstanceID != f.Ins().ID || *(*f.Vmm()).Thaws() != 1 {
		t.Fatalf("under the cap the frozen VM must thaw: result %+v, thaws %d", result, *(*f.Vmm()).Thaws())
	}
}
