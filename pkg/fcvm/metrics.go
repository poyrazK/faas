// Package fcvm — Prometheus gauges for the §12 dashboard row. vmmd and
// schedd both expose `/metrics`; these gauges are added to whichever
// daemon owns the underlying signal. Splitting ownership keeps the
// "only the owner reads the source" invariant intact (spec §Component
// ownership):
//
//   - schedd owns the snapshots table → fcvm_snapshot_fleet_avg_bytes,
//     fcvm_snapshot_fleet_p95_bytes. (Snapshot sizes are persisted in
//     schedd, not vmmd — vmmd's Snapshot() returns a one-shot
//     SnapshotInfo, the persistence path is schedd's pause-and-snapshot
//     handler that follows the VMM call.)
//
//   - schedd owns the admission ledger and the `instances` table →
//     fcvm_resident_ram_pct (Σ ram_mb over live instances /
//     RAMAdmissionCeilingMB).
//
//   - schedd shells out to `lvs` for fcvm_lv_fc_used_pct (the
//     filesystem the apps live on). vmmd could also do this, but
//     schedd already runs periodic work and avoids a second ticker.
//
//   - vmmd owns the wake/restore path → vmmd_cold_boot_fallback_total
//     (counter; incremented in Manager.bringUp when restore fails and
//     the instance cold-boots instead, ADR-005).
//
// Naming follows ADR-015's "<daemon>_" prefix convention. All gauges are
// unlabeled (process-wide) so cardinality stays bounded.
//
// The collectors refresh on Prometheus scrape via prometheus.GaugeFunc
// closures over a Snapshot() function the caller wires in. The 5 s TTL
// in the wrapper prevents a scrape storm from multiplying the work
// (M10-scale debt; irrelevant at M8's tenant count).
package fcvm

import (
	"context"
	"errors"
	"math"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/onebox-faas/faas/pkg/api"
)

// metricLabelUnknown is the canonical "label collapsed because the source
// string was empty" sentinel used across this package's Prometheus
// collectors (warmup histogram + liveness histogram/gauge). Closed-set
// labels stay bounded — every {} occurrence has to map to one of these
// constants to keep goconst happy (golangci-lint v2.4.0 fires on
// repeated string literals ≥ 3×).
const metricLabelUnknown = "unknown"

// SnapshotStat is the minimum surface area the dashboard needs. schedd's
// `snapshots` table row gives us MemBytes + DiskBytes + a Path; we
// compute the parked footprint as MemBytes+VMStateBytes+disk (the sum
// that drives the 130 MB/sandbox financial-model target — spec §1, §8).
//
// VMStateBytes is reported separately because the `snapshots` table
// currently stores it via the same column family the vmmclient
// returns. If a future migration splits MemBytes and VMStateBytes
// into two columns, this struct reflects that without touching
// callers.
type SnapshotStat struct {
	MemBytes     int64
	VMStateBytes int64
	DiskBytes    int64
}

// DashboardMetrics is the input surface schedd passes in. Each field is
// the owner-only query that produces the gauge value. All callbacks
// MUST be safe to call concurrently and SHOULD be cheap (a single SQL
// query or one lvs call). They run on every Prometheus scrape (default
// 15 s); the wrapper below caches the result for 5 s.
type DashboardMetrics struct {
	// ListSnapshotStats returns every live (non-stale) snapshot row's
	// size triple. schedd's pgstore wires this in; tests pass a stub.
	ListSnapshotStats func(ctx context.Context) ([]SnapshotStat, error)
	// ResidentBytes returns the sum of (ram_mb + PerVMOverheadMB) << 20
	// across instances in {WAKING, COLD_BOOTING, RUNNING, SNAPSHOTTING}.
	// schedd's ledger already maintains this number; pass it through.
	ResidentBytes func(ctx context.Context) (int64, error)
	// LvFcUsedPct returns the percentage of the lv-fc logical volume
	// currently in use (0..100). Implemented by `lvs --noheadings -o
	// data_percent LV_NAME`; the default in DefaultLvFcUsedPct
	// handles the parsing. Returns 0 (not an error) when lvs is
	// unavailable so the dashboard degrades gracefully on a macOS
	// dev box.
	LvFcUsedPct func(ctx context.Context) (float64, error)
}

// ColdBootMetrics owns the vmmd_cold_boot_fallback_total counter (ADR-016
// names "cold-boot fallback" as a vmmd-side event; every wake goes through
// Manager.bringUp). The counter is unlabeled: a fallback is a global
// signal of "snapshot went stale or restore failed" — app-level labels
// would multiply cardinality without making the dashboard panel any
// more actionable (the dashboard aggregates across apps).
//
// Held in a dedicated struct so vmmd can share the counter between the
// Manager (the only writer) and the /metrics mux (the only reader) via
// the same pointer. Mirrors wire.OpsMetrics's pattern.
type ColdBootMetrics struct {
	reg      *prometheus.Registry
	fallback prometheus.Counter
}

// NewColdBootMetrics registers vmmd_cold_boot_fallback_total on a fresh
// per-daemon registry. Pass the returned struct to fcvm.NewManager (the
// writer) and to the http mux (the reader). Calling Inc() on a nil
// receiver is a safe no-op so tests can construct a Manager without
// wiring metrics.
func NewColdBootMetrics() *ColdBootMetrics {
	reg := prometheus.NewRegistry()
	m := &ColdBootMetrics{
		reg: reg,
		fallback: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "vmmd_cold_boot_fallback_total",
			Help: "Wakes where the snapshot restore failed and the instance cold-booted from rootfs instead (ADR-005 fallback path). A non-zero rate means snapshots went stale or restore is broken; alerts at > 5% of wakes over 5m.",
		}),
	}
	reg.MustRegister(m.fallback)
	return m
}

// Registry exposes the underlying registry — vmmd's mux mounts this
// alongside the OpsMetrics registry via promhttp.HandlerFor.
func (m *ColdBootMetrics) Registry() *prometheus.Registry { return m.reg }

// Handler returns an http.Handler serving the cold-boot fallback counter.
func (m *ColdBootMetrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{Registry: m.reg})
}

// ObserveFallback records one restore-fell-back-to-cold-boot event.
// Safe on a nil receiver so callers don't have to branch on whether
// metrics were wired (matters in unit tests that drive Manager directly).
func (m *ColdBootMetrics) ObserveFallback() {
	if m == nil {
		return
	}
	m.fallback.Inc()
}

// FrameworkReadyMetrics owns the vmmd_guest_framework_warmup_seconds
// histogram (issue #470 / PR #470-FU-B, ADR-015). The histogram
// observes the wall-clock duration between guest-init boot and the
// runner's first non-5xx response (the warmup_ms the guest sends
// over vsock DGRAM port 1027 / msg=4). Each observation is labelled
// by {runtime, app}; the dashboard panel "warm-snapshot cohort,
// p50/p95 warmup by runner" uses this. The runtime label is bounded
// (≤5 values: node22, node24, python312, python313, go124) so
// cardinality stays bounded; the app label is the apps.id UUID and
// is intentionally unbounded — callers SHOULD use the
// pkg/wire.OtherLabelSet admission primitive if exporting to a shared
// Prometheus, but the per-vmmd dedicated registry pattern (this
// struct) keeps it safe.
//
// Nil-safe — Manager.MarkInstanceFrameworkReady calls ObserveWarmup
// with a nil-check so unit tests can construct a Manager without
// wiring metrics.
type FrameworkReadyMetrics struct {
	reg    *prometheus.Registry
	warmup *prometheus.HistogramVec
}

// NewFrameworkReadyMetrics registers vmmd_guest_framework_warmup_seconds
// on a fresh per-daemon registry. Pass the returned struct to
// fcvm.NewManager.WithFrameworkReady (the writer) AND to the http
// mux (the reader). Calling ObserveWarmup on a nil receiver is a
// safe no-op.
func NewFrameworkReadyMetrics() *FrameworkReadyMetrics {
	reg := prometheus.NewRegistry()
	m := &FrameworkReadyMetrics{
		reg: reg,
		warmup: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "vmmd_guest_framework_warmup_seconds",
			Help: "Wall-clock duration between guest-init boot and the runner's first non-5xx response (issue #470 / PR #470-FU-B). Bounded `runtime` label (≤5 values); `app` label is the per-wake apps.id UUID.",
			// PRD-470 buckets: the wake budget is 350 ms p50, so we
			// need tight resolution around 100-500 ms. The long tail
			// matters for cold-start diagnosis (a 5 s warmup is the
			// "warm tier is not actually warming" signal).
			Buckets: []float64{0.05, 0.1, 0.2, 0.3, 0.35, 0.5, 0.8, 1, 1.5, 3, 5},
		}, []string{"runtime", "app"}),
	}
	reg.MustRegister(m.warmup)
	return m
}

// Registry exposes the underlying registry — vmmd's mux mounts this
// alongside the OpsMetrics + ColdBootMetrics registries via
// promhttp.HandlerFor.
func (m *FrameworkReadyMetrics) Registry() *prometheus.Registry { return m.reg }

// Handler returns an http.Handler serving the warmup histogram.
func (m *FrameworkReadyMetrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{Registry: m.reg})
}

// ObserveWarmup records one warmup duration. Safe on a nil receiver.
// Empty runtime is collapsed to "unknown" so the warmup histogram
// stays queryable even for legacy wakes that pre-date PR #470-FU-B.
func (m *FrameworkReadyMetrics) ObserveWarmup(runtime, app string, seconds float64) {
	if m == nil {
		return
	}
	if runtime == "" {
		runtime = metricLabelUnknown
	}
	if app == "" {
		app = metricLabelUnknown
	}
	m.warmup.WithLabelValues(runtime, app).Observe(seconds)
}

// WakePhaseMetrics owns the vmmd_wake_phase_duration_seconds
// histogram (ADR-098 C11). Three phase labels — restore_ms /
// netns_tap_ms / guest_ready_ms — match the typed scalars on
// api/proto/onebox/faas/vmmd/v1/vmmd.proto WakeResponse (tags 11,
// 12, 13). Stays on a dedicated per-vmmd registry so the vmmd's
// own /metrics surfaces execution timings alongside the event-store write
// timings in wire.OpsMetrics. The latter uses
// vmmd_wake_event_write_duration_seconds; it does not measure VM execution.
//
// Nil-safe — Manager.Wake calls Observe* on a nil-check so unit
// tests can construct a Manager without wiring metrics.
type WakePhaseMetrics struct {
	reg    *prometheus.Registry
	phases *prometheus.HistogramVec
}

// NewWakePhaseMetrics registers vmmd_wake_phase_duration_seconds on
// a fresh per-daemon registry. Pass to Manager.SetWakePhaseMetrics
// (the writer) AND to the http mux (the reader).
func NewWakePhaseMetrics() *WakePhaseMetrics {
	reg := prometheus.NewRegistry()
	m := &WakePhaseMetrics{
		reg: reg,
		phases: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "vmmd_wake_phase_duration_seconds",
			Help: "Phase-decomposed wake duration (ADR-098 C11). Phase ∈ {restore_ms, netns_tap_ms, guest_ready_ms}. Mirrors the typed scalars on api/proto/onebox/faas/vmmd/v1/vmmd.proto WakeResponse (tags 11, 12, 13).",
			Buckets: []float64{
				0.05, 0.1, 0.2, 0.3, 0.35, 0.5, 0.8, 1.0, 1.5, 3.0, 5.0, 10.0,
			},
		}, []string{"phase"}),
	}
	// Pre-instantiate the closed phase set so /metrics surfaces
	// zero-valued series from the moment the daemon binds (mirrors
	// the wakePhaseDur / wakePhaseEmitted pre-instantiation in
	// pkg/wire/metrics.go). An idle box renders zero, not absent.
	for _, phase := range []string{"restore_ms", "netns_tap_ms", "guest_ready_ms"} {
		m.phases.WithLabelValues(phase)
	}
	reg.MustRegister(m.phases)
	return m
}

// Registry exposes the underlying registry — vmmd's mux mounts this
// alongside the OpsMetrics + ColdBootMetrics + FrameworkReadyMetrics
// registries via promhttp.HandlerFor.
func (m *WakePhaseMetrics) Registry() *prometheus.Registry { return m.reg }

// Handler returns an http.Handler serving the wake-phase histogram.
func (m *WakePhaseMetrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{Registry: m.reg})
}

// ObserveWakePhase records one wake-phase measurement in seconds.
// phase ∈ {restore_ms, netns_tap_ms, guest_ready_ms}. ms is the
// raw millisecond value off the WakeResponse typed scalars; the
// conversion to seconds matches the histogram unit. Safe on a nil
// receiver.
func (m *WakePhaseMetrics) ObserveWakePhase(phase string, ms int64) {
	if m == nil {
		return
	}
	m.phases.WithLabelValues(phase).Observe(float64(ms) / 1000.0)
}

// LivenessMetrics owns the vmmd_guest_liveness_* Prometheus collectors
// (issue #554 / ADR-078). Two collectors on a single per-daemon
// registry:
//
//   - vmmd_guest_liveness_probe_seconds{outcome}: histogram of the
//     wall-clock duration for one probe (host dial + JSON RTT). Outcomes
//     are the closed set {ok, non_200, unauthorized, timeout, conn_refused,
//     conn_err} — the same six classes the host's failure counter tracks.
//   - vmmd_guest_liveness_consecutive_failures{instance}: per-instance
//     gauge of the current consecutive-failure count. Resets to 0 on
//     a 2xx response; ticks up on every non-2xx. The
//     {instance} label is the per-wake instances.id and is intentionally
//     high cardinality — like the FrameworkReadyMetrics.app label,
//     callers SHOULD use the pkg/wire.OtherLabelSet admission primitive
//     if exporting to a shared Prometheus, but the per-vmmd dedicated
//     registry pattern (this struct) keeps it safe.
//
// Nil-safe — Manager.ObserveLivenessProbe / SetLivenessConsecutiveFailures
// call into the histograms with a nil-check so unit tests that don't
// wire metrics don't need a stub.
type LivenessMetrics struct {
	reg           *prometheus.Registry
	probe         *prometheus.HistogramVec
	consecutiveGF *prometheus.GaugeVec
}

// DiskMetrics exposes the latest guest writable-root sample on a dedicated
// vmmd registry. Per-instance labels are deleted when a VM is destroyed by
// the Manager, so the label set remains bounded by live concurrency.
type DiskMetrics struct {
	reg      *prometheus.Registry
	used     *prometheus.GaugeVec
	capacity *prometheus.GaugeVec
	pressure *prometheus.GaugeVec
}

func NewDiskMetrics() *DiskMetrics {
	reg := prometheus.NewRegistry()
	m := &DiskMetrics{
		reg: reg,
		used: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "vmmd_guest_disk_used_bytes",
			Help: "Writable application filesystem bytes used inside the guest, sampled from the merged root.",
		}, []string{"instance"}),
		capacity: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "vmmd_guest_disk_capacity_bytes",
			Help: "Writable application filesystem capacity inside the guest.",
		}, []string{"instance"}),
		pressure: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "vmmd_guest_disk_pressure",
			Help: "Current writable filesystem pressure class (normal, near_full, full) per live instance.",
		}, []string{"instance", "level"}),
	}
	reg.MustRegister(m.used, m.capacity, m.pressure)
	return m
}

func (m *DiskMetrics) Registry() *prometheus.Registry {
	if m == nil {
		return prometheus.NewRegistry()
	}
	return m.reg
}

func (m *DiskMetrics) Observe(instance string, used, capacity int64, pressure DiskPressure) {
	if m == nil {
		return
	}
	if instance == "" {
		instance = metricLabelUnknown
	}
	m.used.WithLabelValues(instance).Set(float64(used))
	m.capacity.WithLabelValues(instance).Set(float64(capacity))
	for _, level := range []DiskPressure{DiskPressureNormal, DiskPressureNearFull, DiskPressureFull} {
		value := 0.0
		if level == pressure {
			value = 1
		}
		m.pressure.WithLabelValues(instance, level.String()).Set(value)
	}
}

func (m *DiskMetrics) Delete(instance string) {
	if m == nil || instance == "" {
		return
	}
	m.used.DeleteLabelValues(instance)
	m.capacity.DeleteLabelValues(instance)
	for _, level := range []DiskPressure{DiskPressureNormal, DiskPressureNearFull, DiskPressureFull} {
		m.pressure.DeleteLabelValues(instance, level.String())
	}
}

// NewLivenessMetrics registers the two vmmd_guest_liveness_*
// collectors on a fresh per-daemon registry. Pass the returned struct
// to fcvm.NewManager.WithLivenessMetrics (the writer) AND to the
// http mux (the reader). Calling ObserveProbe / SetConsecutiveFailures
// on a nil receiver is a safe no-op.
func NewLivenessMetrics() *LivenessMetrics {
	reg := prometheus.NewRegistry()
	m := &LivenessMetrics{
		reg: reg,
		probe: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "vmmd_guest_liveness_probe_seconds",
			Help: "Wall-clock duration for one liveness probe (host dial + JSON RTT) per outcome (issue #554 / ADR-078, error-explanations cluster §6.4 amendment 1). Closed outcome set {ok, non_200, unauthorized, timeout, conn_refused, conn_err}.",
			// Probe budget: 5s default period, 2s default timeout.
			// 0.001-0.005 captures the healthy 2xx RTT; 0.2-2.0
			// captures the timeout region; 5+ captures the
			// "host can't even dial" signature.
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.5, 1, 2, 5},
		}, []string{"outcome"}),
		consecutiveGF: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "vmmd_guest_liveness_consecutive_failures",
			Help: "Current consecutive-failure count per instance (issue #554 / ADR-078). Resets to 0 on a 2xx response; ticks up on every non-2xx (timeout, conn_refused, non_200). When the count reaches the per-plan ConsecutiveFailures (default 3), vmmd calls LivenessFailed → schedd DestroyForLivenessFailure.",
		}, []string{"instance"}),
	}
	reg.MustRegister(m.probe)
	reg.MustRegister(m.consecutiveGF)
	return m
}

// Registry exposes the underlying registry — vmmd's mux mounts this
// alongside the OpsMetrics + FrameworkReadyMetrics registries via
// promhttp.HandlerFor.
func (m *LivenessMetrics) Registry() *prometheus.Registry { return m.reg }

// Handler returns an http.Handler serving the liveness collectors.
func (m *LivenessMetrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{Registry: m.reg})
}

// ObserveProbe records one probe's wall-clock duration. Safe on a nil
// receiver. Empty outcome is collapsed to "unknown" so the histogram
// stays queryable even for a wire-shape regression that lands an
// out-of-set outcome string.
func (m *LivenessMetrics) ObserveProbe(outcome string, seconds float64) {
	if m == nil {
		return
	}
	if outcome == "" {
		outcome = metricLabelUnknown
	}
	m.probe.WithLabelValues(outcome).Observe(seconds)
}

// SetConsecutiveFailures records the current consecutive-failure count
// for an instance. Safe on a nil receiver. Empty instance is
// collapsed to "unknown" so the gauge stays queryable even for a
// regression that loses the per-instance join.
func (m *LivenessMetrics) SetConsecutiveFailures(instance string, count int) {
	if m == nil {
		return
	}
	if instance == "" {
		instance = metricLabelUnknown
	}
	m.consecutiveGF.WithLabelValues(instance).Set(float64(count))
}

// DeleteConsecutiveFailures drops the per-instance gauge entry. Called
// on instance teardown so the high-cardinality {instance} label set
// doesn't accumulate dead instances (the same hygiene pattern as
// pkg/fcvm/manager.go::cidToID). Safe on a nil receiver.
func (m *LivenessMetrics) DeleteConsecutiveFailures(instance string) {
	if m == nil {
		return
	}
	m.consecutiveGF.DeleteLabelValues(instance)
}

// DashboardGauges is the wire handle schedd mounts at /metrics. Use
// NewDashboardGauges to build, then Handler() to register on the
// per-daemon mux. The struct is safe for concurrent use; the internal
// cache is mutex-protected.
type DashboardGauges struct {
	reg *prometheus.Registry
	ttl time.Duration
	src DashboardMetrics

	mu        sync.Mutex
	lastEval  time.Time
	cachedAvg float64
	cachedP95 float64
	cachedRAM float64
	cachedLV  float64
	// refreshing is set while a scrape-triggered refresh is in flight
	// (PG / lvs callbacks running outside the lock). A second scrape
	// arriving during the same window sees refreshing==1 and skips,
	// returning the cached value. Without this, a scrape storm would
	// multiply the load on PG and lvs (the exact thing the TTL is
	// meant to prevent). Atomic so the check is lock-free.
	refreshing atomic.Bool
}

// NewDashboardGauges builds a DashboardGauges bound to a fresh
// prometheus.Registry. TTL defaults to 5 s; tests can override via
// WithTTL.
func NewDashboardGauges(src DashboardMetrics) *DashboardGauges {
	g := &DashboardGauges{
		reg: prometheus.NewRegistry(),
		ttl: 5 * time.Second,
		src: src,
	}
	g.reg.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "fcvm_snapshot_fleet_avg_bytes",
			Help: "Plan-weighted average parked snapshot footprint (mem + vmstate + disk) in bytes; 130 MB/sandbox is the financial-model target.",
		}, g.avgFleet),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "fcvm_snapshot_fleet_p95_bytes",
			Help: "p95 parked snapshot footprint in bytes; spec §1 alert at > 300 MB.",
		}, g.p95Fleet),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "fcvm_resident_ram_pct",
			Help: "Σ(ram_mb + 8 MB) over live instances / 47,600 MB (the admission ceiling, spec §1/§4.3).",
		}, g.residentPct),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "fcvm_lv_fc_used_pct",
			Help: "Percentage of the lv-fc logical volume currently in use (spec §8; > 80 warn, > 90 page).",
		}, g.lvPct),
	)
	return g
}

// WithTTL swaps the cache TTL. Tests use this to avoid sleeping
// through real time. Returns the same DashboardGauges for chaining.
func (g *DashboardGauges) WithTTL(d time.Duration) *DashboardGauges {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ttl = d
	return g
}

// Handler returns an http.Handler that serves the dashboard registry.
// Plug into a mux at /metrics alongside the daemon's own ops metrics.
func (g *DashboardGauges) Handler() http.Handler {
	return promhttp.HandlerFor(g.reg, promhttp.HandlerOpts{Registry: g.reg})
}

// Registry exposes the underlying registry. Optional — most callers
// use Handler() directly.
func (g *DashboardGauges) Registry() *prometheus.Registry { return g.reg }

// refresh recomputes the cached gauge values. No-op if the cache is
// still fresh OR if another scrape is already refreshing (single-
// flight via g.refreshing). Errors from the source functions are
// swallowed: the cache keeps the prior value (graceful degradation —
// the dashboard row stays at its last good value, which is more
// honest than a sudden zero during a transient PG hiccup).
func (g *DashboardGauges) refresh(ctx context.Context) {
	g.mu.Lock()
	if time.Since(g.lastEval) < g.ttl {
		g.mu.Unlock()
		return
	}
	if !g.refreshing.CompareAndSwap(false, true) {
		// Another scrape is already fetching; let it finish and
		// return the cached values.
		g.mu.Unlock()
		return
	}
	src := g.src
	g.mu.Unlock()
	defer g.refreshing.Store(false)

	if src.ListSnapshotStats != nil {
		stats, err := src.ListSnapshotStats(ctx)
		if err == nil {
			footprints := make([]int64, 0, len(stats))
			var sum int64
			for _, s := range stats {
				foot := s.MemBytes + s.VMStateBytes + s.DiskBytes
				footprints = append(footprints, foot)
				sum += foot
			}
			sort.Slice(footprints, func(i, j int) bool { return footprints[i] < footprints[j] })
			g.mu.Lock()
			if n := len(footprints); n > 0 {
				g.cachedAvg = float64(sum) / float64(n)
				// Nearest-rank p95: ceil(0.95 * n), clamped to [1, n].
				idx := int(0.95*float64(n) + 0.5)
				if idx < 1 {
					idx = 1
				}
				if idx > n {
					idx = n
				}
				g.cachedP95 = float64(footprints[idx-1])
			} else {
				g.cachedAvg, g.cachedP95 = 0, 0
			}
			g.mu.Unlock()
		}
	}

	if src.ResidentBytes != nil {
		bytes, err := src.ResidentBytes(ctx)
		if err == nil {
			pct := 100.0 * float64(bytes) / float64(api.RAMAdmissionCeilingMB*1024*1024)
			g.mu.Lock()
			g.cachedRAM = pct
			g.mu.Unlock()
		}
	}

	if src.LvFcUsedPct != nil {
		pct, err := src.LvFcUsedPct(ctx)
		if err == nil {
			g.mu.Lock()
			g.cachedLV = pct
			g.mu.Unlock()
		}
	}

	g.mu.Lock()
	g.lastEval = time.Now()
	g.mu.Unlock()
}

// --- GaugeFunc bodies -------------------------------------------------------

func (g *DashboardGauges) avgFleet() float64 {
	g.refresh(context.Background())
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cachedAvg
}

func (g *DashboardGauges) p95Fleet() float64 {
	g.refresh(context.Background())
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cachedP95
}

func (g *DashboardGauges) residentPct() float64 {
	g.refresh(context.Background())
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cachedRAM
}

func (g *DashboardGauges) lvPct() float64 {
	g.refresh(context.Background())
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cachedLV
}

// --- Default lv-fc implementation ------------------------------------------

// DefaultLvFcUsedPct returns a closure that runs `lvs --noheadings -o
// data_percent <lvName>` and parses the trailing percent.
//
// On failure (lvs not on PATH, lv missing, parse error) the closure
// returns math.NaN() and a non-nil error. NaN is the load-bearing
// choice: Prometheus renders NaN as no-data, so Grafana shows "No
// data" instead of "0% used" — which would be dangerously misleading
// on a box where the lv-fc volume doesn't exist (alert at 90% never
// fires if the gauge is silently pinned at 0). Returning 0 here would
// also break the alert threshold; returning -1 would render as -100%
// in some Grafana panels. NaN is the only value that degrades the
// panel honestly.
//
// The dashboard cache (refresh) checks the error and keeps its prior
// value on failure; NaN only reaches the gauge when the cache has no
// prior value (very first scrape after boot, lv missing from the start).
//
// The 1 s ctx budget matches the loop-tick cadence; lv-fc stats are cheap.
func DefaultLvFcUsedPct(lvName string) func(ctx context.Context) (float64, error) {
	return func(ctx context.Context) (float64, error) {
		if lvName == "" {
			return math.NaN(), errors.New("fcvm: empty lv name")
		}
		cctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		out, err := exec.CommandContext(cctx, "lvs", "--noheadings", "-o", "data_percent", lvName).Output()
		if err != nil {
			return math.NaN(), err
		}
		// Output looks like "  37.42\n" — trim, drop trailing %, parse.
		s := strings.TrimSpace(string(out))
		s = strings.TrimSuffix(s, "%")
		if s == "" {
			return math.NaN(), nil
		}
		pct, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return math.NaN(), err
		}
		return pct, nil
	}
}
