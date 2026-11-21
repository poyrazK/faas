package fcvm

// The run deadline with approval-wait credit (ADR-20261027090016 amendment
// 3). A run whose seller deadline is below its request's timeout_sec gets
// that timeout only as the guest's ceiling: vmmd stops it at the seller
// deadline plus the credit agentd grants through ExtendAgentRun, never past
// the ceiling. At the deadline vmmd closes the run's cancel, which resumes a
// paused guest (agentWaitRun) and sends the cancel frame; RunAgent then
// reports the run as timed out.
//
// Lock order: agentDeadlineMu is a leaf; an agentDeadline's own mu is taken
// only under nothing else.

import (
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// agentDeadline is one run's movable deadline.
type agentDeadline struct {
	mu      sync.Mutex
	credit  time.Duration
	start   time.Time
	base    time.Duration
	ceiling time.Duration
	timer   *time.Timer
}

// due is the deadline's distance from now. The caller holds d.mu.
func (d *agentDeadline) due(now time.Time) time.Duration {
	return max(d.start.Add(min(d.base+d.credit, d.ceiling)).Sub(now), 0)
}

// grant raises the run's credit to credit; a smaller value changes nothing.
func (d *agentDeadline) grant(credit time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if credit <= d.credit {
		return
	}
	d.credit = credit
	if d.timer != nil {
		d.timer.Reset(d.due(time.Now()))
	}
}

// ExtendAgentRun records the run's total approval-wait credit. A run without
// a seller deadline ignores it, as does a run that already reached its
// deadline. Credit that arrives before RunAgent starts the clock
// is kept for it.
func (m *Manager) ExtendAgentRun(instance string, credit time.Duration) {
	if m == nil || instance == "" || credit <= 0 {
		return
	}
	credit = min(credit, time.Duration(api.AgentRunApprovalCreditMaxSec)*time.Second)
	m.mu.Lock()
	inst := m.live[instance]
	credited := inst != nil && inst.AgentRunOnly && inst.AgentRunDeadline > 0
	m.mu.Unlock()
	if !credited {
		return
	}
	m.agentDeadlineMu.Lock()
	if m.agentDeadlines == nil {
		m.agentDeadlines = make(map[string]*agentDeadline)
	}
	d := m.agentDeadlines[instance]
	if d == nil {
		d = &agentDeadline{}
		m.agentDeadlines[instance] = d
	}
	m.agentDeadlineMu.Unlock()
	d.grant(credit)
}

// agentDeadlineRun starts a run's deadline clock. It returns the cancel
// channel the exchange should watch (the caller's, closed also at the
// deadline), whether the deadline was reached, and a stop function for
// when the exchange ends. A run whose deadline is not below its ceiling
// keeps the guest's own timeout and gets cancel back unchanged.
func (m *Manager) agentDeadlineRun(instance string, base, ceiling time.Duration, cancel <-chan struct{}) (<-chan struct{}, func() bool, func()) {
	m.agentDeadlineMu.Lock()
	d := m.agentDeadlines[instance]
	if base <= 0 || base >= ceiling {
		delete(m.agentDeadlines, instance)
		m.agentDeadlineMu.Unlock()
		return cancel, func() bool { return false }, func() {}
	}
	if d == nil {
		d = &agentDeadline{}
		if m.agentDeadlines == nil {
			m.agentDeadlines = make(map[string]*agentDeadline)
		}
		m.agentDeadlines[instance] = d
	}
	m.agentDeadlineMu.Unlock()

	merged := make(chan struct{})
	var (
		closeOnce sync.Once
		reasonMu  sync.Mutex
		reached   bool
	)
	stopRun := func(atDeadline bool) {
		closeOnce.Do(func() {
			reasonMu.Lock()
			reached = atDeadline
			reasonMu.Unlock()
			close(merged)
		})
	}
	d.mu.Lock()
	d.start, d.base, d.ceiling = time.Now(), base, ceiling
	d.timer = time.AfterFunc(d.due(d.start), func() { stopRun(true) })
	d.mu.Unlock()

	done := make(chan struct{})
	if cancel != nil {
		go func() {
			select {
			case <-cancel:
				stopRun(false)
			case <-done:
			}
		}()
	}
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			close(done)
			d.mu.Lock()
			d.timer.Stop()
			d.mu.Unlock()
			m.agentDeadlineMu.Lock()
			if m.agentDeadlines[instance] == d {
				delete(m.agentDeadlines, instance)
			}
			m.agentDeadlineMu.Unlock()
		})
	}
	timedOut := func() bool {
		reasonMu.Lock()
		defer reasonMu.Unlock()
		return reached
	}
	return merged, timedOut, stop
}
