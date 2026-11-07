package fcvm

// Guarded pause of apps (ADR-20261107122928 §6). With vmmd's idle_ladder on
// and idle_ladder_apps on, quiet pause also pauses an app schedd marked
// IdleLadderGuardedPause (a request-serving app that declared no pause), but
// only when the activity guard agrees it is idle: nothing in flight for the
// ladder's L1 delay and, over that window, VM CPU and veth traffic within the
// guard's budget. The pause is ADR-1151's: the same quietState, resume points,
// probes and pause ledger, and a request resumes it in vmmd's forward path.
//
// Traffic that reaches a paused app any other way, such as a reply on a
// connection the app opened or a peer's keepalive, resumes it too. The ladder
// pass compares the veth's ingress bytes with what they were when the guard
// allowed the pause and resumes on any byte, within about two 250 ms ticks
// (the netstats poll, then the pass). Without counters it resumes: a paused
// app it cannot watch must not stay paused.

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// idleGuardMaxSampleAge bounds how old the guard's latest counters may be
// when it allows a pause.
const idleGuardMaxSampleAge = 750 * time.Millisecond

// Causes for resuming a guarded app outside the forward path.
const (
	idleAppResumeInbound    = "inbound"     // traffic reached the guest
	idleAppResumeNoCounters = "no_counters" // the guard lost its counters
)

// idleLadderGuardedPauseFor reports whether vmmd may pause a woken instance
// behind the activity guard: one the ladder measures, that schedd marked, and
// that quiet pause does not pause already.
func idleLadderGuardedPauseFor(req WakeRequest, qualificationOnly bool) bool {
	return req.IdleLadderGuardedPause && req.QuietPauseAfter <= 0 && idleLadderEligibleFor(req, qualificationOnly)
}

// idleGuardAllows reports whether the activity guard lets quiet pause pause a
// guarded app now. When it does, it records the ingress bytes the pause
// starts from, so the ladder pass can resume the app on any traffic after.
func (m *Manager) idleGuardAllows(id string, now time.Time) bool {
	if !m.idleGuardOn() {
		return false
	}
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	st := m.idleStates[id]
	if st == nil || len(st.guard.samples) == 0 {
		return false
	}
	g := &st.guard
	last := g.samples[len(g.samples)-1]
	if now.Sub(last.at) > idleGuardMaxSampleAge {
		return false
	}
	cpuMillis, egress, ingress, ok := g.window(now)
	if !ok || cpuMillis > api.IdleLadderGuardCPUMaxMillis ||
		egress > api.IdleLadderGuardNetMaxBytes || ingress > api.IdleLadderGuardNetMaxBytes {
		return false
	}
	g.wakeBase, g.wakeBaseSet = last.ingress, true
	return true
}

// idleInboundReached reports whether a guarded app quiet pause holds paused
// must resume: ingress moved since the guard allowed the pause, or its
// counters are gone. The caller holds idleMu.
func (m *Manager) idleInboundReached(st *idleLadderState, c idleLadderCandidate) (cause string, resume bool) {
	g := &st.guard
	if !m.idleAppsOn || !c.quietPaused || !c.inst.IdleLadderGuardedPause || !g.wakeBaseSet {
		return "", false
	}
	if len(g.samples) == 0 {
		g.wakeBaseSet = false
		return idleAppResumeNoCounters, true
	}
	// != rather than >: a counter reset (a recreated veth) resumes too.
	if g.samples[len(g.samples)-1].ingress == g.wakeBase {
		return "", false
	}
	g.wakeBaseSet = false
	return idleAppResumeInbound, true
}

// idleAppResume is a guarded app the ladder pass resumes, and why.
type idleAppResume struct {
	id, cause string
}

// idleInboundResume resumes a guarded app the ladder pass found traffic for.
func (m *Manager) idleInboundResume(ctx context.Context, r idleAppResume) {
	if err := m.QuietResume(ctx, r.id); err != nil {
		m.log.Warn("idle ladder: resume a paused app for inbound traffic failed; the next request resumes it",
			"instance", r.id, "cause", r.cause, "err", err)
		return
	}
	m.idleMetrics.observeAppResume(r.cause)
}
