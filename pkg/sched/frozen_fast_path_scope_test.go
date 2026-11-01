// adr: 1740, 20261031190030 — a production-lane wake takes the fast path.
package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
)

// The gateway names scope "production" for every project app, and the fast
// path refused every non-empty scope: in production 258 of 283 thaws in a day
// skipped it with reason "scope". A ticket covers the production lane, so a
// wake for production, default or no scope takes it; another scope does not.
func TestProductionScopedWakeTakesTheFastPath(t *testing.T) {
	for _, scope := range []string{"production", "default", ""} {
		t.Run("coordinated "+scope, func(t *testing.T) {
			f, ops := frozenWithTicket(t)
			out, err := (*f.E()).EnsureWakeForDeployment(t.Context(), (*f.App()).ID, "", scope, sched.TriggerGateway)
			if err != nil || out.Instance == nil || out.Instance.InstanceID != (*f.Ins()).ID {
				t.Fatalf("EnsureWakeForDeployment(scope %q): %+v, %v", scope, out.Instance, err)
			}
			if got := thawOutcomes(ops); got["fast"] != 1 || got["success"] != 0 {
				t.Fatalf("thaw outcomes = %v, want one fast thaw", got)
			}
			if got := fastPathSkips(ops); len(got) != 0 {
				t.Fatalf("fast path skips = %v, want none", got)
			}
		})
		t.Run("wake rpc "+scope, func(t *testing.T) {
			f, ops := frozenWithTicket(t)
			result, err := (*f.E()).Wake(t.Context(), (*f.App()).ID, "", scope, "")
			if err != nil || result.InstanceID != (*f.Ins()).ID {
				t.Fatalf("Wake(scope %q): %+v, %v", scope, result, err)
			}
			if got := thawOutcomes(ops); got["fast"] != 1 {
				t.Fatalf("thaw outcomes = %v, want one fast thaw", got)
			}
		})
	}
	t.Run("another scope", func(t *testing.T) {
		f, ops := frozenWithTicket(t)
		// The ordinary path decides a staging wake; only the skip is under test.
		_, _ = (*f.E()).Wake(t.Context(), (*f.App()).ID, "", "staging", "")
		if got := thawOutcomes(ops); got["fast"] != 0 {
			t.Fatalf("a staging wake took the production ticket: %v", got)
		}
		if got := fastPathSkips(ops); got["scope"] != 1 {
			t.Fatalf("fast path skips = %v, want one scope skip", got)
		}
	})
}
