package state

import "time"

// EffectiveMinInstances returns the customer-facing per-app cold-wake
// floor: max(legacy column, scaling-policy jsonb). ADR-071 (issue #557)
// §Decision 2 — pre-#557 the two sources could diverge because a bare
// SetAppMinInstances PATCH writes only the legacy column (the
// SetScalingPolicy path is the only one that re-projects the jsonb into
// the column — see pkg/state/pgstore.go keepMinInstancesInSync).
//
// Three readers depend on this single number:
//
//   - pkg/sched/loop.go (reaper floor arithmetic, RUNNING count not
//     below floor when parking idle instances)
//   - pkg/sched/engine.go atMinFloorWithNoSignal (the admitGate
//     short-circuit that fires when concurrency already meets the
//     floor — the request-driven wake path's idempotency check)
//   - pkg/meter/sampler.go (ADR-060 billing, "billed from t=0" —
//     sampler emits a synthetic usage_minutes row per gap slot when
//     live count < floor)
//
// Pre-#557 the reaper read the legacy column and the sampler read the
// jsonb; a customer who configured the floor via the legacy PATCH got a
// warm floor they were never billed for (revenue-affecting). Post-#557
// the helper is the single read-side glue; no migration required because
// divergent rows behave correctly the moment the helper ships (max
// returns the safer direction — billing and enforcement both see what
// the customer actually configured).
//
// A future cleanup migration (ADR-071 §Downstream) will backfill
// divergent rows so the legacy column and the jsonb agree on every app;
// it is not in scope for this PR.
// ADR-195 adds a third source: a scaling schedule whose window is open
// raises the floor for its duration. It is folded in HERE, rather than at
// the call sites, for the reason the paragraph above describes — a floor
// the scheduler honours and the sampler does not is the revenue bug that
// already happened once. Nine non-test call sites read this helper; adding
// schedule awareness inside it makes all nine correct at once, and the one
// a future contributor forgets to update still returns the right number.
func (a *App) EffectiveMinInstances() int {
	return effectiveMinInstances(a, time.Now())
}

// EffectiveMinInstancesAt evaluates the floor against an explicit clock
// (ADR-195). Callers that own a time source use it — pkg/meter/sampler.go
// has an injectable `now func() time.Time` and bills against that, not
// against the wall clock, so a replayed or back-dated sample computes the
// floor that actually applied at the sampled instant.
func (a *App) EffectiveMinInstancesAt(t time.Time) int {
	return effectiveMinInstances(a, t)
}

// effectiveMinInstances is the function form so callers holding an App
// by value (e.g. pkg/sched/engine.go's local `app state.App`) can pass
// `&app` without copying the whole struct. Nil-safe.
func effectiveMinInstances(a *App, t time.Time) int {
	if a == nil {
		return 0
	}
	policy := ScalingPolicyOrDefault(a.ScalingPolicy)
	floor := a.MinInstances
	if policy.MinInstances > floor {
		floor = policy.MinInstances
	}
	// Schedules compose by max with the static floor and with each other,
	// and only ever raise it: an open window cannot park an app that
	// min_instances would otherwise keep warm.
	if scheduled := policy.ScheduledMinInstancesAt(t); scheduled > floor {
		floor = scheduled
	}
	return floor
}
