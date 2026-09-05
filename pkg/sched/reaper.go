package sched

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Idle reaper + eviction selection (spec §4.3). Both are pure functions over a
// snapshot of instance metadata so the policy is unit-tested without a clock or a
// database; schedd calls them on its 10 s tick and under RAM pressure.

// EvictionThresholdMB is the RAM level above which schedd starts evicting: 80% of
// the 85% admission target (spec §4.3). Below it, nothing is evicted.
const EvictionThresholdMB = api.RAMAdmissionCeilingMB * 80 / 100 // 38,080

// MinInstanceAge protects a freshly-woken instance from being reaped/evicted
// before it has had a chance to serve (spec §4.3: never evict younger than 30 s).
const MinInstanceAge = 30 * time.Second

// InstanceInfo is the snapshot schedd hands the selectors for one instance.
type InstanceInfo struct {
	Instance string
	AppID    string
	Plan     api.Plan
	State    state.State
	RAMMB    int
	// SidecarMBs (issue #463 / ADR-070 / PR-C) is the per-sidecar
	// RAM slice sourced from the deployment's `sidecars jsonb`
	// column at reaper time. Empty/nil means "no sidecars" and
	// admissionMB() collapses to the legacy single-arg helper.
	// The reaper only consults admissionMB() via ReapIdle and
	// SelectEvictions; today neither path is RAM-cumulative (the
	// reaper doesn't pin a ledger), but the field is here so a
	// future per-node slice can route through the same arithmetic
	// without re-reading the deployment row.
	SidecarMBs  []int
	LastRequest time.Time
	Started     time.Time
	// EvictionPriority (issue #475) is the per-app tier, sourced from
	// apps.eviction_priority at the loop tick. 'best_effort' (default
	// for every pre-#475 row) keeps the pre-#475 LRU-by-last_request_at
	// reaper behaviour bit-for-bit; 'reserved' protects the instance
	// from cross-account RAM-pressure eviction — every best_effort
	// candidate is drained before any reserved is parked. The field
	// is identical across all instances of one app (carrier semantics
	// — same as MinInstances); schedd loops over the apps snapshot
	// in the loop tick and stamps the field once per tick. Idle and
	// aggressive reaping intentionally ignore this field so a parked
	// reserved instance eventually parks after its idle timeout
	// (the "idle-still-park" guarantee).
	EvictionPriority string
	IdleTimeoutS     int // app-configured; 0 => plan default
	// NodeID is the compute_node the instance lives on
	// (issue #97 / ADR-025 axis 3). Informational today: the
	// reaper's selectors (ReapIdle, SelectEvictions) work on the
	// global instance set and don't split by node. The field is
	// here so the loop's eviction log line can name the node and
	// so a future per-node reaper slice (e.g. drain a single node
	// before maintenance) can route without a Store round-trip.
	NodeID string
	// OpenConns is the count of TCP flows in ESTABLISHED or RELATED state
	// from this instance (spec §17 G7). An app with open flows is
	// considered active regardless of LastRequest staleness — this stops
	// idle reaping from killing a parked app mid-WebSocket. Zero is the
	// default; populated by Loop.runReaper via a FlowCounter injection
	// (see loop.go). SelectEvictions is intentionally unchanged: RAM
	// pressure is a separate axis and tearing down connections is fine
	// there.
	OpenConns int64
	// TailCount is the in-flight waitUntil(promise) task count for this
	// instance (issue #667, ADR-078). A wake with active tail tasks
	// stays RUNNING — the runner is alive and the tasks are draining
	// in-process — so the reaper treates TailCount > 0 as activity
	// regardless of LastRequest staleness. Mirrors the OpenConns > 0
	// gate in both ReapIdle and ReapAggressive; SelectEvictions is
	// unchanged (RAM pressure tears down regardless — the 5 s watchdog
	// in snapshotAndPark is the safety valve). Populated from
	// state.Instance.TailCount in loop.go's runReaper.
	TailCount int
	// MinInstances is the per-app cold-wake floor (ux_spec §6.5). Zero
	// keeps today's scale-to-zero behaviour; >0 means the reaper must
	// keep at least this many RUNNING instances alive regardless of
	// idle timeout. Honored by ReapIdle, intentionally NOT honored by
	// SelectEvictions — RAM-pressure eviction is the ceiling and it
	// wins (matches invariant §6.2-2: ceiling is physics, floor is
	// budget). Pro/Scale only — the apid gate rejects Free/Hobby so
	// the value is always sane when it lands here.
	//
	// Carrier semantics: every row of the same app carries the SAME
	// value (sourced from app.MinInstances in runReaper). The reaper
	// groups by AppID and reads the floor from the first row it sees.
	// Don't try to set MinInstances per-instance — it's a per-app
	// concept reflected redundantly on each row.
	//
	// Issue #557 closure / ADR-072: the value stamped here is the
	// app-wide max (`max(app.EffectiveMinInstances(),
	// max(d.EffectiveMinInstances() across this app's instances)`) so
	// the reaper agrees with pkg/meter/sampler.go:470-485. The
	// snapshot walk in loop.go first stamps the app-floor value, then
	// post-enriches after seeing each instance's DeploymentID.
	MinInstances int
	// DeploymentID (issue #557 closure / ADR-072) is the
	// per-instance deployment id carrier — empty on legacy rows
	// that pre-date the migration. The snapshot walk reads this
	// to enrich appDeploymentFloor in runReaper. Not consulted by
	// the selectors (ReapIdle / ReapAggressive / SelectEvictions);
	// purely a carrier so the post-snapshot enrichment pass has
	// the value at hand without re-Querying the store.
	DeploymentID string
	// WorkloadClass is the apps-row workload class
	// (ADR-051 PR-D). Workers (background jobs / cron workers /
	// long-running consumers) are reaper-exempt: they have no
	// per-request traffic so LastRequest is a meaningless idle
	// signal, and ReapAggressive's desired=ceil(rps/target) would
	// compute 0 and want to park them. RAM pressure (SelectEvictions)
	// still wins — invariant §6.2-2 is the ceiling.
	WorkloadClass state.WorkloadClass
	// LastScaleInAt is the apps-row last_scale_in_at stamp
	// (PR-C, issue #462). Carrier semantics: every row of the same
	// app carries the SAME value (sourced from app.LastScaleInAt in
	// runReaper; nil if the customer has never had a scale-in event).
	// ReapIdle and ReapAggressive consult it: when now - *LastScaleInAt
	// < ScaleInCooldownS, the entire app is skipped (cooldown_held).
	// Selecting the FIRST row's stamp and consulting once per app is
	// the loop-side contract.
	LastScaleInAt *time.Time
	// ScaleInCooldownS is the per-app scale-in cooldown in seconds
	// (PR-C, issue #462). Same carrier semantics as MinInstances —
	// sourced from app.ScalingPolicy.ScaleInCooldownS via
	// ScalingPolicyOrDefault; identical across rows of one app. Zero
	// disables cooldown enforcement (the customer has not opted in).
	ScaleInCooldownS int
	// Mode (issue #72 / ADR-125) is the instance's mode — sourced
	// from state.Instance.Mode. 'normal' (default) is the
	// customer-facing wake; 'mirror' is the shadow VM a mirror
	// goroutine woke for the comparison ledger. The reaper skips
	// mode='mirror' rows because they self-park on request
	// completion — there's no idle lifetime to reap, and pulling
	// them into the candidate set would create a redundant park
	// alongside the goroutine's deferred ParkInstance. The
	// pkg/meter sampler also skips these rows so the customer is
	// never billed for the shadow VM.
	Mode string
}

func (i InstanceInfo) admissionMB() int {
	return api.BillableRAMMBWithSidecars(i.RAMMB, i.SidecarMBs)
}

// EffectiveIdleTimeoutS resolves an app's idle timeout: the plan default unless
// the app configured one within bounds (floor 10 s, ceiling plan default × 2,
// spec §4.3).
func EffectiveIdleTimeoutS(plan api.Plan, configured int) int {
	l := api.MustLimitsFor(plan)
	if configured <= 0 {
		return l.IdleTimeoutS
	}
	floor, ceiling := l.IdleTimeoutBounds()
	switch {
	case configured < floor:
		return floor
	case configured > ceiling:
		return ceiling
	default:
		return configured
	}
}

// idleReference returns the safest timestamp for idle decisions. The database
// leaves last_request_at NULL until the first request reaches the activity
// batcher, while started_at is present as soon as the instance row is created.
// Treating a missing last request as time.Time{} makes a brand-new VM look
// infinitely idle and was the source of premature parks. A missing both-time
// record is unknown and is handled fail-closed by ReapIdle.
func idleReference(in InstanceInfo) time.Time {
	if !in.LastRequest.IsZero() {
		return in.LastRequest
	}
	return in.Started
}

// ReapIdle returns the instances to park for idleness: RUNNING instances whose
// time since last request exceeds their effective idle timeout (spec §4.3).
//
// G7: an instance with OpenConns > 0 is considered active regardless of
// LastRequest staleness — long-lived WebSockets and similar connections
// produce no periodic /v1/... requests, so a stale LastRequest would
// otherwise park them. The conntrack reader that fills OpenConns lives
// outside schedd (privilege boundary; see plan-file §PR-A).
//
// Per-app floor (ux_spec §6.5): when an app's MinInstances > 0, the
// reaper keeps at least that many RUNNING instances alive regardless
// of idle timeout. We enforce this by limiting the park count to
// (RUNNING_for_app − floor). Direction: when the candidate pool is
// bigger than that allowed count, we drop the freshest candidates —
// the freshly-woken one just served a user, parking it defeats the
// floor's purpose. RAM-pressure eviction (SelectEvictions) intentionally
// ignores the floor; spec invariant §6.2-2 puts the ceiling before the
// floor.
//
// Metrics (P1D): emits schedd_scale_down_decisions_total{outcome ∈
// {cooldown_held, park, min_floor_already}} via the supplied *OpsMetrics.
// One observation per app per tick per outcome (idempotent via per-app
// emitted-once flags). `keep` is intentionally NOT emitted — ReapIdle
// has no traffic-signal consult (no desiredByApp), so the "we
// decided to hold the line" semantics are not applicable. The
// metrics parameter is nil-safe; tests and the no-metrics fixture
// default both pass nil and the emission block no-ops.
//
// cooldownHeldByApp (P1D): per-tick shared set the loop wrapper
// passes to both reapers so the metric is incremented at most ONCE
// per app per tick even when both reaper branches consult the same
// cooldown window. ReapIdle runs first in runReaper and is the
// canonical emitter for `cooldown_held`; when its emission fires,
// it records the app into the set. ReapAggressive runs second and
// consults the set before its own emission — if the app is already
// in the set (idle already emitted), the aggressive emission is
// suppressed. The set is keyed on "this app emitted
// `cooldown_held` in this tick" (not "this app was skipped in this
// tick") so that a nil-metrics ReapIdle (no emission) does not
// poison the aggressive branch's emission. nil-safe: nil means
// "don't share state across reapers" and the per-reaper emission
// runs independently — ReapAggressive will still emit when its
// own metrics is non-nil even if idle didn't.
func ReapIdle(now time.Time, instances []InstanceInfo, metrics *wire.OpsMetrics, cooldownHeldByApp map[string]struct{}) []string {
	// appGroup counts RUNNING instances per app and gathers idle
	// candidates separately so we can trim the candidate list against
	// the floor AFTER the G7 / idle-timeout filter has run.
	type appGroup struct {
		running         int            // total RUNNING instances of this app
		floor           int            // app.MinInstances
		cands           []InstanceInfo // idle-eligible (RUNNING, no flows, stale)
		lastScaleInAt   *time.Time     // carrier (PR-C): from first row seen
		scaleInCooldown time.Duration  // carrier (PR-C): zero disables
		// P1D: per-app emitted-once flags. The cooldown consult runs
		// for every row of the app; emitting from inside the per-row
		// loop would multi-count. Same precedent as ReapAggressive's
		// cooldownEmitted flag (reaper.go:365).
		cooldownEmitted bool
		parkEmitted     bool
		floorEmitted    bool
		// appID is the AppID stamp for metric emissions (only set on
		// the first row seen; carrier semantics like LastScaleInAt).
		appID string
	}
	byApp := map[string]*appGroup{}
	for _, in := range instances {
		if in.State != state.StateRunning {
			continue
		}
		g, ok := byApp[in.AppID]
		if !ok {
			g = &appGroup{
				floor:           in.MinInstances,
				lastScaleInAt:   in.LastScaleInAt,
				scaleInCooldown: time.Duration(in.ScaleInCooldownS) * time.Second,
				appID:           in.AppID,
			}
			byApp[in.AppID] = g
		}
		// PR-C (issue #462): per-app scale-in cooldown consult. When
		// now - *LastScaleInAt < ScaleInCooldownS, the entire app is
		// skipped. The "stamp missed" direction is safe: nil
		// LastScaleInAt → bypass → normal reaping. Carrier semantics:
		// the first row's values are read once per app, so callers
		// MUST stamp the same value across all rows of one app.
		//
		// P1D: ReapIdle is the canonical emitter for
		// schedd_scale_down_decisions_total{outcome="cooldown_held"}
		// (runs first in runReaper). The shared cooldownHeldByApp
		// set is keyed on "emitted", not "skipped" — when the
		// emission fires, the app is recorded so ReapAggressive
		// can suppress its own emission. When ReapIdle is called
		// with nil metrics, the consult fires but no record is
		// written; ReapAggressive (if called with non-nil metrics
		// in the same tick) still emits. Gating the set record on
		// `metrics != nil` is load-bearing — a naive "always
		// record" approach would let ReapIdle poison the set
		// without emitting, silently dropping the observation.
		if g.lastScaleInAt != nil && g.scaleInCooldown > 0 && now.Sub(*g.lastScaleInAt) < g.scaleInCooldown {
			if metrics != nil && !g.cooldownEmitted {
				metrics.ObserveScaleDown(g.appID, "cooldown_held")
				g.cooldownEmitted = true
				if cooldownHeldByApp != nil {
					cooldownHeldByApp[g.appID] = struct{}{}
				}
			}
			continue
		}
		// ADR-051 PR-D: workers are reaper-exempt. They have no
		// per-request traffic — a worker's LastRequest is either
		// stale (no HTTP server) or zero (cold start, never
		// served). Skip them entirely so they don't inflate
		// `running` (the floor arithmetic depends on it) and
		// don't enter the candidate set. RAM pressure still
		// wins via SelectEvictions.
		if in.WorkloadClass == state.WorkloadClassWorker {
			continue
		}
		// M-2 / ADR-137 §Decision 1: execution_mode='worker' rows
		// are reaper-exempt regardless of WorkloadClass. Customers
		// can declare a worker via either the apps.metadata
		// `_execution_mode` field (M-2) or the scan-derived
		// WorkloadClass (ADR-051 PR-D). The two predicates OR;
		// the broader the better — a worker declared one way
		// should be exempt whether the other label is set or not.
		//
		// service + job: per //code-review PR #1202 finding #5
		// the reaper exemption widens to all non-request modes
		// whose lifecycle is NOT request-driven idle-timeout
		// gated. service-mode replicas are kept warm by the
		// desired-count scheduler (commit 6); pulling one out
		// from under it would silently drop the replica below
		// desired. job-mode runs are bounded by JobMaxRuntimeS,
		// not the reaper; destroying mid-job loses the
		// customer's work and emits a false idle_timeout
		// lifecycle_failure_reason.
		switch state.InstanceMode(in.Mode) {
		case state.InstanceModeWorker, state.InstanceModeService, state.InstanceModeJob:
			continue
		}
		// Issue #72 / ADR-125: mode='mirror' rows are
		// reaper-exempt. The mirror goroutine parks the instance
		// on request completion (or timeout), so there's no idle
		// lifetime to reap — pulling it into the candidate set
		// would create a redundant park alongside the goroutine's
		// deferred ParkInstance. RAM pressure (SelectEvictions)
		// still wins: if a node is over-budget, the mirror VM
		// goes the same way as a normal one. Symmetric with the
		// sampler skip — both predicates evaluate mode before any
		// billing / reaping arithmetic.
		if state.InstanceMode(in.Mode) == state.InstanceModeMirror {
			continue
		}
		g.running++
		// G7: an app with open TCP flows is active. Wins over stale
		// LastRequest so a parked app mid-WebSocket isn't reaped.
		if in.OpenConns > 0 {
			continue
		}
		// Issue #667 / ADR-078: an instance with active waitUntil
		// tasks is alive (the runner is in the tail-host drain phase,
		// not idle). Mirrors the OpenConns gate so the reaper never
		// parks a wake that has unfinished tail work — the 5 s
		// watchdog in snapshotAndPark is the upper bound on how long
		// the drain can hold, but in practice tasks complete within
		// the per-plan TailTimeoutS ceiling (5…60 s).
		if in.TailCount > 0 {
			continue
		}
		lastActivity := idleReference(in)
		if lastActivity.IsZero() {
			// We cannot prove that an instance is idle when both
			// timestamps are absent. Keep it until the next report;
			// this is safer than interpreting unknown age as stale.
			continue
		}
		timeout := time.Duration(EffectiveIdleTimeoutS(in.Plan, in.IdleTimeoutS)) * time.Second
		if now.Sub(lastActivity) > timeout {
			g.cands = append(g.cands, in)
		}
	}
	var park []string
	for _, g := range byApp {
		// Sort candidates oldest-activity-first so trimming the
		// front keeps the freshest (most-recently-served) alive. If
		// activity timestamps tie (rare; sub-second precision), the instance
		// id breaks the tie deterministically so a re-run yields the
		// same answer.
		sort.Slice(g.cands, func(a, b int) bool {
			aLast := idleReference(g.cands[a])
			bLast := idleReference(g.cands[b])
			if !aLast.Equal(bLast) {
				return aLast.Before(bLast)
			}
			return g.cands[a].Instance < g.cands[b].Instance
		})
		allowed := g.running - g.floor
		if allowed < 0 {
			allowed = 0
		}
		// preTrimCands (P1D) records the candidate count BEFORE the
		// floor trim so the `min_floor_already` emission below can
		// distinguish "candidates existed but the floor kept them" from
		// "no candidates existed at all" — the latter should emit
		// nothing (the idle branch had nothing to decide on).
		preTrimCands := len(g.cands)
		if len(g.cands) > allowed {
			g.cands = g.cands[:allowed]
		}
		// P1D: emit per-app decision metrics for the idle branch.
		// Two outcomes cover the idle-branch decision space:
		//   - `park`: we parked ≥1 instance (post-trim).
		//   - `min_floor_already`: pre-trim candidates existed but the
		//     floor kept them all (allowed == 0 with floor > 0).
		// `cooldown_held` is emitted from the per-row consult above
		// (ReapIdle is the canonical emitter for this outcome; runs
		// before ReapAggressive in runReaper, which consults the
		// shared cooldownHeldByApp set and skips its own emission
		// when ReapIdle already recorded the app). Idempotent
		// per-app flags keep the emission exactly-once.
		if metrics != nil {
			switch {
			case len(g.cands) > 0:
				if !g.parkEmitted {
					metrics.ObserveScaleDown(g.appID, "park")
					g.parkEmitted = true
				}
			case preTrimCands > 0 && allowed == 0 && g.floor > 0:
				if !g.floorEmitted {
					metrics.ObserveScaleDown(g.appID, "min_floor_already")
					g.floorEmitted = true
				}
			}
		}
		for _, c := range g.cands {
			park = append(park, c.Instance)
		}
	}
	return park
}

// ReapAggressive returns instances to park because measured traffic
// fell below target — the "fast cooldown" path in issue #171 /
// spec §4.3. It runs ALONGSIDE ReapIdle: the aggressive path
// parks the surplus above the autoscale-derived target, and
// ReapIdle's timeout handles the rest.
//
// Per-app policy:
//   - desired = ceil(windowed_rps / autoscale_target_rps); computed
//     by the caller and passed in via desiredByApp. Apps absent from
//     the map (no autoscale configured, no target, or no signal yet)
//     are SKIPPED — ReapIdle handles their cooldown via the timeout.
//   - limit = max(min_instances, desired + 1). The +1 is the
//     hysteresis buffer: it keeps one "warm" instance above the
//     target so a brief RPS wobble does not wake-then-park on the
//     next request. Honor the existing direction (freshest kept) by
//     trimming oldest-LastRequest-first.
//   - G7 still wins: OpenConns > 0 protects.
//   - MinInstanceAge still applies: candidates younger than 30 s are
//     never reaped even if the buffer says to — spec §4.3.
//
// Returns instance IDs in deterministic order (oldest-LastRequest
// first; ties broken by instance ID, matching ReapIdle).
//
// The metrics parameter is the wire OpsMetrics for emitting
// schedd_scale_down_decisions_total{outcome="cooldown_held"} when
// the per-app scale-in cooldown consult skips an app. nil-safe —
// nil and the test/fixture default of (*OpsMetrics)(nil) both
// skip the emission, matching the nil-safety convention of the
// rest of the wire package.
//
// cooldownHeldByApp (P1D): per-tick shared set populated by
// ReapIdle (which runs BEFORE ReapAggressive in runReaper). When
// an app appears here, the cooldown consult has already been
// observed for the same app in the same tick and the emission
// would double-count — ReapAggressive skips its emission in
// that case. The set is also populated by ReapAggressive's own
// consult (so the loop can hand a non-empty set to a future
// per-tick post-aggregator if one is needed). nil-safe.
func ReapAggressive(now time.Time, snapshot []InstanceInfo, desiredByApp map[string]int, metrics *wire.OpsMetrics, cooldownHeldByApp map[string]struct{}) []string {
	type appGroup struct {
		running         int
		floor           int
		desired         int
		candidates      []InstanceInfo // RUNNING, !young, !busy
		lastScaleInAt   *time.Time
		scaleInCooldown time.Duration
		cooldownEmitted bool // P1C: at most one cooldown_held observation per app
	}
	byApp := map[string]*appGroup{}
	for _, in := range snapshot {
		if in.State != state.StateRunning {
			continue
		}
		desired, ok := desiredByApp[in.AppID]
		if !ok {
			continue // no autoscale target — defer to ReapIdle
		}
		// ADR-051 PR-D: workers are reaper-exempt here too. A
		// worker's measured RPS is undefined (no HTTP server) so
		// desired = ceil(0 / target) = 0 and the loop would compute
		// extra = running - 1 (limit = max(floor, 0+1)) and want to
		// park everything above the first. We don't want that.
		if in.WorkloadClass == state.WorkloadClassWorker {
			continue
		}
		// M-2 / ADR-137 §Decision 1: execution_mode='worker' rows
		// are reaper-exempt under autoscale pressure too. Same
		// OR semantics as the idle branch above — workers declared
		// via either axis are skipped.
		//
		// service + job: per //code-review PR #1202 finding #5 the
		// autoscale branch also widens. service-mode replicas are
		// kept warm by the desired-count scheduler (commit 6); an
		// aggressive scale-in here would race the desired scheduler
		// and the instance would churn (parked → woken → parked).
		// job-mode runs are bounded by JobMaxRuntimeS, not the
		// reaper; scale-in mid-job loses the customer's work.
		switch state.InstanceMode(in.Mode) {
		case state.InstanceModeWorker, state.InstanceModeService, state.InstanceModeJob:
			continue
		}
		g, ok := byApp[in.AppID]
		if !ok {
			g = &appGroup{
				floor:           in.MinInstances,
				desired:         desired,
				lastScaleInAt:   in.LastScaleInAt,
				scaleInCooldown: time.Duration(in.ScaleInCooldownS) * time.Second,
			}
			byApp[in.AppID] = g
		}
		// PR-C (issue #462): per-app scale-in cooldown consult (mirror
		// ReapIdle). Skip the entire app when within the cooldown window.
		// P1C: emit schedd_scale_down_decisions_total{outcome="cooldown_held"}
		// exactly once per app — the per-instance loop body otherwise fires
		// for every RUNNING instance in the app. Same precedent as
		// Engine.admitGate's one-shot emission per call.
		// P1D: ReapIdle runs first in runReaper and is the canonical
		// emitter for this outcome. ReapAggressive consults the shared
		// cooldownHeldByApp set; if the idle branch already recorded
		// the app in the same tick, the aggressive emission is
		// suppressed (avoids double-count). The set is still
		// populated here as a safety net so the loop wrapper can
		// inspect it after both branches have run.
		if g.lastScaleInAt != nil && g.scaleInCooldown > 0 && now.Sub(*g.lastScaleInAt) < g.scaleInCooldown {
			_, alreadySeen := cooldownHeldByApp[in.AppID]
			if metrics != nil && !g.cooldownEmitted && !alreadySeen {
				metrics.ObserveScaleDown(in.AppID, "cooldown_held")
				g.cooldownEmitted = true
			}
			if cooldownHeldByApp != nil {
				cooldownHeldByApp[in.AppID] = struct{}{}
			}
			continue
		}
		g.running++
		// G7: open TCP flows count as activity regardless of
		// LastRequest staleness. We still count this instance
		// toward running (it's live) but it never enters the
		// candidate set — the floor of the burst that holds the
		// flow is sacred.
		if in.OpenConns > 0 {
			continue
		}
		// Issue #667 / ADR-078: an instance with active waitUntil
		// tasks is alive. Same gate as ReapIdle — never enter an
		// active tail drain into the aggressive candidate set.
		if in.TailCount > 0 {
			continue
		}
		if now.Sub(in.Started) < MinInstanceAge {
			continue
		}
		g.candidates = append(g.candidates, in)
	}
	var park []string
	for _, g := range byApp {
		limit := g.floor
		if g.desired+1 > limit {
			limit = g.desired + 1
		}
		extra := g.running - limit
		if extra <= 0 {
			continue
		}
		// Trim oldest-LastRequest-first so the freshest (most
		// recently served) stay alive — matches ReapIdle's
		// "freshness floor" direction.
		sort.Slice(g.candidates, func(a, b int) bool {
			if !g.candidates[a].LastRequest.Equal(g.candidates[b].LastRequest) {
				return g.candidates[a].LastRequest.Before(g.candidates[b].LastRequest)
			}
			return g.candidates[a].Instance < g.candidates[b].Instance
		})
		if extra > len(g.candidates) {
			extra = len(g.candidates)
		}
		for i := 0; i < extra; i++ {
			park = append(park, g.candidates[i].Instance)
		}
	}
	return park
}

// SelectEvictions returns instances to park to bring residentMB down to the
// eviction threshold, in eviction order (spec §4.3): LRU by last request, never
// an instance younger than MinInstanceAge, Scale plan evicted last. Service
// replicas are not candidates: their RAM is the customer's paid, continuous
// desired-count capacity and parking one would make the service flap under
// pressure. It returns nothing when resident RAM is at or below the threshold
// or when only service replicas remain.
func SelectEvictions(residentMB int, now time.Time, instances []InstanceInfo) []string {
	if residentMB <= EvictionThresholdMB {
		return nil
	}

	// Candidates: running instances old enough to evict.
	var cands []InstanceInfo
	for _, in := range instances {
		if in.State != state.StateRunning {
			continue
		}
		// Services are continuously reconciled to desired_count and
		// are explicitly exempt from both idle reaping and RAM-pressure
		// parking. The desired-count scheduler is the owner of service
		// replica lifecycle; allowing this independent selector to park
		// one creates an avoidable replacement storm and can leave a
		// service below its availability floor when admission is full.
		if state.InstanceMode(in.Mode) == state.InstanceModeService {
			continue
		}
		if now.Sub(in.Started) < MinInstanceAge {
			continue
		}
		cands = append(cands, in)
	}

	// Order (issue #475): best_effort before reserved, then non-Scale
	// before Scale (Scale evicted last), then oldest last request
	// first (LRU), then instance id for determinism. The new tier
	// comparator is the load-bearing one — it is the only path that
	// can change which instance is parked at a given RAM pressure.
	// Pre-#475 fixtures have EvictionPriority == "" which falls
	// through to the !reserved branch on both sides, preserving the
	// historical LRU behaviour bit-for-bit.
	sort.Slice(cands, func(a, b int) bool {
		ar, br := cands[a].EvictionPriority == string(api.EvictionPriorityReserved),
			cands[b].EvictionPriority == string(api.EvictionPriorityReserved)
		if ar != br {
			return !ar // best_effort first
		}
		as, bs := cands[a].Plan == api.PlanScale, cands[b].Plan == api.PlanScale
		if as != bs {
			return !as // non-Scale first
		}
		if !cands[a].LastRequest.Equal(cands[b].LastRequest) {
			return cands[a].LastRequest.Before(cands[b].LastRequest)
		}
		return cands[a].Instance < cands[b].Instance
	})

	// Greedily evict until resident drops to the threshold.
	var evict []string
	remaining := residentMB
	for _, in := range cands {
		if remaining <= EvictionThresholdMB {
			break
		}
		evict = append(evict, in.Instance)
		remaining -= in.admissionMB()
	}
	return evict
}
