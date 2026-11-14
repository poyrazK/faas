package sched

// Idle-out through the idle ladder (ADR-20261107122928 §1, §3). With vmmd's
// idle_ladder on, an idle request instance stays RUNNING and quiet-paused in
// vmmd (L1, L2 after IdleLadderFreezeAfterSeconds), where a request resumes
// it in about a millisecond with no schedd thaw. With schedd's
// idle_ladder_idle_out on, the reaper leaves such an instance resident past
// its idle timeout and parks it at IdleLadderSnapshotAfterSeconds (L5)
// instead of freezing it; RAM pressure parks it sooner, before any instance
// still inside its idle timeout. The schedd FROZEN state then only drains.
//
// Paused seconds are unbilled only through the quiet pause ledger
// (ADR-20261023171905), so the reaper holds an instance only for accounts
// the quiet_pause_unbilled platform flag releases, the same decision meterd
// makes. Every other account keeps its idle timeout.

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// idleLadderSnapshotAfter is L5's default entry: an instance the ladder holds
// parks this long after its last request (ADR-20261107122928 §1).
const idleLadderSnapshotAfter = api.IdleLadderSnapshotAfterSeconds * time.Second

// WithIdleLadderIdleOut hands idle-out to the idle ladder (schedd
// idle_ladder_idle_out, off by default). released decides the
// quiet_pause_unbilled platform flag for an app's account; nil keeps every
// app on its idle timeout.
func (e *Engine) WithIdleLadderIdleOut(released func(platformflags.Subject) bool) *Engine {
	e.idleLadderRelease = released
	return e
}

// idleLadderReleased reports whether the reaper may hold an idle instance of
// subject's account at the ladder's levels past its idle timeout.
func (e *Engine) idleLadderReleased(subject platformflags.Subject) bool {
	return e != nil && e.idleLadderRelease != nil && subject.AccountID != "" && e.idleLadderRelease(subject)
}

// idleLadderHeld reports whether the ladder owns this instance's idle
// period: its account is released and vmmd reports it quiet-paused (level 1
// or deeper) in a fresh sample. A level-0 instance past its idle timeout is
// running or kept busy by background work, and idles out as before.
func (in InstanceInfo) idleLadderHeld() bool {
	return in.IdleLadderHold && in.IdleLadderLevel >= 1 && in.State == state.StateRunning
}

// idleTimeout is how long after its last activity the idle reaper parks
// this instance: its effective idle timeout, or for an instance the ladder
// holds, the later of that and L5's entry.
func (in InstanceInfo) idleTimeout() time.Duration {
	timeout := time.Duration(EffectiveIdleTimeoutS(in.Plan, in.AppType, in.IdleTimeoutS)) * time.Second
	if in.idleLadderHeld() {
		return max(timeout, idleLadderSnapshotAfter)
	}
	return timeout
}

// idleLadderSurplus reports whether the ladder holds this instance past its
// effective idle timeout: residency the instance would not have without the
// ladder. RAM-pressure eviction takes these with the frozen instances,
// before any instance inside its idle timeout.
func (in InstanceInfo) idleLadderSurplus(now time.Time) bool {
	if !in.idleLadderHeld() {
		return false
	}
	last := idleReference(in)
	timeout := time.Duration(EffectiveIdleTimeoutS(in.Plan, in.AppType, in.IdleTimeoutS)) * time.Second
	return !last.IsZero() && now.Sub(last) > timeout
}

// IdleLadderIdleOutMetrics reports what the reaper holds at the ladder's
// levels and parks at L5.
type IdleLadderIdleOutMetrics struct {
	held  prometheus.Gauge
	parks prometheus.Counter
}

// NewIdleLadderIdleOutMetrics registers the idle-out series on reg.
func NewIdleLadderIdleOutMetrics(reg prometheus.Registerer) *IdleLadderIdleOutMetrics {
	m := &IdleLadderIdleOutMetrics{
		held: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "schedd_idle_ladder_held_instances",
			Help: "Instances the idle ladder keeps resident past their idle timeout (ADR-20261107122928).",
		}),
		parks: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "schedd_idle_ladder_parks_total",
			Help: "Instances the idle reaper parked at the idle ladder's snapshot level (ADR-20261107122928).",
		}),
	}
	if reg != nil {
		reg.MustRegister(m.held, m.parks)
	}
	return m
}

// WithIdleLadderIdleOutMetrics attaches the idle-out series.
func (l *Loop) WithIdleLadderIdleOutMetrics(m *IdleLadderIdleOutMetrics) *Loop {
	l.idleLadderMetrics = m
	return l
}

// observeIdleLadderHeld publishes how many instances the ladder holds past
// their idle timeout this tick.
func (l *Loop) observeIdleLadderHeld(snapshot []InstanceInfo, now time.Time) {
	if l.idleLadderMetrics == nil {
		return
	}
	held := 0
	for _, in := range snapshot {
		if in.idleLadderSurplus(now) {
			held++
		}
	}
	l.idleLadderMetrics.held.Set(float64(held))
}

func (l *Loop) observeIdleLadderPark() {
	if l.idleLadderMetrics != nil {
		l.idleLadderMetrics.parks.Inc()
	}
}
