// adr: 20261027163626 — phase timing for guarded traffic policy writes.
package state

import (
	"sync/atomic"
	"time"
)

// Writers and phases of a guarded traffic policy write. Labels are closed.
const (
	TrafficPolicyWriterAppBinding    = "app_binding"
	TrafficPolicyWriterTenantBinding = "tenant_binding"
	TrafficPolicyWriterDomainBinding = "domain_binding"
	TrafficPolicyWriterRouteRule     = "route_rule"
	TrafficPolicyWriterPolicy        = "policy"

	// Discover reads the binding owners to lock, before any lock is held.
	TrafficPolicyPhaseDiscover = "discover"
	// LockWait is the time spent retrying busy locks or a changed owner set
	// before the attempt that got its locks; zero without contention.
	TrafficPolicyPhaseLockWait = "lock_wait"
	// Before reads the projections the verdict compares, under the locks.
	TrafficPolicyPhaseBefore = "before"
	// Validate reads the after projections and checks them at commit.
	TrafficPolicyPhaseValidate = "validate"
)

// TrafficPolicyPhaseObserver receives one phase's duration. A phase is
// recorded whether it succeeds or is refused, so slow refusals stay visible.
type TrafficPolicyPhaseObserver func(writer, phase string, elapsed time.Duration)

var trafficPolicyPhaseObserver atomic.Pointer[TrafficPolicyPhaseObserver]

// SetTrafficPolicyPhaseObserver wires the daemon's histogram; nil disables it.
func SetTrafficPolicyPhaseObserver(observe TrafficPolicyPhaseObserver) {
	if observe == nil {
		trafficPolicyPhaseObserver.Store(nil)
		return
	}
	trafficPolicyPhaseObserver.Store(&observe)
}

func observeTrafficPolicyPhase(writer, phase string, elapsed time.Duration) {
	if observe := trafficPolicyPhaseObserver.Load(); observe != nil {
		(*observe)(writer, phase, elapsed)
	}
}

func trafficPolicyMutationWriter(globalRoutes bool) string {
	if globalRoutes {
		return TrafficPolicyWriterRouteRule
	}
	return TrafficPolicyWriterPolicy
}
