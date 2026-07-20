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
//   - schedd owns the admission ledger and the `instances` table →
//     fcvm_resident_ram_pct (Σ ram_mb over live instances /
//     RAMAdmissionCeilingMB).
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
	reg     *prometheus.Registry
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

// DashboardGauges is the wire handle schedd mounts at /metrics. Use
// NewDashboardGauges to build, then Handler() to register on the
// per-daemon mux. The struct is safe for concurrent use; the internal
// cache is mutex-protected.
type DashboardGauges struct {
	reg *prometheus.Registry
	ttl time.Duration
	src DashboardMetrics

	mu          sync.Mutex
	lastEval    time.Time
	cachedAvg   float64
	cachedP95   float64
	cachedRAM   float64
	cachedLV    float64
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
