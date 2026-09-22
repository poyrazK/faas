package wire

// Liveness (ADR-190, decision 2) is the per-daemon record of "did my
// main loops actually advance". Readiness (/readyz, daemon_ready)
// answers "are my dependencies up"; liveness answers "am I still
// doing work". The 2026-09-03 schedd prime wedge was ready and not
// live for ten minutes: the notify goroutine was blocked in a gRPC
// call while /metrics kept answering. Nothing observed the stall.
//
// A loop registers itself with a budget and calls Beat every
// iteration. The systemd watchdog (StartWatchdog) pings WATCHDOG=1
// only while no registered loop is past its budget, so a stalled loop
// becomes a unit restart after WatchdogSec instead of an outage that
// waits for a human.

import (
	"sync"
	"sync/atomic"
	"time"
)

// Liveness tracks last-beat timestamps for named loops. Safe for
// concurrent use; Beat is a single atomic store on the hot path.
type Liveness struct {
	mu    sync.RWMutex
	loops map[string]*liveLoop
	now   func() time.Time
}

type liveLoop struct {
	budget   time.Duration
	lastBeat atomic.Int64 // unix nanos
}

// NewLiveness returns an empty registry.
func NewLiveness() *Liveness {
	return &Liveness{loops: map[string]*liveLoop{}, now: time.Now}
}

// Register declares a loop and its stall budget: the longest gap
// between two Beat calls that still counts as healthy. The first beat
// is stamped at registration so a loop that never starts is reported
// stalled after one budget, not never. Re-registering a name replaces
// its budget and resets its clock. budget <= 0 is clamped to 1 s.
func (l *Liveness) Register(loop string, budget time.Duration) {
	if l == nil {
		return
	}
	if budget <= 0 {
		budget = time.Second
	}
	ll := &liveLoop{budget: budget}
	ll.lastBeat.Store(l.now().UnixNano())
	l.mu.Lock()
	l.loops[loop] = ll
	l.mu.Unlock()
}

// Beat records that loop made progress. Unknown loop names are
// ignored so a Beat placed before Register (or in a test without
// wiring) is harmless.
func (l *Liveness) Beat(loop string) {
	if l == nil {
		return
	}
	l.mu.RLock()
	ll := l.loops[loop]
	l.mu.RUnlock()
	if ll == nil {
		return
	}
	ll.lastBeat.Store(l.now().UnixNano())
}

// LoopAge is one loop's observation: how long since its last beat and
// whether that exceeds its budget.
type LoopAge struct {
	Loop    string
	Age     time.Duration
	Budget  time.Duration
	Stalled bool
}

// Ages returns every registered loop's current age, sorted by name
// for stable logs and metrics.
func (l *Liveness) Ages() []LoopAge {
	if l == nil {
		return nil
	}
	now := l.now()
	l.mu.RLock()
	out := make([]LoopAge, 0, len(l.loops))
	for name, ll := range l.loops {
		age := now.Sub(time.Unix(0, ll.lastBeat.Load()))
		out = append(out, LoopAge{Loop: name, Age: age, Budget: ll.budget, Stalled: age > ll.budget})
	}
	l.mu.RUnlock()
	sortLoopAges(out)
	return out
}

// Stalled returns the names of loops past their budget; empty means
// the daemon is live.
func (l *Liveness) Stalled() []string {
	var out []string
	for _, a := range l.Ages() {
		if a.Stalled {
			out = append(out, a.Loop)
		}
	}
	return out
}

// Healthy is the watchdog predicate: true iff no loop is stalled.
func (l *Liveness) Healthy() bool {
	if l == nil {
		return true
	}
	return len(l.Stalled()) == 0
}

func sortLoopAges(a []LoopAge) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j].Loop < a[j-1].Loop; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// SetLoopLiveness publishes one loop's age and stalled bit on
// <daemon>_loop_last_beat_age_seconds{loop} and
// <daemon>_loop_stalled{loop}. nil-safe.
func (m *OpsMetrics) SetLoopLiveness(loop string, age time.Duration, stalled bool) {
	if m == nil || m.loopStalled == nil {
		return
	}
	m.loopLastBeatAgeSeconds.WithLabelValues(loop).Set(age.Seconds())
	v := 0.0
	if stalled {
		v = 1
	}
	m.loopStalled.WithLabelValues(loop).Set(v)
}
