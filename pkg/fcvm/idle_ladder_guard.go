package fcvm

// The idle ladder's activity guard (ADR-20261107122928 §6), observe mode.
//
// Quiet pause pauses only functions and between_requests apps. Any other app
// may run timers, heartbeats or background threads between requests, and a
// pause would stop them. The guard calls such an app idle only when, over the
// last IdleLadderQuietPauseAfterMillis, nothing was in flight, its VM cgroup
// used at most IdleLadderGuardCPUMaxMillis of CPU and its veth moved at most
// IdleLadderGuardNetMaxBytes in each direction.
//
// Observe mode pauses nothing. It records each pause the guard would make and
// what would have ended it: a request; inbound traffic, on which a real pause
// would resume the guest; or the app's own CPU or outbound traffic, work a
// real pause would have held back. It also counts the billed MB-seconds
// those pauses would not bill. The pass runs under idleMu, like the rest of
// the observe pass.

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// IdleLadderCPU reports an instance's cumulative VM cgroup CPU seconds.
// vmmd's cpustats cache implements it through an adapter.
type IdleLadderCPU interface {
	CPUSeconds(instance string) (float64, bool)
}

// IdleLadderNet reports an instance's cumulative veth bytes: egress (guest to
// the world) and ingress. vmmd's netstats cache implements it through an
// adapter.
type IdleLadderNet interface {
	NetBytes(instance string) (egress, ingress uint64, ok bool)
}

// idleGuardWindow is the stretch the guard checks: the ladder's L1 delay.
const idleGuardWindow = api.IdleLadderQuietPauseAfterMillis * time.Millisecond

// idleGuardVerdict is where the guard holds an app that is idle for L1.
type idleGuardVerdict int

const (
	idleGuardNone    idleGuardVerdict = iota // not guarded, busy, or idle less than L1
	idleGuardPaused                          // the guard would hold it paused
	idleGuardCPU                             // its CPU kept it running
	idleGuardNet                             // its traffic kept it running
	idleGuardUnknown                         // no full window of counters yet
	idleGuardVerdictCount
)

var idleGuardVerdictNames = [idleGuardVerdictCount]string{"none", "paused", "cpu", "net", "unknown"}

// Causes that end a pause the guard would make.
const (
	idleGuardEndRequest    = "request"    // a request arrived
	idleGuardEndInbound    = "inbound"    // traffic reached the guest; a real pause resumes on it
	idleGuardEndBackground = "background" // the app used CPU or sent traffic on its own
	idleGuardEndFrozen     = "frozen"     // schedd froze the instance
)

type idleGuardSample struct {
	at              time.Time
	cpu             float64
	egress, ingress uint64
}

// idleGuardState is one guarded instance's counters and verdict.
type idleGuardState struct {
	// samples holds this pass's counters back to the newest sample at least
	// a window old, oldest first.
	samples []idleGuardSample
	verdict idleGuardVerdict
	// pausedAt is when the guard would have paused the instance; zero unless
	// it would hold it paused now.
	pausedAt time.Time
	// wakeBase is the ingress byte count when the guard last allowed quiet
	// pause to pause the app (idle_ladder_apps.go); wakeBaseSet marks it.
	wakeBase    uint64
	wakeBaseSet bool
}

func (m *Manager) idleGuardOn() bool { return m.idleCPU != nil && m.idleNet != nil }

// idleGuardRecord appends this pass's counters to a guarded instance's
// window. A missing reading breaks the window: the guard then waits for a
// full one.
func (m *Manager) idleGuardRecord(st *idleLadderState, c idleLadderCandidate, now time.Time) {
	if !c.guarded || !m.idleGuardOn() {
		return
	}
	g := &st.guard
	cpu, cpuOK := m.idleCPU.CPUSeconds(c.id)
	egress, ingress, netOK := m.idleNet.NetBytes(c.id)
	if !cpuOK || !netOK {
		g.samples = g.samples[:0]
		return
	}
	g.samples = append(g.samples, idleGuardSample{at: now, cpu: cpu, egress: egress, ingress: ingress})
	cut := 0
	for i, s := range g.samples {
		if now.Sub(s.at) >= idleGuardWindow {
			cut = i
		}
	}
	g.samples = g.samples[cut:]
}

// window is what the counters moved over the last window. ok is false
// without a baseline at least a window old, or after a counter reset.
func (g *idleGuardState) window(now time.Time) (cpuMillis float64, egress, ingress uint64, ok bool) {
	if len(g.samples) < 2 {
		return 0, 0, 0, false
	}
	base, last := g.samples[0], g.samples[len(g.samples)-1]
	if now.Sub(base.at) < idleGuardWindow || last.cpu < base.cpu || last.egress < base.egress || last.ingress < base.ingress {
		return 0, 0, 0, false
	}
	return (last.cpu - base.cpu) * 1000, last.egress - base.egress, last.ingress - base.ingress, true
}

// idleGuardStep runs the guard for an idle instance after the observe pass
// updated its idle time.
func (m *Manager) idleGuardStep(st *idleLadderState, c idleLadderCandidate, now time.Time, step time.Duration) {
	g := &st.guard
	if !c.guarded || !m.idleGuardOn() {
		g.verdict = idleGuardNone
		return
	}
	if c.frozen {
		m.idleGuardEnd(st, idleGuardEndFrozen, now)
		return
	}
	cpuMillis, egress, ingress, ok := g.window(now)
	if !g.pausedAt.IsZero() {
		switch {
		case !ok:
			// No reading: a real pause would hold; keep counting.
		case ingress > api.IdleLadderGuardNetMaxBytes:
			m.idleGuardEnd(st, idleGuardEndInbound, now)
			return
		case cpuMillis > api.IdleLadderGuardCPUMaxMillis || egress > api.IdleLadderGuardNetMaxBytes:
			m.idleGuardEnd(st, idleGuardEndBackground, now)
			return
		}
		m.idleMetrics.observeGuardHeld(c.lease, step)
		return
	}
	if st.idle < idleGuardWindow {
		g.verdict = idleGuardNone
		return
	}
	switch {
	case !ok:
		g.verdict = idleGuardUnknown
	case cpuMillis > api.IdleLadderGuardCPUMaxMillis:
		g.verdict = idleGuardCPU
	case egress > api.IdleLadderGuardNetMaxBytes || ingress > api.IdleLadderGuardNetMaxBytes:
		g.verdict = idleGuardNet
	default:
		g.verdict, g.pausedAt = idleGuardPaused, now
		m.idleMetrics.observeGuardPause()
	}
}

// idleGuardEnd ends a pause the guard would hold, recording why and for how
// long. It is a no-op when there is none.
func (m *Manager) idleGuardEnd(st *idleLadderState, cause string, now time.Time) {
	g := &st.guard
	g.verdict = idleGuardNone
	if g.pausedAt.IsZero() {
		return
	}
	m.idleMetrics.observeGuardResume(cause, max(now.Sub(g.pausedAt), 0))
	g.pausedAt = time.Time{}
}

// IdleGuardVerdicts counts the guarded instances by the guard's verdict.
func (m *Manager) IdleGuardVerdicts() (counts [idleGuardVerdictCount]int) {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	for _, st := range m.idleStates {
		counts[st.guard.verdict]++
	}
	return counts
}
