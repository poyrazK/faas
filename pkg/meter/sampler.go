package meter

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// FloorNamespace (ADR-060, issue #515) is the UUID v5 namespace for
// synthetic floor instance IDs derived from uuid.NameSpaceURL +
// "onebox-faas/meterd/floor/v1". The `:floor:` lineage is visible
// in the namespace name (any operator can decode it via uuid.Parse
// + namespace introspection).
//
// # FROZEN — DO NOT bump "v1" in the namespace string.
//
// The version suffix exists for a reason: rotating the namespace
// changes every existing floor row's identity and breaks AppendUsage
// first-write-wins idempotency on (instance_id, minute) across the
// upgrade. Any future rotation MUST be a new namespace string ("v2",
// "v3", …) plus a one-shot migration that re-keys existing floor
// rows. Pinned by TestFloorNamespaceFrozen.
var FloorNamespace = uuid.NewSHA1(
	uuid.NameSpaceURL,
	[]byte("onebox-faas/meterd/floor/v1"),
)

// FloorInstanceID returns the deterministic UUID v5 for the
// i-th floor slot of appID. Pure function: the same (appID, i)
// always produces the same UUID, so first-write-wins on
// (instance_id, minute) is preserved across re-ticks of the same
// minute. The :floor: lineage is preserved in the namespace name.
//
// usage_minutes.instance_id is a UUID column
// (migrations/00001_init.sql:99, PK (instance_id, minute)) and
// PgStore.AppendUsage passes the ID raw — a non-UUID string would
// fail INSERT with 22P02 invalid_text_representation. UUID v5 is
// the only synthetic-ID scheme that satisfies both the schema type
// and the first-write-wins idempotency contract.
func FloorInstanceID(appID string, i int) uuid.UUID {
	return uuid.NewSHA1(
		FloorNamespace,
		[]byte(fmt.Sprintf("%s:%d", appID, i)),
	)
}

// CPUSource is the per-instance cumulative CPU-µs reader the sampler
// uses to compute the per-minute delta. Production wires this to
// pkg/sched/instancestats.Reader via a thin adapter (pkg/meter/sampler.go
// takes a func, so the schedd package stays decoupled from pkg/meter).
// ok=false means the reader has no row for this instance (the
// instance is gone, or the schedd poller has not yet observed it).
type CPUSource interface {
	// CPUUsageUsec returns the cumulative host cgroup CPU-µs for
	// instanceID on the most recent schedd poll, plus a "found"
	// boolean. false means the reader has no row for this instance
	// this tick.
	CPUUsageUsec(instanceID string) (uint64, bool)
}

// EgressSource (ADR-046, step 8) is the per-instance egress-byte
// reader the sampler uses to compute the per-minute byte delta
// for usage_minutes.tx_bytes (gateway response bytes) and
// usage_minutes.net_tx_bytes (netns tap0 egress from vmmd). The
// two columns are sourced independently: production wires
// scheddEgressAdapter (reads netTxBytes from scheddgrpc.
// InstanceStatsRow, which vmmd populated from
// pkg/fcvm/netstats.Cache) and gatewayEgressAdapter (reads the
// gateway's per-instance ring buffer). ok=false means the
// reader has no row for this instance this tick (gone, never
// polled, regression). The TX and NetTX returned values are
// already per-tick deltas; the sampler appends them additively
// to the (instance, minute) row.
type EgressSource interface {
	// EgressBytes returns (txBytes, netTxBytes, ok). txBytes is
	// the gateway-side HTTP response byte count for this
	// instance this tick; netTxBytes is the root-side
	// vethHost.rx_bytes delta for this instance this tick. The
	// sampler does NOT compute a delta itself — the readers
	// own regression handling (mirroring cpustats's
	// drop-baseline contract). nil is OK for either field when
	// the corresponding source has no data; the sampler
	// writes 0 for that column and stamps the other normally.
	EgressBytes(instanceID string) (txBytes, netTxBytes uint64, ok bool)
}

// TailSecondsSource (issue #667 / ADR-078) is the per-instance
// tail-seconds reader the sampler uses to populate
// usage_minutes.tail_seconds. The reader is backed by vmmd
// pkg/fcvm.Manager.ReadAndResetTailSeconds, which atomically
// returns and resets the per-instance wall-clock seconds spent
// draining waitUntil tasks since the previous tick. Production
// wires a tailSource closure that calls into the live Manager
// (cmd/meterd owns the seam). nil is OK — the sampler writes 0
// for the column (the test-harness convention matching CPUSource
// / EgressSource). tail_seconds is INFORMATIONAL ONLY — it does
// NOT enter billing; pinned by
// pkg/meter/pusher_shadow_test.go::TestPushHour_ExcludesTailSeconds.
type TailSecondsSource interface {
	// ReadAndResetTailSeconds returns the per-instance accumulated
	// wall-clock seconds spent draining waitUntil tasks since the
	// previous tick, plus a "found" boolean. false means the
	// reader has no row for this instance (gone, never woken,
	// or already parked). The reader is expected to ATOMICALLY
	// reset the accumulator after reading so subsequent ticks
	// observe only fresh deltas — see Manager.ReadAndResetTailSeconds
	// for the canonical swap-and-reset contract.
	ReadAndResetTailSeconds(instanceID string) (seconds int64, ok bool)
}

// Sampler writes one minute of billable usage per live instance. It walks
// every app on the box (one-box scale; schedd's ListAllApps is the canonical
// source) and lists its instances; for each one in a state that counts
// against the RAM ledger it appends (ram_mb + 8) * 60 mb_seconds to
// usage_minutes for the truncated minute.
//
// Billing rule (spec §4.7): bill on plan RAM + 8 MB, not sampled RSS. The
// admission MB is the source of truth — schedd's ledger already charges the
// same number, so a row in usage_minutes matches what schedd counted toward
// invariant §6.2-2. Tests assert this parity.
//
// PR #75 (#71 in flight on this branch at PR open): the inline
// `ram_mb + api.PerVMOverheadMB` constant folded into api.BillableRAMMB; the
// AppendUsage idempotency on (instance_id, minute) is the meterd↔storage
// contract that prevents silent double-billing under any restart — see
// pkg/state/store.go::Store.AppendUsage.
//
// Issue #279 / PR-B: the sampler also reads the per-instance
// cumulative CPU-µs from a CPUSource (production: schedd
// instancestats.Reader) and appends the per-minute delta to
// usage_minutes.cpu_usec. cpu_usec is a measurement, NOT a billable
// unit — the financial model still bills on plan RAM + 8 MB
// (pkg/api/limits.go). The data lands in usage_minutes because that
// is the canonical per-(account, app, instance, minute) table; the
// read path is wired so a follow-up PR can extend
// Provider.PushUsageRecord without re-plumbing sampling.
type Sampler struct {
	store state.Store
	// cpu is the per-instance CPU-µs reader. nil is OK — the sampler
	// skips the CPU walk and writes 0; this is the test-harness
	// convention (no schedd in unit tests).
	cpu CPUSource
	// egress is the per-instance egress-byte reader
	// (ADR-046, step 8). nil is OK — the sampler writes 0
	// for both egress columns; the test-harness convention.
	egress EgressSource
	// tail (issue #667 / ADR-078) is the per-instance tail-seconds
	// reader that backs usage_minutes.tail_seconds. nil is OK — the
	// sampler writes 0; the test-harness convention matching
	// cpu / egress. Production wires a closure into vmmd's
	// pkg/fcvm.Manager.ReadAndResetTailSeconds.
	tail TailSecondsSource
	now  func() time.Time // injectable for tests

	// cpuBaselineMu guards the per-(instance, minute) baseline the
	// sampler uses to compute the per-minute CPU delta. The map is
	// keyed by instanceID; the value is the (lastSeenCPUUsec,
	// lastSeenMinute) pair stamped when the previous minute boundary
	// was crossed. mu is held only across the baseline lookup /
	// update — the AppendUsage call itself is unlocked (the store
	// owns its own concurrency).
	cpuBaselineMu sync.Mutex
	cpuBaseline   map[string]cpuBaseline
}

// cpuBaseline is the per-instance baseline the sampler retains
// across ticks. The cumulative counter
// (pkg/sched/instancestats.InstanceStat.CPUUsageUsec) is
// monotonically increasing across the lifetime of one cgroup; on a
// cgroup recreation (jailer restart, manual rmdir) it resets to a
// smaller number. The sampler treats the reset as a fresh baseline
// for the next minute — see SampleAndRoll for the regression branch.
type cpuBaseline struct {
	// lastCPUUsec is the cumulative CPU-µs the reader reported at
	// the previous tick. The per-minute delta is
	// `currCPUUsec - lastCPUUsec` (clamped to 0 on regression).
	lastCPUUsec uint64
	// lastMinute is the minute boundary the previous tick was
	// stamped with. The sampler resets the baseline ONLY when the
	// minute boundary changes — a redelivered minute (meterd
	// restart) sees the same baseline and idempotently writes
	// the same delta.
	lastMinute time.Time
}

// NewSampler wires the sampler. now defaults to time.Now if nil. cpu
// may be nil — the sampler skips the CPU walk and writes 0; this is
// the test-harness convention (no schedd in unit tests). egress may
// be nil — the sampler writes 0 for both egress columns; same
// convention. PR-2 (ADR-046) replaces this with a 4-arg signature
// `NewSampler(store, cpu, egress, now)` that wires both seams.
func NewSampler(store state.Store, cpu CPUSource, now func() time.Time) *Sampler {
	if now == nil {
		now = time.Now
	}
	return &Sampler{store: store, cpu: cpu, now: now}
}

// NewSamplerWithEgress is the ADR-046 wiring: store + cpu + egress +
// now. cmd/meterd passes scheddEgressAdapter and
// gatewayEgressAdapter here. now defaults to time.Now if nil. cpu
// and egress may both be nil — the sampler writes 0 for the
// respective column; the test-harness convention. The legacy
// NewSampler is kept for callers (cmd/meterd, e2e tests) that have
// not yet been migrated to the 4-arg form.
func NewSamplerWithEgress(store state.Store, cpu CPUSource, egress EgressSource, now func() time.Time) *Sampler {
	if now == nil {
		now = time.Now
	}
	return &Sampler{store: store, cpu: cpu, egress: egress, now: now}
}

// NewSamplerWithTail is the issue #667 / ADR-078 wiring: store +
// cpu + egress + tail + now. cmd/meterd passes a TailSecondsSource
// closure backed by pkg/fcvm.Manager.ReadAndResetTailSeconds. now
// defaults to time.Now if nil. cpu / egress / tail may all be nil —
// the sampler writes 0 for the respective column; the test-harness
// convention. The legacy NewSampler and NewSamplerWithEgress are
// kept for callers that have not yet been migrated to the 5-arg
// form.
func NewSamplerWithTail(store state.Store, cpu CPUSource, egress EgressSource, tail TailSecondsSource, now func() time.Time) *Sampler {
	if now == nil {
		now = time.Now
	}
	return &Sampler{store: store, cpu: cpu, egress: egress, tail: tail, now: now}
}

// RolledRow is one (instance, minute) billable line. Returned alongside any
// error so callers (the test surface, telemetry) can observe what was
// billed without re-reading the store.
type RolledRow struct {
	InstanceID  string
	AppID       string
	AccountID   string
	Minute      time.Time
	MBSeconds   int64
	AdmissionMB int
	// Mode (M-2 / ADR-137 §Decision 1) is the instance's
	// execution_mode (normal / mirror / worker / service / job).
	// The metered_mb_seconds_total{mode,plan} counter uses this
	// as a label so dashboards can split worker idle-RAM from
	// request-driven RAM. Mirror-mode rows are filtered out
	// upstream via IsMeteredSkippableMode and never reach the
	// row constructor — Mode stays "normal" / "worker" /
	// "service" / "job".
	Mode string
	// Plan (M-2 / ADR-137 §Decision 1) is the owning account's
	// billing plan (free / hobby / pro / scale). Mirrored onto the
	// row so the meterd emitMetric closure can label
	// metered_mb_seconds_total{mode,plan} without re-reading the
	// store per row. Empty string falls back to "free" at
	// emission time — matches the per-Plan fallback in
	// OpsMetrics.MeteredMBSecondsTotal.
	Plan string
	// CPUUsec is the per-minute CPU-µs delta the sampler appended
	// to usage_minutes.cpu_usec. Zero when the scheduler reader
	// has no row for this instance this tick (test stubs, or the
	// instance has not yet been polled). Issue #279 / PR-B.
	CPUUsec int64
	// TXBytes (ADR-046, step 8) is the per-minute
	// HTTP-response byte delta the sampler appended to
	// usage_minutes.tx_bytes. Source: gateway
	// statusRecorder.Bytes. Zero when no source is wired
	// or the source has no data this tick.
	TXBytes int64
	// NetTxBytes (ADR-046, step 8) is the per-minute
	// byte delta on root-side vethHost.rx_bytes the
	// sampler appended to usage_minutes.net_tx_bytes.
	// Source: vmmd netstats.Cache → schedd
	// instancestats.Poller → scheddgrpc.InstanceStatsRow.
	// Zero when no source is wired or the source has no
	// data this tick.
	NetTxBytes int64
	// NetRxBytes (ADR-048) is the per-minute byte delta
	// on root-side vethHost.tx_bytes (root→guest =
	// ingress) the sampler appends to usage_minutes.
	// net_rx_bytes. Source: vmmd netstats.Cache TX path
	// → schedd instancestats.Poller → scheddgrpc.
	// InstanceStatsRow.IngressTxBytes → meterd
	// scheddIngressAdapter. Zero when no source is wired
	// or the source has no data this tick.
	NetRxBytes int64
	// ColdBootCount (ADR-048) is the per-minute count of
	// WAKE_RESTORE→WAKE_COLD_BOOT transitions observed
	// for this instance. The sampler detects the
	// transition by comparing LastWakeMethod on
	// scheddgrpc.InstanceStatsRow across two consecutive
	// ticks for the same (instance, minute) — only the
	// transition counts; a redelivered tick within the
	// same minute is a no-op. Zero when no source is
	// wired or the instance is in WAKE_RESTORE steady-
	// state for the whole minute.
	ColdBootCount int32
	// TailSeconds (issue #667 / ADR-078) is the per-minute
	// wall-clock seconds the instance spent draining waitUntil
	// tasks. Source: vmmd pkg/fcvm.Manager.ReadAndResetTailSeconds,
	// sampled once per minute. INFORMATIONAL ONLY — does NOT
	// enter billing; pinned by
	// pkg/meter/pusher_shadow_test.go::TestPushHour_ExcludesTailSeconds.
	// Rolled up to usage_daily.tail_seconds via the meterd rollup
	// cron. Zero when no source is wired (test stubs).
	TailSeconds int64
	// SyntheticFloor (ADR-060, issue #515) marks a row
	// generated to satisfy the per-app min_instances GB-h
	// floor. True when the row's mb_seconds is NOT backed
	// by a live instance but is appended to fill the gap
	// between live instance count and
	// ScalingPolicy.MinInstances. Used for observability
	// (meterd_floor_applied_total{plan}) and the floor-vs-
	// sampled property test; storage shape is unchanged
	// (synthetic rows go through the same AppendUsage
	// path with a UUID v5 instance ID via FloorInstanceID).
	// The DB column is UUID, so the lineage cannot be
	// inferred from the instance_id alone; this bool is
	// the in-memory marker. Not persisted as a separate
	// column — the synthetic identity is the UUID v5.
	SyntheticFloor bool
}

// SampleAndRoll walks every app's live instances and appends one minute of
// billable usage per instance to usage_minutes. It is safe to call from a
// single goroutine; the Store implementation is responsible for concurrent
// safety (MemStore holds a single mutex; PgStore's INSERT … ON CONFLICT is
// atomic per row).
//
// The function returns the rows it wrote so tests can assert on the
// exact set without re-querying; production logs the count and moves on.
//
// Two side-effects per (instance, minute) row:
//   - the billable MB-seconds (spec §4.7: plan RAM + 8 MB per running
//     second; NOT changed by this PR).
//   - the per-minute CPU-µs delta (issue #279 / PR-B; informational
//     only — billing is on RAM).
func (s *Sampler) SampleAndRoll(ctx context.Context) ([]RolledRow, error) {
	minute := MinuteKey(s.now())
	apps, err := s.store.ListAllApps(ctx)
	if err != nil {
		return nil, err
	}
	// M-2 / ADR-137 §Decision 1 — prefetch per-account plans so
	// RolledRow.Plan is populated in one DB read rather than one
	// per app. The lookup mirrors Residency's per-tick account
	// fan-out: the sampler runs once per minute, so the scan cost
	// is bounded by the customer count, not the app count. A
	// failure here is non-fatal — the closure falls back to
	// "free" at emission time (OpsMetrics.MeteredMBSecondsTotal
	// treats empty as free) so a transient Postgres blip doesn't
	// drop the entire tick's metric.
	planByAccount := make(map[string]api.Plan)
	if accounts, accErr := s.store.ListAllAccounts(ctx); accErr == nil {
		for _, a := range accounts {
			planByAccount[a.ID] = a.Plan
		}
	}
	var out []RolledRow
	for _, app := range apps {
		if app.Status == state.AppDeleted {
			continue
		}
		ins, err := s.store.ListInstancesForApp(ctx, app.ID)
		if err != nil {
			return nil, err
		}
		// liveCount tracks CountsForRAM() instances for the
		// floor math below (ADR-060, issue #515). CountsForRAM
		// is the same predicate the live-instance loop uses,
		// so the floor is symmetric with the rows we just
		// wrote — never over- nor under-counting.
		liveCount := 0
		// Issue #463 / ADR-070 / PR-C: pre-load sidecar MBs
		// once per app (not once per instance) so a 100-instance
		// fleet is 1 DB read per app, not 100. Missing keys
		// (deployment_id empty / lookup failed) collapse to
		// nil = no-sidecar admission form.
		sidecarByDeploy := make(map[string][]int, len(ins))
		for _, inst := range ins {
			if inst.DeploymentID == "" {
				continue
			}
			if _, seen := sidecarByDeploy[inst.DeploymentID]; seen {
				continue
			}
			mbs, err := s.store.DeploymentSidecarRAMs(ctx, inst.DeploymentID)
			if err != nil {
				// Fail-closed: under-admit rather than over-admit.
				// The sampler doesn't own a *slog.Logger today
				// (matches the existing budget-tier floor's no-log
				// shape), so the warning rides the slog default.
				slog.Default().Warn("meter: deployment sidecar RAM lookup failed",
					"deployment_id", inst.DeploymentID, "err", err)
				continue
			}
			sidecarByDeploy[inst.DeploymentID] = mbs
		}
		for _, ins := range ins {
			if !state.State(ins.State).CountsForRAM() {
				continue
			}
			// Issue #72 / ADR-125: skip mode='mirror' instances at
			// the sampler. A mirror VM never serves the customer —
			// the customer only saw the source deployment's
			// response — so billing it would be a customer-trust
			// bug, not a feature. The reaper also skips these rows
			// (mirror VMs self-park on request completion, so
			// there's no idle lifetime to reap), which keeps the
			// sampler and reaper symmetric on the
			// CountsForRAM-vs-mirror skip — both predicates are
			// evaluated in the same order. The state-mode column is
			// NOT NULL DEFAULT 'normal' (migration 00349), so
			// pre-feature rows backfill to the default and skip
			// this branch.
			if state.IsMeteredSkippableMode(ins.Mode) {
				continue
			}
			sidecarMBs := sidecarByDeploy[ins.DeploymentID]
			row := RolledRow{
				InstanceID:  ins.ID,
				AppID:       app.ID,
				AccountID:   app.AccountID,
				Minute:      minute,
				AdmissionMB: api.BillableRAMMBWithSidecars(ins.RAMMB, sidecarMBs),
				MBSeconds:   MBSecondsPerMinute(api.BillableRAMMBWithSidecars(ins.RAMMB, sidecarMBs)),
				Mode:        ins.Mode,
				Plan:        string(planByAccount[app.AccountID]),
			}
			// Move 1 (event-driven packaging): set usage_minutes.requests
			// to the count of invocations the drain drove through this
			// instance in this minute. Index-backed by
			// invocations_instance_idx (state='dispatching'). For
			// instances with zero traffic (just parked, not yet woken)
			// this returns 0 — matching the existing free-tier
			// semantics.
			requests, err := s.store.CountInstanceInvocationsInMinute(ctx, ins.ID, minute)
			if err != nil {
				return out, fmt.Errorf("meter: sample %s/%s: %w", app.ID, ins.ID, err)
			}
			row.CPUUsec = s.cpuDeltaForMinute(ins.ID, minute)
			// PR 2 (ADR-046): wire EgressSource so this row
			// carries tx_bytes (gateway response bytes) and
			// net_tx_bytes (root-side vethHost.rx_bytes
			// delta) instead of zeros. nil EgressSource
			// keeps the legacy PR-1 behaviour for tests +
			// cmd/meterd prior to the 4-arg wiring.
			// egressBytes returns (0, 0, false) when no source
			// is wired (the legacy PR-1 path) or the source has
			// no row for this instance; in both cases the
			// additive-merge baseline stays put (mirrors the
			// cpu path's contract — same idempotency).
			if txBytes, netTxBytes, ok := s.egressBytes(ins.ID); ok {
				row.TXBytes = int64(txBytes)
				row.NetTxBytes = int64(netTxBytes)
			}
			// ADR-048 (extend metering telemetry): ingress bytes
			// and WakeMethod transitions are wired by tasks A.3a
			// and A.3b. Until then, the sampler passes 0 for both
			// new columns (the additive-merge contract makes this
			// safe — a redelivered AppendUsage with 0/0 is a no-op
			// for the columns, matching the established pattern).
			//
			// Issue #667 / ADR-078: tail_seconds is sourced from the
			// vmmd Manager's per-instance accumulator; nil
			// TailSecondsSource (the legacy test-harness path)
			// writes 0 (the additive-merge contract makes a
			// redelivered AppendUsage with 0 safe — same shape as
			// the cpu_usec / tx_bytes nil-source handling).
			if tailSec, ok := s.tailSecondsFor(ins.ID); ok {
				row.TailSeconds = tailSec
			}
			if err := s.store.AppendUsage(ctx, app.AccountID, app.ID, ins.ID, minute, row.MBSeconds, int64(requests), row.CPUUsec, row.TXBytes, row.NetTxBytes, row.NetRxBytes, int32(row.ColdBootCount), row.TailSeconds); err != nil {
				return out, err
			}
			out = append(out, row)
			liveCount++
		}
		// PR-A (ADR-060, issue #515): per-app GB-h floor for
		// ScalingPolicy.MinInstances. When the floor is set
		// and live instance count is below it, append
		// (MinInstances - liveCount) synthetic rows whose
		// total mb_seconds equals the floor's worth. Floor
		// applies from t=0 — the reaper's first-minute
		// warm-up window is billable (customer pays for the
		// warm slot from the moment they configure it).
		//
		// Synthetic IDs are deterministic UUID v5
		// (FloorInstanceID below) because
		// usage_minutes.instance_id is a UUID column and
		// PgStore.AppendUsage passes the ID raw. UUID v5
		// preserves first-write-wins idempotency on
		// (instance_id, minute) across re-ticks (the ID is
		// a pure function of (appID, ordinal)). The total
		// is split evenly across the gap; remainder goes
		// to slot 0 so the integer sum is exact.
		//
		// The loop is fail-fast on AppendUsage errors:
		// if a live-row write errors, we returned above
		// without entering this block. If a synthetic-row
		// write errors, we return here. A single tick of
		// "no floor" is preferable to "floor without live
		// rows" — partial floor state is more confusing to
		// the customer than a missed minute.
		// ADR-071 §Decision 2: read the effective floor (max of legacy
		// column + jsonb ScalingPolicy) so a bare SetAppMinInstances
		// PATCH — which only writes the legacy column — is billed
		// correctly. Pre-#557 this read policy.MinInstances only, so a
		// customer who configured via the legacy PATCH got a warm
		// floor they were never billed for.
		//
		// Issue #557 closure / ADR-072 §Decision 2: the per-deployment
		// axis is layered as max(app, deployment). The deployment
		// contribution is read from the instance row's deployment_id
		// (already populated in pgstore.CreateInstance); legacy rows
		// with empty deployment_id (test seams only) fall through to
		// the per-app floor alone.
		floor := app.EffectiveMinInstances()
		for _, ins := range ins {
			if !state.State(ins.State).CountsForRAM() {
				continue
			}
			// Same skip as the live-instance loop above: mode='mirror'
			// instances shouldn't contribute to the per-deployment
			// floor enrichment either. Skipping them keeps the floor
			// math symmetric — a customer with a mirror rule sees
			// their floor counted from production instances only.
			if state.IsMeteredSkippableMode(ins.Mode) {
				continue
			}
			if ins.DeploymentID == "" {
				continue
			}
			dep, err := s.store.DeploymentByID(ctx, ins.DeploymentID)
			if err != nil {
				continue
			}
			if dFloor := dep.EffectiveMinInstances(); dFloor > floor {
				floor = dFloor
			}
		}
		if floor > 0 && liveCount < floor {
			gap := floor - liveCount
			billable := api.BillableRAMMB(app.RAMMB)
			floorTotal := int64(gap) * MBSecondsPerMinute(billable)
			perRow := floorTotal / int64(gap)
			remainder := floorTotal - perRow*int64(gap)
			for i := 0; i < gap; i++ {
				mb := perRow
				if i == 0 {
					mb += remainder
				}
				instanceID := FloorInstanceID(app.ID, i).String()
				// Additive columns are zero (cpu_usec, tx_bytes,
				// net_tx_bytes, net_rx_bytes, cold_boot_count,
				// tail_seconds, requests) — matches the "instance
				// just parked, no traffic yet" shape. Only
				// mb_seconds is non-zero. Synthetic floors do not
				// have live instances draining waitUntil tasks, so
				// tail_seconds is 0 by construction.
				if err := s.store.AppendUsage(ctx, app.AccountID, app.ID, instanceID, minute, mb, 0, 0, 0, 0, 0, 0, 0); err != nil {
					return out, err
				}
				out = append(out, RolledRow{
					InstanceID:     instanceID,
					AppID:          app.ID,
					AccountID:      app.AccountID,
					Minute:         minute,
					AdmissionMB:    billable,
					MBSeconds:      mb,
					SyntheticFloor: true,
				})
			}
		}
	}
	return out, nil
}

// cpuDeltaForMinute computes the per-minute CPU-µs delta for the
// given instance and stamps the (instance, minute) baseline so the
// next call sees the diff from the previous tick. Returns 0 when
// the cpu source is nil (production: schedd reader not wired;
// tests), or when the reader has no row for this instance. The
// regression branch (currCPUUsec < lastCPUUsec) treats the new
// reading as a fresh baseline and returns 0 — the next minute
// picks up from there.
func (s *Sampler) cpuDeltaForMinute(instanceID string, minute time.Time) int64 {
	if s.cpu == nil {
		return 0
	}
	curr, ok := s.cpu.CPUUsageUsec(instanceID)
	if !ok {
		// Reader has no row for this instance (gone, or never
		// polled). Skip the baseline update so a future tick
		// that does observe it starts fresh.
		return 0
	}
	s.cpuBaselineMu.Lock()
	defer s.cpuBaselineMu.Unlock()
	if s.cpuBaseline == nil {
		s.cpuBaseline = map[string]cpuBaseline{}
	}
	prev, have := s.cpuBaseline[instanceID]
	var delta uint64
	switch {
	case !have:
		// First observation: this is the baseline. The first
		// non-zero delta is reported NEXT minute — same shape
		// as the vmmd cpustats.Cache first-sample-is-baseline
		// contract.
		delta = 0
	case curr < prev.lastCPUUsec:
		// Regression: cgroup recreated (jailer restart).
		// Mirrors pkg/fcvm/cpustats.Cache.Observe's
		// drop-baseline contract (ADR-039 §3.1) — the
		// customer's CPU clock for the instance starts fresh
		// on the new cgroup. The previous counter's work is
		// not patched across the break; the next-minute
		// delta picks up from the new counter.
		delta = 0
	case minute.Equal(prev.lastMinute):
		// Same minute boundary as the previous tick (redelivered
		// minute from a meterd restart). The delta is the full
		// curr - prev — restoring the previous baseline is
		// idempotent because AppendUsage on the same
		// (instance_id, minute) is additive on cpu_usec only
		// (DO NOTHING for mb_seconds / requests).
		delta = curr - prev.lastCPUUsec
	default:
		// New minute boundary crossed. The per-minute delta is
		// curr - prev; on a long gap (instance was parked
		// between minutes) the counter stops incrementing
		// (cgroup is gone) and curr equals prev → delta is 0,
		// which is the correct value for "no CPU consumed
		// during the gap".
		delta = curr - prev.lastCPUUsec
	}
	s.cpuBaseline[instanceID] = cpuBaseline{
		lastCPUUsec: curr,
		lastMinute:  minute,
	}
	return int64(delta)
}

// egressBytes returns (txBytes, netTxBytes, ok) for the given
// instance, mirroring the cpuDeltaForMinute shape but without
// the baseline state: the source readers (vmmd netstats.Cache
// via schedd, the gateway ring buffer) own their own regression
// handling, so the sampler is just a fan-out. Returns 0, 0, false
// when s.egress is nil (legacy PR-1 wiring; tests). Returns 0, 0,
// false when the source has no row for the instance (gone /
// never-polled). The sampler does NOT cache per-instance state;
// the source readers are themselves per-(instance, minute)
// accumulators on the producer side (mirror of the cpu baseline
// but moved across the wire boundary so the per-tick delta is
// computed where the counter lives).
func (s *Sampler) egressBytes(instanceID string) (uint64, uint64, bool) {
	if s.egress == nil {
		return 0, 0, false
	}
	return s.egress.EgressBytes(instanceID)
}

// tailSecondsFor returns the per-instance accumulated waitUntil
// wall-clock seconds for the current minute and atomically resets
// the accumulator on the vmmd side (via TailSecondsSource.ReadAndResetTailSeconds),
// so the same window cannot be reported twice across two minutes
// even if the Sampler runs an extra tick. Mirrors the egressBytes
// shape: returns (0, false) when s.tail is nil (the legacy PR-1
// test-harness path) or when the instance has no live tail
// accumulator (just parked, never had a tail, or already drained).
// Issue #667 / ADR-078: tail_seconds is informational only — see
// pkg/meter/pusher_shadow_test.go::TestPushHour_ExcludesTailSeconds.
// Never use this value as a billing input.
func (s *Sampler) tailSecondsFor(instanceID string) (int64, bool) {
	if s.tail == nil {
		return 0, false
	}
	v, ok := s.tail.ReadAndResetTailSeconds(instanceID)
	if !ok {
		return 0, false
	}
	return v, true
}
