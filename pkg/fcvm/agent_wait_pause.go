package fcvm

// Model-wait pause of agent runs (ADR-20261027090012). vmmd quiet-pauses an
// eligible agent run VM while its harness does nothing but wait on a call
// through the outbound relay:
//
//   - at least one relay connection is open, and no relay bytes crossed it
//     in either direction for the run's AgentWaitPauseAfter;
//   - no output frame arrived from the guest in that window;
//   - the run's cgroup used less than api.AgentWaitPauseCPUMaxMillis of CPU
//     over the same window.
//
// Relay bytes from outboundd resume it before they reach the guest, and so
// do a cancel, the run's deadline and every operation that needs the guest
// (ADR-1151's resume points).
//
// The pause is ADR-1151's: the same quietState, ordering, Firecracker calls
// and QuietPauseReport. Only eligibility and the activity signal are
// agent-specific. A run VM has no TAP (it is networkless), so the ADR's
// condition that no other tenant-egress flow is active holds by
// construction. A networked agent run is not eligible until vmmd checks its
// flows.
//
// Lock order: agentWaitMu is a leaf, like quietMu. It is never held while
// m.mu, quietMu or a quietState lock is taken.

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
)

// AgentWaitCPU reports an instance's cumulative cgroup CPU seconds. vmmd's
// cpustats cache implements it through an adapter.
type AgentWaitCPU interface {
	CPUSeconds(instance string) (float64, bool)
}

// AgentWaitPauseConfig turns the model-wait pause on (vmmd agent_wait_pause).
type AgentWaitPauseConfig struct {
	CPU     AgentWaitCPU
	Metrics *AgentWaitMetrics
}

// WithAgentWaitPause enables the model-wait pause. RunQuietPause then runs
// its pass for agent runs even when ADR-1151 quiet pause is off. Without a
// CPU source nothing is paused: an idle CPU cannot be shown.
func (m *Manager) WithAgentWaitPause(cfg AgentWaitPauseConfig) *Manager {
	m.agentWaitCPU = cfg.CPU
	m.agentWaitMetrics = cfg.Metrics
	if m.quietTick <= 0 {
		m.quietTick = DefaultQuietPauseTick
	}
	return m
}

func (m *Manager) agentWaitOn() bool { return m.agentWaitCPU != nil }

// agentWaitPauseAfterFor is the model-wait pause delay a woken VM gets: only
// an agent run VM carries one.
func agentWaitPauseAfterFor(req WakeRequest) time.Duration {
	if !req.agentRunOnly || req.agentRunWaitPauseAfter <= 0 {
		return 0
	}
	return req.agentRunWaitPauseAfter
}

type agentWaitCPUSample struct {
	at      time.Time
	seconds float64
}

// agentWaitState is one run's model-wait activity. It lives while RunAgent
// runs; a VM without one is never paused.
type agentWaitState struct {
	inst *Instance
	// relays counts the run's open relay connections.
	relays int
	// lastActivity is the last relay byte, output frame or run start.
	lastActivity time.Time
	// stopping is set once the run is cancelled or reaches its deadline.
	// From then on the guest must run to stop the harness and report.
	stopping bool
	// cpu holds the pass's recent CPU samples, oldest first.
	cpu []agentWaitCPUSample
}

// agentWaitRun registers a run for the model-wait pause. It returns the run's
// output receiver and cancel channel wrapped so that an output frame counts
// as activity and a cancel resumes the guest before the cancel frame is
// written, and a stop function for when the exchange ends. A run that is not
// eligible gets its receiver and cancel channel back unchanged.
func (m *Manager) agentWaitRun(ctx context.Context, instance string, inst *Instance, timeout time.Duration,
	receive agentrunproto.OutputReceiver, cancel <-chan struct{}) (agentrunproto.OutputReceiver, <-chan struct{}, func()) {
	if !m.agentWaitOn() || inst == nil || inst.AgentWaitPauseAfter <= 0 {
		return receive, cancel, func() {}
	}
	m.agentWaitMu.Lock()
	if m.agentWaits == nil {
		m.agentWaits = make(map[string]*agentWaitState)
	}
	m.agentWaits[instance] = &agentWaitState{inst: inst, lastActivity: time.Now()}
	m.agentWaitMu.Unlock()

	wrapped := func(ctx context.Context, stream string, chunk []byte) error {
		m.agentWaitMark(instance)
		if receive == nil {
			return nil
		}
		return receive(ctx, stream, chunk)
	}
	done := make(chan struct{})
	var cancelOut chan struct{}
	if cancel != nil {
		cancelOut = make(chan struct{})
		go func() {
			select {
			case <-cancel:
				m.agentWaitStop(ctx, instance)
				close(cancelOut)
			case <-done:
			}
		}()
	}
	// The host timer runs while the guest is paused: at the run's deadline
	// the guest resumes, so its own timeout stops the harness and reports.
	deadline := time.AfterFunc(timeout, func() { m.agentWaitStop(ctx, instance) })
	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(done)
			deadline.Stop()
			m.agentWaitMu.Lock()
			if st := m.agentWaits[instance]; st != nil && st.inst == inst {
				delete(m.agentWaits, instance)
			}
			m.agentWaitMu.Unlock()
		})
	}
	if cancelOut == nil {
		return wrapped, nil, stop
	}
	return wrapped, cancelOut, stop
}

// agentWaitMark records activity for a run, so the pass waits a full window
// before it pauses the VM.
func (m *Manager) agentWaitMark(instance string) {
	m.agentWaitMu.Lock()
	if st := m.agentWaits[instance]; st != nil {
		st.lastActivity = time.Now()
	}
	m.agentWaitMu.Unlock()
}

// agentWaitStop stops pausing a run and resumes it if it is paused.
func (m *Manager) agentWaitStop(ctx context.Context, instance string) {
	m.agentWaitMu.Lock()
	if st := m.agentWaits[instance]; st != nil {
		st.stopping = true
		st.lastActivity = time.Now()
	}
	m.agentWaitMu.Unlock()
	m.quietResumeBestEffort(context.WithoutCancel(ctx), instance)
}

// AgentRelay is one open relay connection of an agent run whose model-wait
// pause is on. vmmd's relay splice reports the connection's bytes to it.
type AgentRelay struct {
	m        *Manager
	instance string
}

// AgentRelayOpened counts an open relay connection of an agent run whose
// model-wait pause is on. It returns nil for any other instance, whose relay
// keeps the plain splice. Close the relay when the connection ends.
func (m *Manager) AgentRelayOpened(instance string) *AgentRelay {
	if m == nil || !m.agentWaitOn() {
		return nil
	}
	m.agentWaitMu.Lock()
	defer m.agentWaitMu.Unlock()
	st := m.agentWaits[instance]
	if st == nil {
		return nil
	}
	st.relays++
	st.lastActivity = time.Now()
	return &AgentRelay{m: m, instance: instance}
}

// ToGuest is called with bytes from outboundd, before they are written to
// the guest. It marks activity first and then resumes a paused VM (ADR-1151
// ordering), so the bytes never reach a stopped guest.
func (r *AgentRelay) ToGuest(ctx context.Context) error {
	r.m.agentWaitMark(r.instance)
	return r.m.QuietResume(ctx, r.instance)
}

// FromGuest is called with bytes the guest sent.
func (r *AgentRelay) FromGuest() { r.m.agentWaitMark(r.instance) }

// Close ends the relay connection's count.
func (r *AgentRelay) Close() {
	r.m.agentWaitMu.Lock()
	if st := r.m.agentWaits[r.instance]; st != nil && st.relays > 0 {
		st.relays--
		st.lastActivity = time.Now()
	}
	r.m.agentWaitMu.Unlock()
}

// Reader wraps the reader of one splice direction so each read reports to
// the relay before its bytes are written on. toGuest is true for the
// outboundd-to-guest direction.
func (r *AgentRelay) Reader(ctx context.Context, src io.Reader, toGuest bool) io.Reader {
	return agentRelayReader{ctx: ctx, src: src, relay: r, toGuest: toGuest}
}

type agentRelayReader struct {
	ctx     context.Context
	src     io.Reader
	relay   *AgentRelay
	toGuest bool
}

func (r agentRelayReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if n > 0 {
		if !r.toGuest {
			r.relay.FromGuest()
		} else if rerr := r.relay.ToGuest(r.ctx); rerr != nil {
			return 0, rerr
		}
	}
	return n, err
}

// agentWaitEligibleLocked reports whether a live agent run VM may be paused
// while it waits on the model. The caller holds m.mu.
func (m *Manager) agentWaitEligibleLocked(id string, inst *Instance) bool {
	if !m.agentWaitOn() || inst == nil || !inst.AgentRunOnly || inst.AgentWaitPauseAfter <= 0 ||
		!inst.Lease.Networkless || inst.Paused || inst.Frozen {
		return false
	}
	if _, busy := m.instanceFlights[id]; busy {
		return false
	}
	return m.instanceStops[id] == nil && m.pendingCleanup[id] == nil
}

// agentWaitSample records the run's CPU seconds for this pass, keeping the
// samples that can still be a window's baseline.
func (m *Manager) agentWaitSample(id string, after time.Duration, now time.Time) {
	seconds, ok := m.agentWaitCPU.CPUSeconds(id)
	m.agentWaitMu.Lock()
	defer m.agentWaitMu.Unlock()
	st := m.agentWaits[id]
	if st == nil {
		return
	}
	if !ok {
		st.cpu = st.cpu[:0]
		return
	}
	st.cpu = append(st.cpu, agentWaitCPUSample{at: now, seconds: seconds})
	// Keep the newest sample at or before the window's start; drop older ones.
	cut := 0
	for i, s := range st.cpu {
		if now.Sub(s.at) >= after {
			cut = i
		}
	}
	st.cpu = st.cpu[cut:]
}

// agentWaitIdle reports whether a run has waited on the model, doing nothing
// else, for at least after.
func (m *Manager) agentWaitIdle(id string, after time.Duration, now time.Time) bool {
	m.agentWaitMu.Lock()
	st := m.agentWaits[id]
	if st == nil || st.stopping || st.relays < 1 || now.Sub(st.lastActivity) < after {
		m.agentWaitMu.Unlock()
		return false
	}
	var base *agentWaitCPUSample
	for i := range st.cpu {
		if now.Sub(st.cpu[i].at) >= after {
			base = &st.cpu[i]
		}
	}
	baseSeconds, haveBase := 0.0, base != nil
	if haveBase {
		baseSeconds = base.seconds
	}
	m.agentWaitMu.Unlock()
	if !haveBase {
		return false
	}
	current, ok := m.agentWaitCPU.CPUSeconds(id)
	if !ok || current < baseSeconds {
		return false
	}
	return (current-baseSeconds)*1000 < api.AgentWaitPauseCPUMaxMillis
}

// candidateIdle and candidateEligibleLocked pick the ADR-1151 or the
// model-wait rule for a pause pass candidate.
func (m *Manager) candidateIdle(c quietCandidate, st *quietState, now time.Time) bool {
	if c.agent {
		return m.agentWaitIdle(c.id, c.after, now)
	}
	return m.quietIdle(c.id, st, c.after, now)
}

func (m *Manager) candidateEligibleLocked(c quietCandidate, inst *Instance) bool {
	if c.agent {
		return m.agentWaitEligibleLocked(c.id, inst)
	}
	return m.quietEligibleLocked(c.id, inst)
}

// AgentWaitMetrics owns the ADR-20261027090012 series.
type AgentWaitMetrics struct {
	pauses *prometheus.CounterVec
	resume prometheus.Histogram
	paused prometheus.Gauge
}

// NewAgentWaitMetrics registers vmmd_agent_wait_pause_total,
// vmmd_agent_wait_resume_seconds and vmmd_agent_wait_paused_instances on
// reg. A nil registerer returns nil, which every method accepts.
func NewAgentWaitMetrics(reg prometheus.Registerer) *AgentWaitMetrics {
	if reg == nil {
		return nil
	}
	m := &AgentWaitMetrics{
		pauses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vmmd_agent_wait_pause_total",
			Help: "Agent run VMs paused while they wait on the model, by outcome: paused, error or resume_error (ADR-20261027090012).",
		}, []string{"outcome"}),
		resume: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "vmmd_agent_wait_resume_seconds",
			Help:    "Time to resume an agent run VM paused while it waited on the model (ADR-20261027090012).",
			Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25},
		}),
		paused: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "vmmd_agent_wait_paused_instances",
			Help: "Agent run VMs paused now while they wait on the model (ADR-20261027090012).",
		}),
	}
	for _, outcome := range []string{"paused", "error", "resume_error"} {
		m.pauses.WithLabelValues(outcome)
	}
	reg.MustRegister(m.pauses, m.resume, m.paused)
	return m
}

func (a *AgentWaitMetrics) observePause(outcome string) {
	if a != nil {
		a.pauses.WithLabelValues(outcome).Inc()
	}
}

func (a *AgentWaitMetrics) observeResume(d time.Duration) {
	if a != nil {
		a.resume.Observe(d.Seconds())
	}
}

func (a *AgentWaitMetrics) setPaused(n int) {
	if a != nil {
		a.paused.Set(float64(n))
	}
}
