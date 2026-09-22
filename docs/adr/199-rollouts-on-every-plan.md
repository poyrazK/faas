# ADR-199 · Traffic splitting and canary rollouts on every plan

- **Status:** accepted
- **Date:** 2026-09-21
- **Amends:** invariant §6.2-1; the `TrafficSplit` plan gate from issue #556

## Context

Traffic splitting (`deployments.traffic_percent`) and the canary ladder
(`canary_preset`, `rollout_state`) shipped Pro/Scale-only. The gate's stated
reasoning, preserved verbatim in `pkg/api/limits.go`, was cost shape:

> keeping N canary deployments warm is RAM-billable per running second for
> every "extra" live deployment, and Hobby's value-prop is "near-Free with a
> floor", not "production canary rollout"

That reasoning prices a *steady state* — several deployments running
indefinitely. It does not describe a rollout. A canary ladder is bounded by
its own stage durations (the longest catalog preset, `slow`, is 10 minutes
end to end), and it collapses back to a single deployment the moment it
completes or aborts.

There is also a correctness argument the original gate did not weigh. A safe
rollout is the mechanism that prevents a bad deploy from becoming an outage.
The customer least able to absorb an outage, and least likely to have a
staging environment, is the one on the smallest plan. Selling "deploy with
confidence" only to the tiers that can afford redundancy inverts who needs
it.

### The blocker

Flipping the flag alone would have shipped a visibly broken feature. The
concurrency ledger is **per-app**, not per-deployment:

```go
func (l *NodeLedger) Concurrency(appID string) int {
	return l.perApp[appID]
}
```

On Free (`MaxConcurrency: 1`) the currently-serving revision consumes the
app's only slot. The canary's wake then fails the cap check and that slice of
traffic receives `plan_limit_concurrency` (429) instead of the new revision —
the exact opposite of what a canary is for. Hobby (`2`) fits a two-way split
only by spending the app's entire concurrency headroom on it.

## Decision

Set `Limits.TrafficSplit = true` on every plan, and allow an app to exceed
its plan's `max_concurrency` by exactly one instance while a second
deployment is coming up alongside the one already serving.

**The overlap primitive already existed.** `NodeLedger.Request` has carried
`AllowConcurrencyOverlap` since the authenticated deployment verifier needed
a candidate revision to overlap the stable one:

```go
if have := l.perApp[r.AppID]; have >= maxConc && (!r.AllowConcurrencyOverlap || have >= maxConc+1) {
	return api.ErrPlanLimitConcurrencyAt(limits, maxConc, have)
}
```

This ADR widens *when* that flag is set rather than introducing a second,
parallel allowance. The `max+1` bound, and every physical gate, are already
written and tested.

### Detecting a rollout overlap

The condition is two O(1) ledger reads, so it adds no query to the wake hot
path:

```
the target deployment currently has NO instances   (it is the new revision)
AND the app has at least one instance              (an older revision is serving)
```

This is true only while two revisions overlap. When the rollout finishes and
the old revision's instances are reaped, the target deployment holds the
instances itself, the first condition goes false, and the grant retires on
its own. Nothing has to expire it, and there is no rollout-state column to
read or keep consistent.

An empty deployment id returns false: without a target we cannot distinguish
a rollout from ordinary scale-out, and the safe answer is the plain plan cap.

### What the grant does not do

`RolloutConcurrencyGrant = 1` relaxes the **plan** gate only. Every physical
gate still applies unchanged:

- the RAM ledger (invariant §6.2-2, Σ(`ram_mb` + 8) ≤ ceiling),
- the transactional per-node ceiling (ADR-193),
- vCPU and CPU-millicore admission.

A node under memory pressure refuses the extra instance exactly as it would
refuse any other, and the ladder simply holds at its current stage rather
than promoting. A Free customer can now run a canary; they cannot use one to
hold more RAM than their plan's single instance would have held anyway, and
they cannot walk past the cap one deployment at a time — a third revision is
refused while two are already up.

`admitGate` and `NodeLedger.Admit` must agree on the ceiling for a given
wake, or the engine would reject a canary before the ledger got the chance to
allow it. `Engine.maxConcurrencyForWake` exists so both read the same number.

### Scope

Only `TrafficSplit` is opened. Two neighbouring gates stay as they are:

- **`MirrorRuleAllowed`** stays Pro+. Mirroring wakes a shadow VM for *every*
  customer request — a 1:1 wake ratio with no bound and no completion, which
  is a genuinely different cost shape from a rollout's bounded overlap.
- **`RollbackOn5xxAllowed`** is untouched by this ADR. It is a reasonable
  candidate for the same treatment (it consumes no extra runtime resources —
  it reads 5xx counters already collected) but it is a separate decision and
  belongs in its own change.

## Consequences

- Invariant §6.2-1 now reads: **≤ `max_concurrency(plan)` +
  `RolloutConcurrencyGrant` instances of an app in {WAKING, COLD_BOOTING,
  RUNNING}, where the grant applies only during a rollout overlap.** The
  steady-state bound is unchanged.
- Worst-case resident RAM for an app rises by one instance for the duration
  of a rollout. On Free that is 136 MB (128 + 8 overhead) for at most the
  ladder's length. The tenant admission ceiling is unchanged, so fleet-level
  RAM is governed exactly as before — the grant can only ever be spent
  against headroom that already existed.
- Free and Hobby customers can now receive `plan_limit_concurrency` refusals
  in a new situation: a rollout on a node with no headroom. This surfaces as
  the ladder holding rather than an error to the customer's own traffic.
- The 403 `plan_traffic_split_not_allowed` problem code is now unreachable in
  practice. It is retained in the wire contract for older clients and for the
  case where an operator re-tiers the flag.

## Validation

`TestProperty_RolloutGrant_AllowsExactlyOneOverlap` (`pkg/sched`) pins all
four halves of the decision, at `maxConc = 1` because that is the only value
where the grant is load-bearing:

1. a second revision overlapping the serving one **is** admitted;
2. a second instance of the **same** revision is **not** — the grant must not
   widen ordinary scale-out;
3. a **third** revision is refused — the grant is exactly +1;
4. once the old revision parks, the app is back under its plain cap with no
   lingering entitlement.

The property was control-tested from both directions, per the lesson that
zeroing an input proves nothing when the inputs are redundant — the decision
function itself was neutered. Forcing the grant off fails assertion 1;
forcing it always-on fails assertion 2. A test that only ever saw the
implementation pass would have caught neither.
