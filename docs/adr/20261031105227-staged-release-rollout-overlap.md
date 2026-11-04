# ADR: A release waking beside a staged release gets the rollout overlap

Date: 2026-10-31
Status: Accepted (owner, 2026-10-31)
Amends: [ADR-570](570-traffic-enforcement-guarantees.md) (the gateway's bounded rollout overlap, "a cold second positive cohort beside a routable cohort")
Related: [ADR-199](199-rollouts-on-every-plan.md), #6121

## Context

ADR-199 lets an app run `max_concurrency` + `RolloutConcurrencyGrant` (1)
instances while two revisions overlap. Its rule for detecting the overlap:
the target deployment holds no instance and the app holds at least one.
schedd applies that rule (`rolloutGrantAppliesForEnvironment`).

The gateway passes a wake maximum to its local admission fast path, which
refuses at capacity, without asking schedd, once the app's cached instances
reach it. Under ADR-570, for a weighted, version or session selection, that
maximum includes the grant only when another positive-weight cohort is
routable.

A release staged with `gregale deploy --no-traffic` has 0% weight, so it is
not in the routing weights. Its instance, booted for readiness and the
preview URL, still counts.

When the serving release is parked or frozen, its wake is capped at
`max_concurrency`. `max_concurrency` defaults to 1, so the staged instance
fills the cap and production answers 429 `app_concurrency_reached` until the
staged instance idles out (10 minutes on Scale). The journey
`TestTrafficSplitAndRollback` found this on production, rc.311 (#6121).

## Decision

1. **The gateway applies ADR-199's rule too.** Its wake maximum adds the grant
   when the selected deployment holds no cached instance and another
   deployment of the app holds one. Instances withdrawn by readiness count,
   as they do in schedd's ledger.
2. **Unchanged:**
   - schedd owns the admission decision and applies ADR-199's rule;
   - the grant stays 1;
   - every physical gate applies (RAM, the per-node ceiling, vCPU);
   - a selected deployment that already holds an instance scales within
     `max_concurrency`.

## Consequences

- A parked or frozen serving release wakes beside a staged release.
  The app runs `max_concurrency` + 1 instances only while the staged release
  holds an instance, until that instance idles out.
- Invariant §6.2-1 keeps its form: a staged release is a second deployment
  alongside the one already serving.
- The gateway can now pass schedd a wake that schedd still refuses, for
  example beside another environment's instance, which schedd does not treat
  as an overlap. That refusal reaches the customer as schedd's at-capacity
  answer, as before.

## Alternatives considered

- **Production evicts the staged instance.** When the serving release needs
  the slot, park the staged instance. This keeps the steady state at
  `max_concurrency`. But it ends the preview the customer is checking, and
  it needs a new eviction path in schedd.
- **Park staged instances after readiness.** This shortens the window but
  does not close it: a preview request wakes the staged release again.
