package fcvm

// The idle ladder (ADR-20261107122928, proposed). The ladder moves an idle
// request-serving instance down levels by how long it has had nothing in
// flight: L1 quiet pause, L2 a vmmd-local freeze, L3 and L4 compress the pages
// it left untouched, L5 snapshots it.
//
// Observe mode only measures: it tracks every eligible instance's idle time,
// whatever its declared pause, and records the level each would reach, the
// RAM held at each level, the level each request wakes from and the billed
// MB-seconds the unbilled levels would not bill.
//
// On adds the first two levels to the measurement, for the instances quiet
// pause already pauses (functions and between_requests apps): quiet pause
// waits IdleLadderQuietPauseAfterMillis (L1), and an instance quiet-paused
// and quiet for IdleLadderFreezeAfterSeconds is reported at L2 in
// InstanceStats, where measured admission charges its memory with the frozen
// floor. The guest stays
// quiet-paused and resumes on the next request as before; L2 changes only
// how admission counts it. The deeper levels are not built yet.
//
// Lock order: m.mu is released before idleMu is taken; idleMu is a leaf.

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
)

// DefaultIdleLadderTick is how often the observe pass samples idle time. It
// bounds how far a recorded idle gap or level boundary can be off.
const DefaultIdleLadderTick = 250 * time.Millisecond

// idleLadderMaxStep caps the time one pass credits to an idle instance, so
// a pass that ran late (a stalled host, a suspended test clock) does not
// count a long stretch it did not watch.
const idleLadderMaxStep = 5 * time.Second

// IdleLevel is a rung of the idle ladder.
type IdleLevel int

// The idle ladder's levels, from running down to a snapshot.
const (
	IdleRunning IdleLevel = iota
	IdleQuietPause
	IdleFreeze
	IdleCold1
	IdleCold2
	IdleSnapshot
	idleLevelCount
)

var idleLevelNames = [idleLevelCount]string{"running", "quiet_pause", "freeze", "cold_1", "cold_2", "snapshot"}

// String is the level's metric label.
func (l IdleLevel) String() string {
	if l < 0 || l >= idleLevelCount {
		return "unknown"
	}
	return idleLevelNames[l]
}

// IdleLevelFor is the level an instance reaches after idle with nothing in
// flight (pkg/api limits).
func IdleLevelFor(idle time.Duration) IdleLevel {
	switch {
	case idle >= api.IdleLadderSnapshotAfterSeconds*time.Second:
		return IdleSnapshot
	case idle >= api.IdleLadderColdAge2Seconds*time.Second:
		return IdleCold2
	case idle >= api.IdleLadderColdAge1Seconds*time.Second:
		return IdleCold1
	case idle >= api.IdleLadderFreezeAfterSeconds*time.Second:
		return IdleFreeze
	case idle >= api.IdleLadderQuietPauseAfterMillis*time.Millisecond:
		return IdleQuietPause
	}
	return IdleRunning
}

// idleLadderEligibleFor reports whether the observe pass measures an
// instance: one schedd marks as a request-serving workload, or one it asks to
// quiet-pause. The exclusions are quietPauseAfterFor's.
func idleLadderEligibleFor(req WakeRequest, qualificationOnly bool) bool {
	if !req.IdleLadderEligible && req.QuietPauseAfter <= 0 {
		return false
	}
	return !req.ExecutionOnly && !req.AppTaskOnly && !qualificationOnly &&
		!req.KeepPaused && req.ExportDir == "" && req.PrivateNetworkID == ""
}

// IdleLadderConfig turns on the idle ladder.
type IdleLadderConfig struct {
	Activity QuietActivity
	Metrics  *IdleLadderMetrics
	// Tick is the observe pass period; zero means DefaultIdleLadderTick.
	Tick time.Duration
	// On applies levels L1 and L2 to quiet-paused instances; without it the
	// ladder only observes. On needs quiet pause (WithQuietPause, not in
	// observe mode).
	On bool
	// CPU and Net feed the activity guard (idle_ladder_guard.go); without
	// both it stays off.
	CPU IdleLadderCPU
	Net IdleLadderNet
}

// WithIdleLadder enables the idle ladder. Without it RunIdleLadderObserve
// returns at once and IdleLadderLevel reports IdleRunning.
func (m *Manager) WithIdleLadder(cfg IdleLadderConfig) *Manager {
	m.idleActivity = cfg.Activity
	m.idleMetrics = cfg.Metrics
	m.idleLadderOn = cfg.On
	m.idleCPU, m.idleNet = cfg.CPU, cfg.Net
	m.idleTick = cfg.Tick
	if m.idleTick <= 0 {
		m.idleTick = DefaultIdleLadderTick
	}
	if cfg.Metrics != nil {
		cfg.Metrics.src = m
	}
	return m
}

// idleLadderState is one live incarnation's idle gap. Like quietState it
// remembers the *Instance it describes, so an instance id reused by a new VM
// starts over.
type idleLadderState struct {
	inst *Instance
	// seenAt is when the pass first saw this incarnation; idle time is never
	// measured from before it.
	seenAt time.Time
	// idleFrom starts the current idle gap and idle is its length at the
	// last pass; idleFrom is zero while something is in flight.
	idleFrom time.Time
	idle     time.Duration
	// guard is the activity guard's view of an app quiet pause does not
	// pause (idle_ladder_guard.go).
	guard idleGuardState
}

// idleLadderCandidate is an eligible live instance, read under m.mu.
type idleLadderCandidate struct {
	id     string
	inst   *Instance
	lease  Lease
	frozen bool
	tail   bool
	// guarded marks an app quiet pause does not pause: the activity guard
	// decides whether it is idle.
	guarded bool
}

// RunIdleLadderObserve runs the observe pass every tick until ctx ends.
func (m *Manager) RunIdleLadderObserve(ctx context.Context) {
	if m.idleActivity == nil {
		return
	}
	ticker := time.NewTicker(m.idleTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.idleLadderPass(time.Now())
		}
	}
}

// idleLadderEligibleLocked reports whether the observe pass measures a live
// instance now. A frozen instance stays measured: it is idle and holds its
// memory. The caller holds m.mu.
func (m *Manager) idleLadderEligibleLocked(id string, inst *Instance) bool {
	if inst == nil || !inst.IdleLadderEligible || inst.Paused || inst.IsJob || inst.AgentRunOnly ||
		inst.ExecutionOnly || inst.AppTaskOnly || inst.QualificationOnly {
		return false
	}
	if m.frameworkReadyRuns[id] != nil {
		return false
	}
	return m.instanceStops[id] == nil && m.pendingCleanup[id] == nil
}

// idleLadderPass samples every eligible instance's idle time once.
func (m *Manager) idleLadderPass(now time.Time) {
	m.mu.Lock()
	candidates := make([]idleLadderCandidate, 0, len(m.live))
	for id, inst := range m.live {
		if m.idleLadderEligibleLocked(id, inst) {
			candidates = append(candidates, idleLadderCandidate{
				id: id, inst: inst, lease: inst.Lease, frozen: inst.Frozen, tail: inst.TailCount > 0,
				guarded: inst.QuietPauseAfter <= 0,
			})
		}
	}
	m.mu.Unlock()

	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	step := time.Duration(0)
	if !m.idleLast.IsZero() {
		step = min(max(now.Sub(m.idleLast), 0), idleLadderMaxStep)
	}
	m.idleLast = now
	if m.idleStates == nil {
		m.idleStates = make(map[string]*idleLadderState)
	}
	current := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		current[c.id] = true
		st := m.idleStates[c.id]
		if st == nil || st.inst != c.inst {
			st = &idleLadderState{inst: c.inst, seenAt: now}
			m.idleStates[c.id] = st
		}
		m.idleLadderSample(st, c, now, step)
	}
	for id := range m.idleStates {
		if !current[id] {
			delete(m.idleStates, id)
		}
	}
}

// idleLadderSample advances one instance's idle gap. A gap ends when
// something is in flight or a request came and went since the last pass;
// it is recorded at the level it had reached. The caller holds idleMu.
func (m *Manager) idleLadderSample(st *idleLadderState, c idleLadderCandidate, now time.Time, step time.Duration) {
	m.idleGuardRecord(st, c, now)
	inflight, since, seen := m.idleActivity.Quiet(c.id)
	from := st.seenAt
	if seen && since.After(from) {
		from = since
	}
	busy := inflight > 0 || c.tail
	if !st.idleFrom.IsZero() && (busy || from.After(st.idleFrom)) {
		m.idleMetrics.observeGap(st.idle)
		m.idleGuardEnd(st, idleGuardEndRequest, now)
		st.idleFrom, st.idle = time.Time{}, 0
	}
	if busy {
		st.guard.verdict = idleGuardNone
		return
	}
	if st.idleFrom.IsZero() {
		st.idleFrom = from
	}
	st.idle = max(now.Sub(st.idleFrom), 0)
	m.idleGuardStep(st, c, now, step)
	// Billed today, unbilled on the ladder: idle time past L1 while the
	// instance is not frozen (a frozen instance is already unbilled).
	if c.frozen || step <= 0 {
		return
	}
	past := st.idle - api.IdleLadderQuietPauseAfterMillis*time.Millisecond
	m.idleMetrics.observeUnbilled(c.lease, min(step, max(past, 0)))
}

// quietPauseAfter is how long quiet pause waits before pausing inst: its own
// delay, or the ladder's L1 delay when idle_ladder is on and that is shorter.
func (m *Manager) quietPauseAfter(inst *Instance) time.Duration {
	after := inst.QuietPauseAfter
	if m.idleLadderOn && after > 0 {
		after = min(after, api.IdleLadderQuietPauseAfterMillis*time.Millisecond)
	}
	return after
}

// IdleLadderLevel is the level vmmd holds an instance at while idle_ladder is
// on: IdleQuietPause while quiet pause holds it paused, IdleFreeze once it
// has also had nothing in flight for IdleLadderFreezeAfterSeconds. The deeper
// levels are not built, so IdleFreeze is the most it reports. It is
// IdleRunning when the ladder is off and for an instance that is running, is
// an agent run, or whose last pause failed (its state is unknown).
func (m *Manager) IdleLadderLevel(instance string, now time.Time) IdleLevel {
	if !m.idleLadderOn || m.quietActivity == nil {
		return IdleRunning
	}
	st := m.quietStateIfAny(instance)
	if st == nil || st.inst.AgentRunOnly {
		return IdleRunning
	}
	st.mu.Lock()
	paused, since := st.paused.Load(), st.since
	st.mu.Unlock()
	if !paused || since.IsZero() {
		return IdleRunning
	}
	from := st.seenAt
	if _, last, ok := m.quietActivity.Quiet(instance); ok && last.After(from) {
		from = last
	}
	if now.Sub(from) >= api.IdleLadderFreezeAfterSeconds*time.Second {
		return IdleFreeze
	}
	return IdleQuietPause
}

// idleLadderSnapshot is one measured instance at scrape time.
type idleLadderSnapshot struct {
	lease Lease
	level IdleLevel
}

// IdleLadderLevels returns how many measured instances sit at each level and
// the VM cgroup memory.current they hold there, read now.
func (m *Manager) IdleLadderLevels() (counts [idleLevelCount]int, resident [idleLevelCount]int64) {
	m.idleMu.Lock()
	snaps := make([]idleLadderSnapshot, 0, len(m.idleStates))
	for _, st := range m.idleStates {
		snaps = append(snaps, idleLadderSnapshot{lease: st.inst.Lease, level: IdleLevelFor(st.idle)})
	}
	m.idleMu.Unlock()
	reader, _ := m.vmm.(vmResidentReader)
	for _, s := range snaps {
		counts[s.level]++
		if reader == nil {
			continue
		}
		if b, err := reader.VMResidentBytes(s.lease); err == nil {
			resident[s.level] += b
		}
	}
	return counts, resident
}

// IdleLadderSource is what the level gauges read at scrape time; *Manager
// implements it.
type IdleLadderSource interface {
	IdleLadderLevels() (counts [idleLevelCount]int, resident [idleLevelCount]int64)
	IdleGuardVerdicts() (counts [idleGuardVerdictCount]int)
}

// IdleLadderMetrics owns the idle ladder observe mode series.
type IdleLadderMetrics struct {
	src       IdleLadderSource
	instances *prometheus.Desc
	resident  *prometheus.Desc
	wakes     *prometheus.CounterVec
	gaps      prometheus.Histogram
	unbilled  *prometheus.CounterVec

	// The activity guard (idle_ladder_guard.go).
	guardInstances *prometheus.Desc
	guardPauses    prometheus.Counter
	guardResumes   *prometheus.CounterVec
	guardDurations prometheus.Histogram
	guardHeld      *prometheus.CounterVec
}

// NewIdleLadderMetrics registers vmmd_idle_ladder_observed_instances,
// vmmd_idle_ladder_observed_resident_bytes,
// vmmd_idle_ladder_observed_wakes_total,
// vmmd_idle_ladder_observed_gap_seconds and
// vmmd_idle_ladder_observed_unbilled_mb_seconds_total on reg. The level
// gauges read the Manager passed to WithIdleLadder.
func NewIdleLadderMetrics(reg prometheus.Registerer) *IdleLadderMetrics {
	m := &IdleLadderMetrics{
		instances: prometheus.NewDesc("vmmd_idle_ladder_observed_instances",
			"Request-serving instances on this node by the idle ladder level their idle time reaches (ADR-20261107122928, observe mode).",
			[]string{"level"}, nil),
		resident: prometheus.NewDesc("vmmd_idle_ladder_observed_resident_bytes",
			"Sum of VM cgroup memory.current of the measured instances, by the idle ladder level they reach (ADR-20261107122928, observe mode).",
			[]string{"level"}, nil),
		wakes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vmmd_idle_ladder_observed_wakes_total",
			Help: "Idle gaps of at least the quiet pause level ended by a request, by the deepest idle ladder level the gap reached (ADR-20261107122928, observe mode).",
		}, []string{"level"}),
		gaps: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "vmmd_idle_ladder_observed_gap_seconds",
			Help:    "Length of each idle gap of at least the quiet pause level that a request ended (ADR-20261107122928, observe mode).",
			Buckets: []float64{1, 2, 5, 10, 30, 60, 120, 180, 300, 600, 1200, 1800, 3600},
		}),
		unbilled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vmmd_idle_ladder_observed_unbilled_mb_seconds_total",
			Help: "Billable MB-seconds (VM RAM + 8 MB) of idle time past the quiet pause level on instances that were not frozen, by plan: what the idle ladder's unbilled levels would not bill (ADR-20261107122928, observe mode).",
		}, []string{"plan"}),
		guardInstances: prometheus.NewDesc("vmmd_idle_ladder_guard_observed_instances",
			"Apps quiet pause does not pause, by the activity guard's verdict: paused (it would hold them paused), cpu or net (their own work kept them running), unknown (no full window of counters), none (busy or idle less than 1 s) (ADR-20261107122928, observe mode).",
			[]string{"verdict"}, nil),
		guardPauses: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "vmmd_idle_ladder_guard_observed_pauses_total",
			Help: "Pauses the idle ladder's activity guard would have made on apps quiet pause does not pause (ADR-20261107122928, observe mode).",
		}),
		guardResumes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vmmd_idle_ladder_guard_observed_resumes_total",
			Help: "Ends of the pauses the activity guard would have made, by cause: request, inbound (traffic reached the guest; a real pause resumes on it), background (the app used CPU or sent traffic on its own, work a real pause would have held back), frozen (ADR-20261107122928, observe mode).",
		}, []string{"cause"}),
		guardDurations: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "vmmd_idle_ladder_guard_observed_pause_seconds",
			Help:    "How long each pause the activity guard would have made would have lasted (ADR-20261107122928, observe mode).",
			Buckets: []float64{1, 2, 5, 10, 30, 60, 120, 180, 300, 600, 1200, 1800, 3600},
		}),
		guardHeld: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vmmd_idle_ladder_guard_observed_mb_seconds_total",
			Help: "Billable MB-seconds (VM RAM + 8 MB) of the pauses the activity guard would have made, by plan: what unbilled pauses of apps would not bill (ADR-20261107122928, observe mode).",
		}, []string{"plan"}),
	}
	for l := IdleQuietPause; l < idleLevelCount; l++ {
		m.wakes.WithLabelValues(l.String())
	}
	for _, cause := range []string{idleGuardEndRequest, idleGuardEndInbound, idleGuardEndBackground, idleGuardEndFrozen} {
		m.guardResumes.WithLabelValues(cause)
	}
	reg.MustRegister(m, m.wakes, m.gaps, m.unbilled, m.guardPauses, m.guardResumes, m.guardDurations, m.guardHeld)
	return m
}

// Describe implements prometheus.Collector for the level gauges.
func (q *IdleLadderMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- q.instances
	ch <- q.resident
	ch <- q.guardInstances
}

// Collect implements prometheus.Collector for the level gauges.
func (q *IdleLadderMetrics) Collect(ch chan<- prometheus.Metric) {
	if q.src == nil {
		return
	}
	counts, resident := q.src.IdleLadderLevels()
	for l := IdleRunning; l < idleLevelCount; l++ {
		ch <- prometheus.MustNewConstMetric(q.instances, prometheus.GaugeValue, float64(counts[l]), l.String())
		ch <- prometheus.MustNewConstMetric(q.resident, prometheus.GaugeValue, float64(resident[l]), l.String())
	}
	verdicts := q.src.IdleGuardVerdicts()
	for v := idleGuardNone; v < idleGuardVerdictCount; v++ {
		ch <- prometheus.MustNewConstMetric(q.guardInstances, prometheus.GaugeValue, float64(verdicts[v]), idleGuardVerdictNames[v])
	}
}

// observeGap records an idle gap a request ended, if it reached L1.
func (q *IdleLadderMetrics) observeGap(idle time.Duration) {
	level := IdleLevelFor(idle)
	if q == nil || level == IdleRunning {
		return
	}
	q.wakes.WithLabelValues(level.String()).Inc()
	q.gaps.Observe(idle.Seconds())
}

// observeUnbilled adds d of billed idle time past L1 for a VM.
func (q *IdleLadderMetrics) observeUnbilled(l Lease, d time.Duration) {
	if q == nil || d <= 0 || l.MemoryMaxMiB <= 0 {
		return
	}
	q.unbilled.WithLabelValues(string(l.Plan)).Add(float64(l.MemoryMaxMiB+api.PerVMOverheadMB) * d.Seconds())
}

func (q *IdleLadderMetrics) observeGuardPause() {
	if q != nil {
		q.guardPauses.Inc()
	}
}

func (q *IdleLadderMetrics) observeGuardResume(cause string, d time.Duration) {
	if q == nil {
		return
	}
	q.guardResumes.WithLabelValues(cause).Inc()
	q.guardDurations.Observe(d.Seconds())
}

// observeGuardHeld adds d of a pause the guard would hold for a VM.
func (q *IdleLadderMetrics) observeGuardHeld(l Lease, d time.Duration) {
	if q == nil || d <= 0 || l.MemoryMaxMiB <= 0 {
		return
	}
	q.guardHeld.WithLabelValues(string(l.Plan)).Add(float64(l.MemoryMaxMiB+api.PerVMOverheadMB) * d.Seconds())
}
