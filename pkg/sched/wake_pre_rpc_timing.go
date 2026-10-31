package sched

import (
	"context"
	"sync"
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
	if w := t.PreWake; w.Set {
		for i, ms := range []int64{w.AuthorizeMs, w.EnvironmentMs, w.CoordinateMs, w.TransitionMs} {
			if obs, ok := e.ops.WakeRPCDuration(appID, preWakePhases[i]).(prometheus.ExemplarObserver); ok {
				obs.ObserveWithExemplar((time.Duration(ms) * time.Millisecond).Seconds(), exemplar)
			}
		}
	}
}

// preWakeClock stamps the EnsureWake RPC's steps before Engine.Wake starts,
// which wakeStartedKey cannot see: the RPC's authorization, the app and
// admission reads, coordination and the parked -> active claim. The RPC
// handler starts it; ensureWake marks it; the wake that admits reads it into
// events.PreRPCTimings.PreWake.
type preWakeClock struct {
	mu                                          sync.Mutex
	rpcStarted, ensureStarted, envReady, leader time.Time
}

type preWakeClockKey struct{}

// WithEnsureWakeRPCStarted starts the pre-wake clock for one EnsureWake RPC.
func WithEnsureWakeRPCStarted(ctx context.Context, at time.Time) context.Context {
	return context.WithValue(ctx, preWakeClockKey{}, &preWakeClock{rpcStarted: at})
}

// EnsureWakeRPCStartedAt is when the EnsureWake RPC carrying ctx started its
// pre-wake clock; zero when it did not.
func EnsureWakeRPCStartedAt(ctx context.Context) time.Time {
	c := preWakeClockFrom(ctx)
	if c == nil {
		return time.Time{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rpcStarted
}

func preWakeClockFrom(ctx context.Context) *preWakeClock {
	c, _ := ctx.Value(preWakeClockKey{}).(*preWakeClock)
	return c
}

// preWakeMark names one stamp of the clock.
type preWakeMark int

const (
	preWakeEnsureStarted preWakeMark = iota
	preWakeEnvironmentReady
	preWakeLeader
)

// markPreWake stamps mark once; later calls (a retried step) keep the first.
func markPreWake(ctx context.Context, mark preWakeMark) {
	c := preWakeClockFrom(ctx)
	if c == nil {
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	var field *time.Time
	switch mark {
	case preWakeEnsureStarted:
		field = &c.ensureStarted
	case preWakeEnvironmentReady:
		field = &c.envReady
	case preWakeLeader:
		field = &c.leader
	}
	if field != nil && field.IsZero() {
		*field = now
	}
}

// preWakeTimings derives the spans for a wake that started at wakeStarted.
func preWakeTimings(ctx context.Context, wakeStarted time.Time) events.PreWakeTimings {
	c := preWakeClockFrom(ctx)
	if c == nil {
		return events.PreWakeTimings{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return events.NewPreWakeTimings(c.rpcStarted, c.ensureStarted, c.envReady, c.leader, wakeStarted)
}

// preWakePhases are the schedd_wake_rpc_duration_seconds phases that split
// EnsureWake before Engine.Wake, in order.
var preWakePhases = []string{"pre_wake_authorize", "pre_wake_environment", "pre_wake_coordinate", "pre_wake_transition"}

// preRPCWithPreWake attaches the pre-wake spans, ending where Engine.Wake
// began (attempt when the admission was not reached through Wake).
func preRPCWithPreWake(ctx context.Context, t events.PreRPCTimings, attempt time.Time) events.PreRPCTimings {
	if !t.Set {
		return t
	}
	started := wakeStartedFrom(ctx)
	if started.IsZero() {
		started = attempt
	}
	t.PreWake = preWakeTimings(ctx, started)
	return t
}
