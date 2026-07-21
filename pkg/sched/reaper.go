package sched

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
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
	Instance     string
	AppID        string
	Plan         api.Plan
	State        state.State
	RAMMB        int
	LastRequest  time.Time
	Started      time.Time
	IdleTimeoutS int // app-configured; 0 => plan default
	// OpenConns is the count of TCP flows in ESTABLISHED or RELATED state
	// from this instance (spec §17 G7). An app with open flows is
	// considered active regardless of LastRequest staleness — this stops
	// idle reaping from killing a parked app mid-WebSocket. Zero is the
	// default; populated by Loop.runReaper via a FlowCounter injection
	// (see loop.go). SelectEvictions is intentionally unchanged: RAM
	// pressure is a separate axis and tearing down connections is fine
	// there.
	OpenConns int64
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
	MinInstances int
}

func (i InstanceInfo) admissionMB() int { return api.BillableRAMMB(i.RAMMB) }

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
func ReapIdle(now time.Time, instances []InstanceInfo) []string {
	// appGroup counts RUNNING instances per app and gathers idle
	// candidates separately so we can trim the candidate list against
	// the floor AFTER the G7 / idle-timeout filter has run.
	type appGroup struct {
		running int            // total RUNNING instances of this app
		floor   int            // app.MinInstances
		cands   []InstanceInfo // idle-eligible (RUNNING, no flows, stale)
	}
	byApp := map[string]*appGroup{}
	for _, in := range instances {
		if in.State != state.StateRunning {
			continue
		}
		g, ok := byApp[in.AppID]
		if !ok {
			g = &appGroup{floor: in.MinInstances}
			byApp[in.AppID] = g
		}
		g.running++
		// G7: an app with open TCP flows is active. Wins over stale
		// LastRequest so a parked app mid-WebSocket isn't reaped.
		if in.OpenConns > 0 {
			continue
		}
		timeout := time.Duration(EffectiveIdleTimeoutS(in.Plan, in.IdleTimeoutS)) * time.Second
		if now.Sub(in.LastRequest) > timeout {
			g.cands = append(g.cands, in)
		}
	}
	var park []string
	for _, g := range byApp {
		// Sort candidates oldest-LastRequest-first so trimming the
		// front keeps the freshest (most-recently-served) alive. If
		// LastRequest ties (rare; sub-second precision), the instance
		// id breaks the tie deterministically so a re-run yields the
		// same answer.
		sort.Slice(g.cands, func(a, b int) bool {
			if !g.cands[a].LastRequest.Equal(g.cands[b].LastRequest) {
				return g.cands[a].LastRequest.Before(g.cands[b].LastRequest)
			}
			return g.cands[a].Instance < g.cands[b].Instance
		})
		allowed := g.running - g.floor
		if allowed < 0 {
			allowed = 0
		}
		if len(g.cands) > allowed {
			g.cands = g.cands[:allowed]
		}
		for _, c := range g.cands {
			park = append(park, c.Instance)
		}
	}
	return park
}

// SelectEvictions returns instances to park to bring residentMB down to the
// eviction threshold, in eviction order (spec §4.3): LRU by last request, never
// an instance younger than MinInstanceAge, Scale plan evicted last. It returns
// nothing when resident RAM is at or below the threshold.
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
		if now.Sub(in.Started) < MinInstanceAge {
			continue
		}
		cands = append(cands, in)
	}

	// Order: non-Scale before Scale (Scale evicted last), then oldest last
	// request first (LRU), then instance id for determinism.
	sort.Slice(cands, func(a, b int) bool {
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
