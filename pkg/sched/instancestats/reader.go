package instancestats

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Validity tags the freshness of a single signal on an InstanceStat
// row. The poller stamps Unknown on the first sample (CPUPct has no
// prior baseline) and on transient cgroup reads; Stale is reserved
// for the freshness budget — when a row's SampledAt is older than
// DefaultFreshness, signal readers treat its values as absent. The
// poller may still retain the row for diagnostic/snapshot consumers.
type Validity uint8

const (
	// Valid means the value was sampled successfully on the most
	// recent Tick and is fresh within the poller's natural
	// cadence.
	Valid Validity = 0
	// Unknown means the poller has no value to report yet. For
	// CPUPct this is the first sample (the cumulative counter
	// needs a prior reading to produce a rate). For RSSMB this
	// is the vmmd reporting no value (non-Linux, transient cgroup
	// miss, first sample). The Prometheus rollup excludes Unknown
	// rows.
	Unknown Validity = 1
	// Stale means the value is older than the freshness budget.
	// Reserved as an explicit producer tag. Reader signal accessors
	// independently enforce the SampledAt freshness budget.
	Stale Validity = 2
)

// InstanceStat is the in-memory row the poller publishes per live
// VM, per Tick. The fields are the union of the cgroup-derived
// signals (CPUPct, RSSMB) and the vmmd ActivityTracker signals
// (InflightRequests, LastRequestAt). Per-instance values are the
// raw inputs to the {app,node} Prometheus rollup in
// pkg/wire.ReplaceInstanceStats.
//
// #171 (reaper scale-down bias) will call SnapshotForApp from
// runReaper and look up RecentCPUPct / RecentInflight on
// InstanceInfo. #169 (reactive scale-up trigger) will call
// SnapshotAll from a new Loop worker. Both depend on this struct
// staying stable — adding a field is fine, renaming or removing
// breaks the future PRs.
type InstanceStat struct {
	// InstanceID is the per-node instance id (state.Instances.ID).
	// Empty rows are not published; the poller filters them.
	InstanceID string
	// NodeID is the compute_node the instance lives on
	// (state.Instances.NodeID). The Prometheus rollup uses
	// (AppID, NodeID) as the label tuple.
	NodeID string
	// AppID is the app the instance belongs to
	// (state.Instances.AppID). Empty rows are not published.
	AppID string
	// CPUPct is the host cgroup CPU percent for the most recent
	// interval. The schedd-side poller does not compute the
	// rate; vmmd (PR-B) owns the cumulative-counter → rate
	// conversion and ships the per-tick value over Stats.
	// PR-A treats a nil-on-wire as the "absent this tick"
	// sentinel — the poller stamps CPU=Unknown and the wire
	// rollup excludes the row. Valid only when CPU == Valid;
	// zero or NaN with CPU=Unknown is the sentinel shape.
	CPUPct float64
	// RSSMB is cgroup memory.current, in MiB. NaN or 0 with
	// RSS=Unknown is the "absent this tick" sentinel.
	// Valid only when RSS == Valid.
	RSSMB float64
	// InflightRequests is the count of in-flight ForwardHTTP
	// calls on this instance, populated by the vmmd
	// ActivityTracker (PR-B). PR-A leaves this at 0 because the
	// wire currently carries zero (no vmmd-side population yet).
	// Zero is a real value and is distinct from the
	// "not-yet-observed" case only by the reader's Validity,
	// not the field.
	InflightRequests int64
	// LastRequestAt is the most recent ForwardHTTP start time on
	// this instance. PR-B populates it from the vmmd
	// ActivityTracker; PR-A leaves it zero and the poller
	// falls back to state.Instance.LastRequestAt.
	LastRequestAt time.Time
	// RequestCountTotal is the cumulative number of ForwardHTTP
	// requests observed by vmmd for this instance. The counter is
	// process-local and may reset when vmmd or the instance is
	// recreated; Reader detects that regression and establishes a
	// new baseline. RequestCountValid distinguishes a real zero from
	// a wire that did not provide the counter.
	RequestCountTotal uint64
	RequestCountValid bool
	// SampledAt is the wall-clock time the poller stamped this
	// row. Signal readers reject rows older than DefaultFreshness;
	// snapshot consumers can still inspect the row for diagnostics.
	SampledAt time.Time
	// CPU is the validity of CPUPct. PR-A semantics: Unknown
	// when the wire reports nil (vmmd-side `usage_usec` is
	// empty / non-Linux / transient cgroup miss), Valid when
	// the wire emits a value. The "regression / cgroup
	// recreation forces a baseline reset" branch lives in PR-B
	// — PR-A treats nil-on-the-wire as Unknown and lets the
	// rollup drop the row, without keeping a previous-sample
	// baseline. Reader signal accessors independently enforce the
	// SampledAt freshness budget; the poller does not need to rewrite
	// the validity tag when a row ages out.
	CPU Validity
	// RSS is the validity of RSSMB. Unknown on a transient
	// cgroup miss; Valid otherwise.
	RSS Validity
	// CPUUsageUsec is the cumulative host cgroup CPU usage
	// observed for this instance on the most recent Tick,
	// surfaced via the vmmd `cpu_seconds` wire field
	// (issue #279, PR-B). The value is monotonically
	// increasing across the lifetime of one cgroup; on a
	// cgroup recreation (jailer restart, manual rmdir) it
	// resets to a smaller number. The poller absorbs the
	// reset by stamping CPU=Unknown on the first post-regression
	// row and resuming CPU=Valid on the next sample. Callers
	// that need a per-instance baseline (meterd's CPU sampler)
	// SHOULD read CPUUsageUsec and remember the previous value
	// themselves; the poller does not retain a baseline.
	CPUUsageUsec uint64
	// CPUHour is CPUUsageUsec / 3.6e9 — the per-instance
	// CPU-hour reading the meterd sampler writes to
	// usage_minutes.cpu_usec. Computed on read for the
	// single tick (cheap, no copy); callers that need it
	// across ticks (e.g. cumulative hour rollup) should
	// store their own baseline.
	CPUHour float64
	// TXBytes (ADR-046) is the cumulative byte counter
	// on root-side vethHost.rx_bytes for this instance,
	// surfaced via the vmmd `net_tx_bytes` wire field.
	// Unit is interface bytes (includes Ethernet framing);
	// the same kernel counter the per-plan tc tbf qdisc
	// reads. The value is the most-recent reading from
	// pkg/fcvm/netstats.Cache.Lookup (computed at the cache's
	// own 250 ms cadence, not the schedd poller's 200 ms
	// cadence — the alignment is best-effort). Valid only
	// when TX == Valid; 0 with TX=Unknown is the sentinel
	// shape.
	TXBytes uint64
	// TX is the validity of TXBytes. Unknown on a first
	// sample / regression / netstats cache miss; Valid when
	// the wire emits a value. The meterd sampler (PR-2
	// sampler fold-in) skips rows where TX != Valid so the
	// per-minute accumulator does not double-count a
	// baseline row.
	TX Validity
	// RXBytes (ADR-048) is the cumulative byte counter on
	// root-side vethHost.tx_bytes for this instance (root →
	// guest = ingress), surfaced via the vmmd `net_rx_bytes`
	// wire field. Unit is interface bytes; same kernel
	// counter family as TXBytes. Valid only when RX ==
	// Valid; 0 with RX=Unknown is the sentinel shape. Wire
	// mirror field on scheddgrpc.InstanceStatsRow is
	// NetRxBytes; the schedd poller populates it from the
	// vmmd wire row once regen lands.
	RXBytes uint64
	// RX is the validity of RXBytes. Mirrors TX semantics:
	// Unknown on first sample / regression / cache miss.
	RX Validity
	// SidecarMBs (issue #463 / ADR-070 §Decision 6 / PR-C) is
	// the per-sidecar RAM slice sourced from the deployment's
	// `sidecars jsonb` column at Tick time. Nil/empty = legacy
	// no-sidecar shape (meterd's sampler collapses to the
	// single-arg helper). Length is bounded by
	// api.SidecarCapMax = 2; the broker that populates this
	// field (pkg/state.DeploymentSidecarRAMs) is the same one
	// schedd's Request builder reads at Admit time, so a
	// deployment with no sidecars on Admit stays no-sidecars on
	// every tick until the next deploy.
	SidecarMBs []int
}

// Reader is the stable, concurrency-safe read API the future
// scale-up and reaper code will call. It is populated exclusively
// by Poller.Replace (the only writer); readers use the Snapshot
// accessors.
//
// The internal store is a *atomic.Pointer[[]InstanceStat] — each
// Replace atomically swaps in a freshly built slice, and Snapshot
// reads pin the pointer before walking the slice. This avoids the
// copy-on-Replace + read-mutex pattern that would force the
// reader to take a lock on every wake path; the trade-off is one
// pointer-size atomic per Snapshot. For the schedd hot path this
// is the right shape (the Reader is read in many places, written
// once per 200 ms).
type Reader struct {
	snap atomic.Pointer[[]InstanceStat]

	// rateMu protects the cumulative-counter baselines and their
	// derived app-level rates. These are deliberately separate from
	// snap: Replace is the only writer, while the scale-up trigger can
	// read a rate concurrently with a poller tick.
	rateMu       sync.Mutex
	previousRate map[string]requestRateSample
	requestRates map[string]requestRate
}

type requestRateSample struct {
	count   uint64
	sampled time.Time
}

type requestRate struct {
	rps   float64
	valid bool
}

// NewReader returns a Reader with an empty snapshot. Safe to call
// before any Replace; the Snapshot accessors return empty slices
// until the first Replace.
func NewReader() *Reader { return &Reader{} }

// Replace atomically swaps in the next snapshot. The poller calls
// this once per Tick. The slice is taken by reference — the poller
// MUST NOT mutate the slice after handing it over. The reader's
// Snapshot accessors do not copy, so a Replace that mutates the
// previous slice would race against an in-flight reader.
func (r *Reader) Replace(next []InstanceStat) {
	// Defensive: stable-sort once so Snapshot accessors do not
	// need to. Determinism is part of the Reader's contract
	// (issue #170 plan §2.1 — "deterministic (appID,
	// instanceID) ordering is part of the contract").
	sort.SliceStable(next, func(i, j int) bool {
		if next[i].AppID != next[j].AppID {
			return next[i].AppID < next[j].AppID
		}
		return next[i].InstanceID < next[j].InstanceID
	})
	cp := make([]InstanceStat, len(next))
	copy(cp, next)
	r.updateRequestRates(cp)
	r.snap.Store(&cp)
}

// updateRequestRates converts each instance's cumulative vmmd request
// counter into an aggregate app-level RPS signal. A first sample, a missing
// counter, a timestamp regression, or a counter regression only establishes
// a new baseline; it never turns an old counter value into a burst.
func (r *Reader) updateRequestRates(rows []InstanceStat) {
	r.rateMu.Lock()
	defer r.rateMu.Unlock()
	if r.previousRate == nil {
		r.previousRate = make(map[string]requestRateSample)
	}
	if r.requestRates == nil {
		r.requestRates = make(map[string]requestRate)
	}

	nextRates := make(map[string]requestRate)
	seenInstances := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.AppID == "" || row.InstanceID == "" {
			continue
		}
		seenInstances[row.InstanceID] = struct{}{}
		if !row.RequestCountValid || row.SampledAt.IsZero() {
			continue
		}
		current := requestRateSample{count: row.RequestCountTotal, sampled: row.SampledAt}
		previous, ok := r.previousRate[row.InstanceID]
		if ok && current.sampled.After(previous.sampled) && current.count >= previous.count {
			elapsed := current.sampled.Sub(previous.sampled).Seconds()
			if elapsed > 0 {
				rate := float64(current.count-previous.count) / elapsed
				value := nextRates[row.AppID]
				value.rps += rate
				value.valid = true
				nextRates[row.AppID] = value
			}
		}
		r.previousRate[row.InstanceID] = current
	}
	for instanceID := range r.previousRate {
		if _, ok := seenInstances[instanceID]; !ok {
			delete(r.previousRate, instanceID)
		}
	}
	r.requestRates = nextRates
}

// RequestsPerSecond returns the aggregate request rate observed across the
// app's live instances during the latest pair of stats snapshots. It is an
// optional fallback signal for scale-up when gateway Prometheus scraping is
// unavailable, and returns false until a valid delta exists.
func (r *Reader) RequestsPerSecond(appID string) (float64, bool) {
	if r == nil || appID == "" {
		return 0, false
	}
	r.rateMu.Lock()
	defer r.rateMu.Unlock()
	rate, ok := r.requestRates[appID]
	if !ok || !rate.valid {
		return 0, false
	}
	return rate.rps, true
}

// SnapshotAll returns every row in the latest snapshot, in
// deterministic (AppID, InstanceID) order. Empty slice if the
// poller has not yet ticked. The returned slice is a defensive
// copy so the caller cannot mutate the Reader's state.
func (r *Reader) SnapshotAll() []InstanceStat {
	cur := r.snap.Load()
	if cur == nil {
		return nil
	}
	out := make([]InstanceStat, len(*cur))
	copy(out, *cur)
	return out
}

// SnapshotForApp returns the rows for one app, in InstanceID
// order. Empty slice if the app has no live instances or the
// poller has not ticked. The returned slice is a defensive copy
// of the matching rows (the Reader's slice stays intact).
func (r *Reader) SnapshotForApp(appID string) []InstanceStat {
	cur := r.snap.Load()
	if cur == nil {
		return nil
	}
	// Linear scan; the per-Tick N is bounded by the
	// (max_concurrency × apps) which is O(100s) for a one-box
	// and O(1000s) for a small cluster. Sort+bisect would be
	// premature. The linear scan is O(N) and runs on cold
	// paths (reaper, scale-up trigger), not the hot path.
	out := make([]InstanceStat, 0, 4)
	for _, row := range *cur {
		if row.AppID == appID {
			out = append(out, row)
		}
	}
	return out
}

// SnapshotForInstance returns the row for one instance id, with a
// "found" boolean. Empty (InstanceStat{}, false) if the poller
// has no row for that id this tick — the caller treats that as
// "the instance is gone, fall back to durable state".
func (r *Reader) SnapshotForInstance(instanceID string) (InstanceStat, bool) {
	cur := r.snap.Load()
	if cur == nil {
		return InstanceStat{}, false
	}
	for _, row := range *cur {
		if row.InstanceID == instanceID {
			return row, true
		}
	}
	return InstanceStat{}, false
}

// MaxInflightForApp returns the maximum InflightRequests across
// all rows of the latest snapshot for appID, plus a "found"
// boolean. (0, false) when no rows exist for the app — caller
// treats that as "no signal", distinct from (0, true) which
// means "the app has live instances but they are idle".
//
// PR-B (issue #462): the vmmd ActivityTracker feeds
// InflightRequests per instance; the schedd scale-up trigger
// consumes MaxInflightForApp to compare against the customer's
// target.concurrent_requests value. Same complexity as
// SnapshotForApp: linear scan over the atomic snapshot, called
// only on cold paths (wake-gate, scale-up trigger, reaper). The
// snapshot is pinned for the duration of the scan via the Load
// above; concurrent Replace atomically swaps in a fresh slice
// that the next caller observes.
func (r *Reader) MaxInflightForApp(appID string) (int64, bool) {
	cur := r.snap.Load()
	if cur == nil {
		return 0, false
	}
	now := time.Now()
	var max int64
	var found bool
	for _, row := range *cur {
		if row.AppID != appID || !freshSample(row.SampledAt, now) {
			continue
		}
		if !found || row.InflightRequests > max {
			max = row.InflightRequests
			found = true
		}
	}
	return max, found
}

// MaxCPU (PR-C, issue #462) returns the maximum CPUPct across
// all rows of the latest snapshot for appID where CPU is Valid,
// plus a "present" boolean. (0, false) when no rows exist for the
// app — caller treats that as "no signal", distinct from
// (max, true) which means "the app has live instances". When the
// app has live instances but every row has CPU=Unknown (first
// sample / transient cgroup miss), the result is (0, true) —
// caller distinguishes via the boolean.
//
// Companion to MaxInflightForApp (same (val, present) shape).
// The vmmd cpustats cache feeds CPUPct per instance (PR-B); the
// schedd scale-up trigger (pkg/sched/scaleup) consumes MaxCPU to
// compare against the customer's target.cpu_pct value. Rows
// stamped CPU=Unknown are skipped when computing the max: the
// cgroup-derived CPU rate requires a prior baseline, so the
// first sample of any instance is silently absent — mirroring
// the wire shape the schedd poller already decodes.
//
// Same complexity as MaxInflightForApp: linear scan over the
// atomic snapshot, called only on cold paths (scale-up trigger,
// reaper). The snapshot is pinned for the duration of the scan
// via the Load above; concurrent Replace atomically swaps in a
// fresh slice that the next caller observes.
func (r *Reader) MaxCPU(appID string) (float64, bool) {
	cur := r.snap.Load()
	if cur == nil {
		return 0, false
	}
	now := time.Now()
	var max float64
	var seen bool
	for _, row := range *cur {
		if row.AppID != appID || !freshSample(row.SampledAt, now) {
			continue
		}
		// Any row for the app counts as "the app is live" —
		// mirrors MaxInflightForApp's seen/found distinction.
		// Only CPU=Valid rows contribute to the max itself.
		seen = true
		if row.CPU != Valid {
			continue
		}
		if row.CPUPct > max {
			max = row.CPUPct
		}
	}
	if !seen {
		return 0, false
	}
	return max, true
}

// freshSample is deliberately fail-closed. A zero timestamp means the
// producer did not identify when the measurement was taken, and a future
// timestamp is not trusted as fresh because it indicates a clock problem.
func freshSample(sampledAt, now time.Time) bool {
	if sampledAt.IsZero() {
		return false
	}
	age := now.Sub(sampledAt)
	return age >= 0 && age <= DefaultFreshness
}
