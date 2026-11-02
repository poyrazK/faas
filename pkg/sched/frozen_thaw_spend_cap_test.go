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

// The ticketed thaw (ADR-1740) skipped the spend cap the ordinary thaw honours,
// which mattered once production wakes took the fast path (ADR
// 20261031190030). At the cap the ticket is kept and the ordinary gate
// refuses; under it the ticket thaws.
func TestTicketedThawHonoursTheSpendCap(t *testing.T) {
	for _, coordinated := range []bool{false, true} {
		name := "wake rpc"
		if coordinated {
			name = "coordinated"
		}
		t.Run(name, func(t *testing.T) {
			f, ops := frozenWithTicket(t)
			e := *f.E()
			var reached atomic.Bool
			e.WithOverageChecker(switchableSpendCap(&reached))
			reached.Store(true)
			wake := func() (string, error) {
				if coordinated {
					out, err := e.EnsureWake(t.Context(), f.App().ID, sched.TriggerGateway)
					if out.Instance == nil {
						return "", err
					}
					return out.Instance.InstanceID, err
				}
				result, err := e.Wake(t.Context(), f.App().ID, "", "", "")
				return result.InstanceID, err
			}
			_, _ = wake()
			if thaws := *(*f.Vmm()).Thaws(); thaws != 0 {
				t.Fatalf("a wake at the spend cap thawed %d frozen VM(s)", thaws)
			}
			if got := thawOutcomes(ops); got["fast"] != 0 {
				t.Fatalf("thaw outcomes at the cap = %v, want no fast thaw", got)
			}
			reached.Store(false)
			id, err := wake()
			if err != nil || id != f.Ins().ID {
				t.Fatalf("wake under the cap: %q, %v", id, err)
			}
			if got := thawOutcomes(ops); got["fast"] != 1 {
				t.Fatalf("thaw outcomes under the cap = %v, want the ticketed thaw", got)
			}
		})
	}
}
