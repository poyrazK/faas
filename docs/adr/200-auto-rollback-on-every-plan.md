# ADR-200 · First-wake 5xx auto-rollback on every plan

- **Status:** accepted
- **Date:** 2026-09-21
- **Amends:** the `RollbackOn5xxAllowed` plan gate from ADR-118 (issue #961)
- **Follows:** [ADR-199](199-rollouts-on-every-plan.md)

## Context

`deployments.rollback_on_5xx` is the per-deployment opt-in behind
`gregale deploy --safe`: when a new revision returns 5xx responses above a
threshold inside its first-wake window, the platform reverts traffic to the
previous revision without a human in the loop. It shipped Pro/Scale-only.

ADR-199 opened traffic splitting and the canary ladder to every plan, and had
to do real work to get there — the concurrency ledger is per-app, so Free
could not hold two revisions resident and needed the rollout grant.

**This gate has no equivalent argument to answer.** Auto-rollback consumes no
extra runtime resources:

- `first_5xx_count`, `first_wake_at` and `first_5xx_window_ends_at` are
  stamped by `StampFirstWake` / `BumpFirst5xxCount` on **every** deployment
  regardless of plan. The counters already exist; the gate only decided
  whether anything was allowed to *read* them.
- The rollback itself re-promotes a deployment that is already on disk. There
  is no build, no image pull, and no additional resident instance — the same
  path `gregale rollback` has always taken.

So the gate was not protecting capacity. It was withholding a safety net, and
the work it triggers is strictly cheaper than the outage it prevents: a bad
revision left serving keeps burning wake and RAM-seconds for as long as it
stays up, and on Free that is the account's only instance.

## Decision

Set `Limits.RollbackOn5xxAllowed = true` on every plan.

**The opt-in stays off by default.** The column default is `false`, so nothing
changes for a customer who does not ask for it; `--safe` remains an explicit
choice. This ADR removes a refusal, it does not change any default behaviour.

The field is retained rather than deleted, for the same two reasons as
`TrafficSplit` in ADR-199: it is the single switch an operator flips to
re-tier the behaviour, and `plan_rollback_on_5xx_not_allowed` stays in the
wire contract for older clients.

### Scope

Only `RollbackOn5xxAllowed`. `MirrorRuleAllowed` remains Pro+ for the third
time and the same reason: mirroring wakes a shadow VM for *every* customer
request, an unbounded 1:1 cost shape unlike either a rollout's bounded overlap
or this gate's zero marginal cost.

## Consequences

- `gregale deploy --safe` works on every plan. Combined with ADR-199 this
  makes the full safe-release path — canary ladder, health-gated promotion,
  automatic revert — available to the tier most likely to need it and least
  likely to have a staging environment.
- A Free or Hobby deployment can now revert itself. The customer-visible
  effect is a deployment that returns to its previous revision with
  `last_auto_rollback_reason` set, rather than one that stays broken until
  someone notices.
- `plan_rollback_on_5xx_not_allowed` becomes unreachable in practice.
- No migration, no schema change, no runtime change. The only behavioural
  delta is that `validateDeploymentRollbackOptions` stops refusing two plans.

## Validation

`TestPlanRollbackOn5xxAllowed` now asserts every known plan is true, with the
unknown-plan case retained as the one that still carries weight — it pins that
the accessor fails closed rather than inheriting a zero-value default.

`TestValidateDeploymentRollbackOptions_AllowedOnEveryPlan` exercises the
handler-level validator directly on all four plans, because the plan table
being right is not the same as the refusal being gone: the validator is the
only thing that ever produced the 403.
