package fcvm

// Idle ladder L3 and L4 (ADR-20261107122928 §1, §2): vmmd compresses the
// cold pages of an instance quiet pause holds paused. On every tick the
// compression pass works through each paused, ladder-eligible instance:
//
//   - After the guest ran, and at most once per IdleLadderColdAge1Seconds,
//     it ages the VM's memory (lru_gen.go), so the youngest generation holds
//     what the guest touched last.
//   - At IdleLadderColdAge1Seconds of quiet (L3) it compresses every
//     generation but the youngest IdleLadderCold1KeepGenerations, and at
//     IdleLadderColdAge2Seconds (L4) every one but the youngest
//     IdleLadderCold2KeepGenerations. Each app's ceiling adds generations to
//     keep.
//   - A request resumes the guest as at L1/L2. The pages it touches fault
//     back from zswap, and vmmd charges that wake the resume plus
//     IdleLadderFaultMicrosPerPage for each refault until the next pause.
//   - A wake over its budget, the lower of IdleLadderWakeBudgetMillis and the
//     instance's own restore time, makes the app keep one more generation.
//
// Compression never holds quietState.mu, so a resume never waits on it: it
// works in chunks and stops at the first chunk after a resume. Swap opens
// to the scope only for the compression and closes after the next resume,
// as for frozen VMs (frozen_swap.go).

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
)

// DefaultIdleCompressTick is how often the compression pass runs.
const DefaultIdleCompressTick = time.Second

// coldPager is the JailerVMM side of cold-page compression.
type coldPager interface {
	ColdPagesReady(l Lease) bool
	AgeVMMemory(l Lease) error
	EvictColdVMMemory(l Lease, keep int, stop func() bool) (VMMemoryReclaim, error)
	VMRefaultPages(l Lease) (int64, error)
	SetFrozenSwap(l Lease, frozen bool) error
}

// coldState is one incarnation's compression record, in its quietState.
// mu is a leaf lock, taken after quietState.mu where both are held. Only the
// compression pass changes it, except wake, which QuietResume starts.
type coldState struct {
	mu sync.Mutex
	// agedAt and agedEpoch are the last aging pass and the quiet epoch it
	// ran in: the guest has run since whenever the epoch moved.
	agedAt    time.Time
	agedEpoch uint64
	// level is the deepest compression done during the pause at
	// levelEpoch; tried is the deepest level attempted then.
	level, tried IdleLevel
	levelEpoch   uint64
	// compressed is what the last compression took out of the cgroup.
	compressed int64
	// swapOpen is whether the scope's swap is open for a compression.
	swapOpen bool
	// wake measures the wake from a compressed level until the next pause.
	wake *coldWake
}

// coldWake is one wake from L3 or L4 being measured.
type coldWake struct {
	level IdleLevel
	// epoch is the quiet epoch the resume moved to; a later epoch means the
	// guest paused again and the wake is over.
	epoch  uint64
	base   int64
	resume time.Duration
	budget time.Duration
}

// coldCeiling is one app's compression ceiling: how many more generations
// than the defaults its instances keep.
type coldCeiling struct {
	extra int
	good  int
}

// coldLevelLocked reports the compressed level of the current pause, or
// IdleRunning. The caller holds cs.mu.
func (cs *coldState) coldLevelLocked(epoch uint64) IdleLevel {
	if cs.levelEpoch != epoch {
		return IdleRunning
	}
	return cs.level
}

// RunIdleLadderCompress runs the compression pass every tick until ctx ends.
// It returns at once unless the idle ladder is on with compression.
func (m *Manager) RunIdleLadderCompress(ctx context.Context) {
	if !m.idleCompressOn {
		return
	}
	if _, ok := m.vmm.(coldPager); !ok {
		return
	}
	ticker := time.NewTicker(DefaultIdleCompressTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.idleCompressPass(time.Now())
		}
	}
}

// coldCandidate is a live instance the compression pass looks at.
type coldCandidate struct {
	id     string
	inst   *Instance
	lease  Lease
	appID  string
	budget time.Duration
	frozen bool
}

func (m *Manager) idleCompressPass(now time.Time) {
	pager, ok := m.vmm.(coldPager)
	if !ok || !m.idleCompressOn {
		return
	}
	m.mu.Lock()
	cands := make([]coldCandidate, 0, len(m.live))
	for id, inst := range m.live {
		if m.idleLadderEligibleLocked(id, inst) && !inst.Lease.IsBuilder {
			cands = append(cands, coldCandidate{id: id, inst: inst, lease: inst.Lease, appID: inst.AppID,
				budget: coldWakeBudget(inst.RestoreMs), frozen: inst.Frozen})
		}
	}
	m.mu.Unlock()
	var held int64
	for _, c := range cands {
		st := m.quietStateIfAny(c.id)
		if st == nil || st.inst != c.inst {
			continue
		}
		held += m.idleCompressOne(pager, st, c, now)
	}
	m.idleCompressMetrics.setCompressed(held)
}

// coldWakeBudget is a wake's budget: IdleLadderWakeBudgetMillis, or the
// instance's restore time when it was restored faster than that.
func coldWakeBudget(restoreMs int64) time.Duration {
	budget := time.Duration(api.IdleLadderWakeBudgetMillis) * time.Millisecond
	if restoreMs > 0 {
		budget = min(budget, time.Duration(restoreMs)*time.Millisecond)
	}
	return budget
}

// idleCompressOne advances one instance and returns the bytes it holds
// compressed now.
func (m *Manager) idleCompressOne(pager coldPager, st *quietState, c coldCandidate, now time.Time) int64 {
	cs := &st.cold
	epoch := st.epoch.Load()
	paused := st.paused.Load() && !c.frozen
	cs.mu.Lock()
	swapOpen := cs.swapOpen
	wake := cs.wake
	if c.frozen {
		// schedd froze it: what it faults back after the thaw is not this
		// wake's doing.
		cs.wake, wake = nil, nil
	}
	if wake != nil && paused && epoch != wake.epoch {
		cs.wake = nil
	}
	cs.mu.Unlock()
	if wake != nil && paused && epoch != wake.epoch {
		m.finishColdWake(pager, c, wake)
	}
	if !paused {
		// The guest runs (or schedd froze it, which owns its swap then):
		// close what a compression opened.
		if swapOpen && !c.frozen {
			m.setColdSwap(pager, cs, c.lease, false)
		}
		return 0
	}
	m.ageIfRan(pager, st, c, epoch, now)
	target := m.coldTarget(st, c.id, now)
	cs.mu.Lock()
	current, tried := cs.coldLevelLocked(epoch), IdleRunning
	if cs.levelEpoch == epoch {
		tried = cs.tried
	}
	// Generations aged before the guest last ran may hold the pages it just
	// brought back; compress only after an aging pass in this pause.
	aged := cs.agedEpoch == epoch
	cs.mu.Unlock()
	if aged && target > current && target > tried {
		m.compressTo(pager, st, c, epoch, target)
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.coldLevelLocked(epoch) >= IdleCold1 {
		return cs.compressed
	}
	return 0
}

// ageIfRan ages the VM's memory once the guest has run since the last pass,
// at most once per IdleLadderColdAge1Seconds.
func (m *Manager) ageIfRan(pager coldPager, st *quietState, c coldCandidate, epoch uint64, now time.Time) {
	cs := &st.cold
	cs.mu.Lock()
	due := cs.agedEpoch != epoch && (cs.agedAt.IsZero() || now.Sub(cs.agedAt) >= api.IdleLadderColdAge1Seconds*time.Second)
	cs.mu.Unlock()
	if !due {
		return
	}
	started := time.Now()
	err := pager.AgeVMMemory(c.lease)
	m.idleCompressMetrics.addSeconds(time.Since(started))
	if err != nil {
		m.idleCompressMetrics.observe(IdleRunning, "age_error")
		m.log.Warn("idle ladder: age VM memory", "instance", c.id, "err", err)
		return
	}
	cs.mu.Lock()
	cs.agedAt, cs.agedEpoch = now, epoch
	cs.mu.Unlock()
}

// coldTarget is the compressed level an instance's quiet time reaches.
func (m *Manager) coldTarget(st *quietState, id string, now time.Time) IdleLevel {
	from := st.seenAt
	if m.quietActivity != nil {
		if inflight, last, ok := m.quietActivity.Quiet(id); ok {
			if inflight > 0 {
				return IdleRunning
			}
			if last.After(from) {
				from = last
			}
		}
	}
	switch quiet := now.Sub(from); {
	case quiet >= api.IdleLadderColdAge2Seconds*time.Second:
		return IdleCold2
	case quiet >= api.IdleLadderColdAge1Seconds*time.Second:
		return IdleCold1
	}
	return IdleRunning
}

// compressTo compresses a paused instance to target, within its app's
// ceiling. A resume during the compression stops it; the level is recorded
// only if the guest is still paused in the same pause.
func (m *Manager) compressTo(pager coldPager, st *quietState, c coldCandidate, epoch uint64, target IdleLevel) {
	cs := &st.cold
	cs.mu.Lock()
	if cs.levelEpoch != epoch {
		cs.level, cs.tried, cs.levelEpoch, cs.compressed = IdleRunning, IdleRunning, epoch, 0
	}
	cs.tried = target
	cs.mu.Unlock()
	keep := api.IdleLadderCold1KeepGenerations
	if target == IdleCold2 {
		keep = api.IdleLadderCold2KeepGenerations
	}
	keep += m.coldExtra(c.appID)
	if keep >= lruGenMaxGens {
		m.idleCompressMetrics.observe(target, "ceiling")
		return
	}
	if !pager.ColdPagesReady(c.lease) {
		m.idleCompressMetrics.observe(target, "unavailable")
		return
	}
	m.setColdSwap(pager, cs, c.lease, true)
	stop := func() bool { return !st.paused.Load() || st.epoch.Load() != epoch }
	started := time.Now()
	r, err := pager.EvictColdVMMemory(c.lease, keep, stop)
	m.idleCompressMetrics.addSeconds(time.Since(started))
	if err != nil {
		m.idleCompressMetrics.observe(target, "error")
		m.log.Warn("idle ladder: compress cold pages", "instance", c.id, "level", target.String(), "err", err)
		return
	}
	if stop() {
		m.idleCompressMetrics.observe(target, "interrupted")
		return
	}
	cs.mu.Lock()
	cs.level, cs.compressed = target, cs.compressed+max(r.BeforeBytes-r.AfterBytes, 0)
	cs.mu.Unlock()
	m.idleCompressMetrics.observe(target, "compressed")
	m.log.Debug("idle ladder: compressed cold pages", "instance", c.id, "level", target.String(), "keep", keep,
		"before_bytes", r.BeforeBytes, "after_bytes", r.AfterBytes, "ms", time.Since(started).Milliseconds())
}

// setColdSwap opens or closes the scope's swap for a compression.
func (m *Manager) setColdSwap(pager coldPager, cs *coldState, l Lease, open bool) {
	if err := pager.SetFrozenSwap(l, open); err != nil {
		m.log.Warn("idle ladder: set VM swap limit", "instance", l.Instance, "open", open, "err", err)
		return
	}
	cs.mu.Lock()
	cs.swapOpen = open
	cs.mu.Unlock()
}

// startColdWake records, before a resume, the refault count of a guest
// resuming from a compressed level. It returns nil for any other resume.
// The caller holds st.mu.
func (m *Manager) startColdWake(st *quietState, inst *Instance) *coldWake {
	if !m.idleCompressOn {
		return nil
	}
	pager, ok := m.vmm.(coldPager)
	if !ok {
		return nil
	}
	cs := &st.cold
	cs.mu.Lock()
	level := cs.coldLevelLocked(st.epoch.Load())
	cs.mu.Unlock()
	if level < IdleCold1 {
		return nil
	}
	base, err := pager.VMRefaultPages(inst.Lease)
	if err != nil {
		return nil
	}
	return &coldWake{level: level, base: base, budget: coldWakeBudget(inst.RestoreMs)}
}

// resumedColdWake runs once a resume took effect: it closes the swap a
// compression opened, so the running guest's memory stays resident, and
// stores a started wake.
func (m *Manager) resumedColdWake(st *quietState, inst *Instance, w *coldWake, resume time.Duration) {
	cs := &st.cold
	cs.mu.Lock()
	open := cs.swapOpen
	cs.mu.Unlock()
	if pager, ok := m.vmm.(coldPager); ok && open {
		m.setColdSwap(pager, cs, inst.Lease, false)
	}
	if w == nil {
		return
	}
	w.resume, w.epoch = resume, st.epoch.Load()
	cs.mu.Lock()
	cs.wake = w
	cs.mu.Unlock()
}

// finishColdWake charges a wake its resume and refaults, and moves the app's
// ceiling.
func (m *Manager) finishColdWake(pager coldPager, c coldCandidate, w *coldWake) {
	now, err := pager.VMRefaultPages(c.lease)
	if err != nil {
		return
	}
	cost := w.resume + time.Duration(max(now-w.base, 0)*api.IdleLadderFaultMicrosPerPage)*time.Microsecond
	over := cost > w.budget
	m.idleCompressMetrics.observeWake(w.level, cost, over)
	m.moveColdCeiling(c.appID, cost, w.budget)
}

// moveColdCeiling keeps one more generation for an app whose wake exceeded
// its budget, and gives one back after IdleLadderCeilingRelaxWakes wakes in
// a row under half of it.
func (m *Manager) moveColdCeiling(appID string, cost, budget time.Duration) {
	m.idleColdMu.Lock()
	defer m.idleColdMu.Unlock()
	if m.idleColdCeilings == nil {
		m.idleColdCeilings = make(map[string]coldCeiling)
	}
	c := m.idleColdCeilings[appID]
	switch {
	case cost > budget:
		c.extra, c.good = min(c.extra+1, api.IdleLadderMaxExtraKeepGenerations), 0
	case cost <= budget/2:
		if c.good++; c.good >= api.IdleLadderCeilingRelaxWakes {
			c.extra, c.good = max(c.extra-1, 0), 0
		}
	default:
		c.good = 0
	}
	if c == (coldCeiling{}) {
		delete(m.idleColdCeilings, appID)
		return
	}
	m.idleColdCeilings[appID] = c
}

func (m *Manager) coldExtra(appID string) int {
	m.idleColdMu.Lock()
	defer m.idleColdMu.Unlock()
	return m.idleColdCeilings[appID].extra
}

// coldLevel reports an instance's compressed level in its current pause.
func (st *quietState) coldLevel() IdleLevel {
	cs := &st.cold
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.coldLevelLocked(st.epoch.Load())
}

// IdleCompressMetrics owns the L3/L4 series (ADR-20261107122928 §12).
type IdleCompressMetrics struct {
	compressed   prometheus.Gauge
	seconds      prometheus.Counter
	compressions *prometheus.CounterVec
	wakes        *prometheus.HistogramVec
	exceeded     *prometheus.CounterVec
}

// NewIdleCompressMetrics registers the compression series on reg.
func NewIdleCompressMetrics(reg prometheus.Registerer) *IdleCompressMetrics {
	m := &IdleCompressMetrics{
		compressed: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "vmmd_idle_compressed_bytes",
			Help: "Memory the idle ladder's L3/L4 compression took out of quiet-paused VMs that are still paused (ADR-20261107122928).",
		}),
		seconds: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "vmmd_idle_compress_seconds_total",
			Help: "Time spent aging and compressing quiet-paused VMs' memory (ADR-20261107122928).",
		}),
		compressions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vmmd_idle_compressions_total",
			Help: "Idle ladder compression attempts by level and outcome: compressed, interrupted, ceiling, unavailable, error, age_error (ADR-20261107122928).",
		}, []string{"level", "outcome"}),
		wakes: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "vmmd_idle_wake_seconds",
			Help:    "Wakes from a compressed idle ladder level: the resume plus api.IdleLadderFaultMicrosPerPage for each page brought back before the next pause (ADR-20261107122928).",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.35, 0.5, 1, 2.5},
		}, []string{"level"}),
		exceeded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vmmd_idle_wake_budget_exceeded_total",
			Help: "Wakes from a compressed idle ladder level that exceeded their budget (ADR-20261107122928).",
		}, []string{"level"}),
	}
	if reg != nil {
		reg.MustRegister(m.compressed, m.seconds, m.compressions, m.wakes, m.exceeded)
	}
	return m
}

func (q *IdleCompressMetrics) setCompressed(bytes int64) {
	if q != nil {
		q.compressed.Set(float64(bytes))
	}
}

func (q *IdleCompressMetrics) addSeconds(d time.Duration) {
	if q != nil {
		q.seconds.Add(d.Seconds())
	}
}

func (q *IdleCompressMetrics) observe(level IdleLevel, outcome string) {
	if q != nil {
		q.compressions.WithLabelValues(level.String(), outcome).Inc()
	}
}

func (q *IdleCompressMetrics) observeWake(level IdleLevel, cost time.Duration, over bool) {
	if q == nil {
		return
	}
	q.wakes.WithLabelValues(level.String()).Observe(cost.Seconds())
	if over {
		q.exceeded.WithLabelValues(level.String()).Inc()
	}
}
