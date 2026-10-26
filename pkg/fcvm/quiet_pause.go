package fcvm

// Quiet pause (ADR-1151). vmmd pauses an eligible running instance's vCPUs
// once it has had no forwarded request, stream or tail task for its
// QuietPauseAfter, and resumes it when the next request arrives. The
// instance stays live and RUNNING in schedd's view: nothing is
// snapshotted, reclaimed or written to the database, and billing is
// unchanged. A quiet-paused VM costs no host CPU, so schedd can stop
// counting its vCPUs toward the node's active vCPU.
//
// Ordering. A pause sets quietState.paused before its last activity check
// and before the Firecracker call, all under quietState.mu. A request
// marks activity first and then calls QuietResume: either the pause sees
// the request and backs off, or the request sees paused and waits on the
// lock until the pause finishes, then resumes the VM. Operations that need
// a running guest (snapshot, freeze, migration, stop) register their
// instance flight first and then call QuietResume, so the pass that
// checks for flights either sees theirs or is resumed by it.
//
// Lock order: quietState.mu, then Manager.mu. quietMu is a leaf.

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	// DefaultQuietPauseTick is how often the pause pass looks for idle
	// instances. It bounds how far past QuietPauseAfter a pause lands.
	DefaultQuietPauseTick = 250 * time.Millisecond
	// quietVMMTimeout bounds one Firecracker pause or resume call; both take
	// well under a millisecond when the VMM is healthy.
	quietVMMTimeout = 2 * time.Second
)

// QuietActivity reports per-instance request activity. vmmd's activity
// tracker (pkg/fcvm/activity) implements it: inflight counts forwarded
// requests and streams, and since is when the last of them ended.
type QuietActivity interface {
	Quiet(instance string) (inflight int64, since time.Time, ok bool)
}

// QuietPauseConfig turns quiet pause on.
type QuietPauseConfig struct {
	Activity QuietActivity
	Metrics  *QuietPauseMetrics
	// Tick is the pause pass period; zero means DefaultQuietPauseTick.
	Tick time.Duration
}

// WithQuietPause enables quiet pause. Without it RunQuietPause returns at
// once and QuietResume never finds a paused instance.
func (m *Manager) WithQuietPause(cfg QuietPauseConfig) *Manager {
	m.quietActivity = cfg.Activity
	m.quietMetrics = cfg.Metrics
	m.quietTick = cfg.Tick
	if m.quietTick <= 0 {
		m.quietTick = DefaultQuietPauseTick
	}
	return m
}

// quietPauser is the VMM capability quiet pause needs; resume uses
// VMM.ResumeVM.
type quietPauser interface {
	PauseVM(ctx context.Context, l Lease) error
}

// quietState is one live incarnation's pause state. schedd reuses an
// instance id when it wakes the instance again, so the state remembers the
// *Instance it describes and is discarded when the id comes back as a new VM.
type quietState struct {
	inst *Instance
	mu   sync.Mutex
	// paused is set before the Firecracker pause call so a request that
	// races the pause waits on mu instead of reaching a guest that is
	// about to stop.
	paused atomic.Bool
	// epoch counts pauses and resumes, so a probe can tell that the guest
	// was paused at some point while the probe ran.
	epoch atomic.Uint64
	// since is when the current pause took effect; it stays zero while a
	// failed pause leaves the guest's state unknown. Guarded by mu.
	since time.Time
	// lastStarted and lastEnded bound the most recent completed pause, and
	// pausedTotal sums every completed pause of this incarnation on the
	// monotonic clock (ADR-20261023171905). Guarded by mu.
	lastStarted, lastEnded time.Time
	pausedTotal            time.Duration
	// seenAt is when the pause pass first saw this incarnation. Idleness is
	// never measured from before it: the activity tracker keys on the
	// instance id, so its last request may belong to an earlier VM.
	seenAt time.Time
}

// quietPauseAfterFor is the quiet pause delay an instance gets. Instances
// whose traffic or lifecycle does not pass through vmmd's forward path are
// never quiet-paused: private-network peers talk guest to guest, warm-pool
// entries are already paused, and execution, app-task, qualification and
// builder VMs are not request-driven.
func quietPauseAfterFor(req WakeRequest, qualificationOnly bool) time.Duration {
	if req.QuietPauseAfter <= 0 || req.ExecutionOnly || req.AppTaskOnly || qualificationOnly ||
		req.KeepPaused || req.ExportDir != "" || req.PrivateNetworkID != "" {
		return 0
	}
	return req.QuietPauseAfter
}

// quietEligibleLocked reports whether a live instance may be quiet-paused
// now. A frozen instance (ADR-1151) is already paused, and its pause belongs
// to Freeze and Thaw. The caller holds m.mu.
func (m *Manager) quietEligibleLocked(id string, inst *Instance) bool {
	if inst == nil || inst.QuietPauseAfter <= 0 || inst.Paused || inst.Frozen || inst.IsJob ||
		inst.ExecutionOnly || inst.AppTaskOnly || inst.QualificationOnly || inst.TailCount > 0 {
		return false
	}
	if functionExecutionBusy(inst) || functionMigrationActive(inst) {
		return false
	}
	if _, busy := m.instanceFlights[id]; busy {
		return false
	}
	// Startup is not over until the runtime's framework-ready receipt is
	// recorded; its loop polls the guest until then.
	if m.frameworkReadyRuns[id] != nil {
		return false
	}
	return m.instanceStops[id] == nil && m.pendingCleanup[id] == nil
}

type quietCandidate struct {
	id    string
	inst  *Instance
	after time.Duration
}

// RunQuietPause pauses idle eligible instances every tick until ctx ends.
func (m *Manager) RunQuietPause(ctx context.Context) {
	if m.quietActivity == nil {
		return
	}
	if _, ok := m.vmm.(quietPauser); !ok {
		m.log.Warn("quiet pause: VMM cannot pause; quiet pause stays off")
		return
	}
	ticker := time.NewTicker(m.quietTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.quietPausePass(ctx, time.Now())
		}
	}
}

// quietPausePass pauses every eligible instance that has been idle for its
// QuietPauseAfter, and forgets state for instances that are gone.
func (m *Manager) quietPausePass(ctx context.Context, now time.Time) {
	m.mu.Lock()
	candidates := make([]quietCandidate, 0, len(m.live))
	live := make(map[string]*Instance, len(m.live))
	for id, inst := range m.live {
		live[id] = inst
		if m.quietEligibleLocked(id, inst) {
			candidates = append(candidates, quietCandidate{id: id, inst: inst, after: inst.QuietPauseAfter})
		}
	}
	m.mu.Unlock()

	for _, c := range candidates {
		m.quietPauseOne(ctx, c, now)
	}

	paused := 0
	m.quietMu.Lock()
	for id, st := range m.quietStates {
		if live[id] != st.inst {
			delete(m.quietStates, id)
			continue
		}
		if st.paused.Load() {
			paused++
		}
	}
	m.quietMu.Unlock()
	m.quietMetrics.setPaused(paused)
}

// quietStateFor returns the pause state of this incarnation of id, starting
// a fresh one when the id belongs to a new VM.
func (m *Manager) quietStateFor(id string, inst *Instance, now time.Time) *quietState {
	m.quietMu.Lock()
	defer m.quietMu.Unlock()
	if m.quietStates == nil {
		m.quietStates = make(map[string]*quietState)
	}
	st := m.quietStates[id]
	if st == nil || st.inst != inst {
		st = &quietState{inst: inst, seenAt: now}
		m.quietStates[id] = st
	}
	return st
}

// quietStateIfAny returns the pause state of the live incarnation of id, or
// nil. State left by an earlier VM with the same id does not count.
func (m *Manager) quietStateIfAny(id string) *quietState {
	m.quietMu.Lock()
	st := m.quietStates[id]
	m.quietMu.Unlock()
	if st == nil {
		return nil
	}
	m.mu.Lock()
	current := m.live[id] == st.inst
	m.mu.Unlock()
	if !current {
		return nil
	}
	return st
}

// quietIdle reports whether this incarnation has had nothing in flight for
// at least after: since its last request ended, and since the pause pass
// first saw it.
func (m *Manager) quietIdle(id string, st *quietState, after time.Duration, now time.Time) bool {
	inflight, since, seen := m.quietActivity.Quiet(id)
	if inflight > 0 {
		return false
	}
	ref := st.seenAt
	if seen && since.After(ref) {
		ref = since
	}
	return now.Sub(ref) >= after
}

func (m *Manager) quietPauseOne(ctx context.Context, c quietCandidate, now time.Time) {
	st := m.quietStateFor(c.id, c.inst, now)
	if st.paused.Load() || !m.quietIdle(c.id, st, c.after, now) {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.paused.Load() {
		return
	}
	// Announce the pause first: from here a request that marks activity
	// either shows up in the checks below or waits on st.mu to resume.
	st.paused.Store(true)
	m.mu.Lock()
	inst := m.live[c.id]
	eligible := inst == c.inst && m.quietEligibleLocked(c.id, inst)
	var lease Lease
	if eligible {
		lease = inst.Lease
	}
	m.mu.Unlock()
	if !eligible || !m.quietIdle(c.id, st, c.after, now) {
		st.paused.Store(false)
		return
	}
	pauser := m.vmm.(quietPauser)
	pauseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), quietVMMTimeout)
	err := pauser.PauseVM(pauseCtx, lease)
	cancel()
	if err != nil {
		m.quietMetrics.observePause("error")
		// The call can fail after Firecracker paused the guest: the timeout
		// can fire late, and the API client resends a request after a
		// transport error. Resume so the guest is known to run; ResumeVM
		// treats a running VM as success.
		resumeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), quietVMMTimeout)
		rerr := m.resumeWithRehearsalNetwork(resumeCtx, c.inst)
		cancel()
		if rerr != nil {
			// The guest may still be paused. It stays marked paused, with no
			// start, so the next request resumes it first and the pause
			// ledger never counts it as unbilled time.
			m.log.Warn("quiet pause: pause failed, then the resume after it failed; the next request resumes the guest",
				"instance", c.id, "err", err, "resume_err", rerr)
			return
		}
		st.paused.Store(false)
		st.epoch.Add(1)
		m.log.Warn("quiet pause: pause failed; instance keeps running", "instance", c.id, "err", err)
		return
	}
	st.since = time.Now()
	st.epoch.Add(1)
	m.quietMetrics.observePause("paused")
}

// QuietResume resumes a quiet-paused instance and returns once its vCPUs
// run. It is a no-op for an instance that is not paused, and for a frozen
// one (ADR-1151): only Thaw resumes a frozen guest, after schedd admits it.
// Callers mark activity (or register their instance flight) before calling
// it. The resume outlives the caller's cancellation: a half-done resume
// would leave the next request to find the guest still paused.
func (m *Manager) QuietResume(ctx context.Context, instance string) error {
	st := m.quietStateIfAny(instance)
	if st == nil || !st.paused.Load() {
		return nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.paused.Load() {
		return nil
	}
	m.mu.Lock()
	inst := m.live[instance]
	var lease Lease
	frozen := false
	if inst != nil {
		lease, frozen = inst.Lease, inst.Frozen
	}
	m.mu.Unlock()
	if inst == nil || inst != st.inst {
		// Gone, or replaced by a new VM that was never paused: teardown owns
		// the old process.
		st.paused.Store(false)
		return nil
	}
	if frozen {
		return nil
	}
	m.mu.Lock()
	migrating := functionMigrationActive(inst)
	m.mu.Unlock()
	if migrating {
		return ErrInstanceBusy
	}
	started := time.Now()
	resumeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), quietVMMTimeout)
	err := m.ensureRehearsalNetwork(resumeCtx, inst)
	if err == nil {
		err = m.vmm.ResumeVM(resumeCtx, lease)
	}
	cancel()
	if err != nil {
		m.quietMetrics.observeResumeError()
		return fmt.Errorf("quiet resume %s: %w", instance, err)
	}
	if !st.since.IsZero() {
		st.lastStarted, st.lastEnded = st.since, time.Now()
		st.pausedTotal += st.lastEnded.Sub(st.lastStarted)
	}
	st.paused.Store(false)
	st.since = time.Time{}
	st.epoch.Add(1)
	m.quietMetrics.observeResume(time.Since(started))
	return nil
}

// quietResumeBestEffort resumes a quiet-paused instance before an
// operation that needs a running guest, logging rather than failing: the
// operation's own error is more useful than this one.
func (m *Manager) quietResumeBestEffort(ctx context.Context, instance string) {
	if err := m.QuietResume(ctx, instance); err != nil {
		m.log.Warn("quiet pause: resume before operation failed", "instance", instance, "err", err)
	}
}

// QuietPauseState reports whether an instance is quiet-paused and since
// when, for InstanceStats.
func (m *Manager) QuietPauseState(instance string) (paused bool, since time.Time) {
	st := m.quietStateIfAny(instance)
	if st == nil || !st.paused.Load() {
		return false, time.Time{}
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.paused.Load() {
		return false, time.Time{}
	}
	return true, st.since
}

// QuietPauseReport is the quiet pause record of an instance's live
// incarnation that vmmd reports in InstanceStats. schedd keeps the pauses
// in the ledger meterd subtracts from billed seconds (ADR-20261023171905).
type QuietPauseReport struct {
	// Paused and Since describe the current pause; Since is zero unless
	// Paused, and while a failed pause leaves the guest's state unknown
	// (the next resume settles it, and no pause is recorded).
	Paused bool
	Since  time.Time
	// LastStarted and LastEnded bound the most recent completed pause;
	// both are zero until the first resume.
	LastStarted, LastEnded time.Time
	// PausedTotal sums this incarnation's completed pauses.
	PausedTotal time.Duration
}

// QuietPauseReport returns the quiet pause record of instance's live
// incarnation. An instance without one, or replaced by a new VM, reports
// the zero record.
func (m *Manager) QuietPauseReport(instance string) QuietPauseReport {
	st := m.quietStateIfAny(instance)
	if st == nil {
		return QuietPauseReport{}
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	r := QuietPauseReport{LastStarted: st.lastStarted, LastEnded: st.lastEnded, PausedTotal: st.pausedTotal}
	if st.paused.Load() {
		r.Paused, r.Since = true, st.since
	}
	return r
}

// QuietGate lets a host-side probe loop skip a quiet-paused guest. A probe
// reads it before probing and again after: if the guest was paused at
// either point, or the epoch moved in between, the outcome says nothing
// about the guest's health and must be dropped.
func (m *Manager) QuietGate(instance string) (paused bool, epoch uint64) {
	st := m.quietStateIfAny(instance)
	if st == nil {
		return false, 0
	}
	return st.paused.Load(), st.epoch.Load()
}

// QuietPauseMetrics owns the ADR-1151 quiet pause series.
type QuietPauseMetrics struct {
	pauses  *prometheus.CounterVec
	resumes prometheus.Histogram
	errors  prometheus.Counter
	paused  prometheus.Gauge
}

// NewQuietPauseMetrics registers vmmd_quiet_pause_total,
// vmmd_quiet_resume_seconds, vmmd_quiet_resume_errors_total and
// vmmd_quiet_paused_instances on reg.
func NewQuietPauseMetrics(reg prometheus.Registerer) *QuietPauseMetrics {
	resumes := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "vmmd_quiet_resume_seconds",
		Help:    "Time to resume a quiet-paused instance when a request arrives (ADR-1151).",
		Buckets: []float64{0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.5},
	})
	m := &QuietPauseMetrics{
		pauses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vmmd_quiet_pause_total",
			Help: "Quiet pause attempts by outcome (paused, error) (ADR-1151).",
		}, []string{"outcome"}),
		resumes: resumes,
		errors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "vmmd_quiet_resume_errors_total",
			Help: "Quiet resumes that failed; the request that triggered one fails or retries (ADR-1151).",
		}),
		paused: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "vmmd_quiet_paused_instances",
			Help: "Instances held quiet-paused between requests on this node (ADR-1151).",
		}),
	}
	reg.MustRegister(m.pauses, resumes, m.errors, m.paused)
	return m
}

func (q *QuietPauseMetrics) observePause(outcome string) {
	if q != nil {
		q.pauses.WithLabelValues(outcome).Inc()
	}
}

func (q *QuietPauseMetrics) observeResume(d time.Duration) {
	if q != nil {
		q.resumes.Observe(d.Seconds())
	}
}

func (q *QuietPauseMetrics) observeResumeError() {
	if q != nil {
		q.errors.Inc()
	}
}

func (q *QuietPauseMetrics) setPaused(n int) {
	if q != nil {
		q.paused.Set(float64(n))
	}
}
