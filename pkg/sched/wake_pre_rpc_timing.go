package sched

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/events"
)

// wakeStartedKey carries the instant Engine.Wake began through to the
// admission it hands off to, so wake.boot_started can report the fast path
// that ran before it (events.PreRPCTimings.BeforeAdmissionMs).
type wakeStartedKey struct{}

func withWakeStarted(ctx context.Context, at time.Time) context.Context {
	return context.WithValue(ctx, wakeStartedKey{}, at)
}

// wakeStartedFrom is zero when the admission was not reached through Wake.
func wakeStartedFrom(ctx context.Context) time.Time {
	at, _ := ctx.Value(wakeStartedKey{}).(time.Time)
	return at
}

// preRPCPhases are the schedd_wake_rpc_duration_seconds phases that split the
// window before admit_to_rpc, in wake order.
var preRPCPhases = []string{"before_admission", "lock_wait", "reads", "admit", "post_admit"}

// observePreRPCPhases records one successful wake's pre-RPC spans beside the
// ADR-097 phases, with the same wake_id exemplar.
func (e *Engine) observePreRPCPhases(appID string, exemplar prometheus.Labels, t events.PreRPCTimings) {
	if e.ops == nil || !t.Set {
		return
	}
	spans := []int64{t.BeforeAdmissionMs, t.LockWaitMs, t.ReadsMs, t.AdmitMs, t.PostAdmitMs}
	for i, phase := range preRPCPhases {
		if obs, ok := e.ops.WakeRPCDuration(appID, phase).(prometheus.ExemplarObserver); ok {
			obs.ObserveWithExemplar((time.Duration(spans[i]) * time.Millisecond).Seconds(), exemplar)
		}
	}
}
