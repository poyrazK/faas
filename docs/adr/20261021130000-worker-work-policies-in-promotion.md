# ADR-20261021130000: Worker work policies in environment promotion

## Status

Implemented. Amends ADR-20261019120027 and ADR-20261019120040 (worker-aware and
multiple-worker environment promotion). Worker promotion admission is
unchanged (`FAAS_PROMOTION_WORKERS_ENABLED`, off by default).

## Context

Worker promotion refused any worker that owned work policies, the per-key
ordering and concurrency lanes the scheduler applies to its invocations. The
promotion snapshot already treats work policies like queue bindings: the
destination keeps its own records, and the destination settings hash, which
includes them, is bound into the approved promotion plan. The refusal therefore
covered the open question of behaviour, not ownership: a worker could run
promoted code under lane rules it was never tested with.

## Decision

A worker with work policies is eligible when the source and the destination
define the same policies: the same names, per-key and fairness concurrency,
pending-update mode, debounce and expiry. Revisions are per-environment
bookkeeping and are ignored. The destination's records stay in place, and a
change to them during promotion invalidates the approved plan through the
existing settings hash. Different or one-sided policies keep the refusal.

Other worker shapes are unchanged: one production push binding and consumer per
worker, and no pull or job workers, sidecars, reload hooks, worker-owned
release commands or mixed worker migrations. Each of those changes the health,
pin and drain evidence keyed by the single binding and needs its own decision.

## Consequences

Projects that serialize queue work per key can promote workers when both
environments use the same lanes. A lane change still needs its own step in each
environment before promotion.

## Validation

`TestPromotionWorkerWorkPolicies` covers matching, revision-only and differing
definitions. The native worker promotion procedure should add one worker with a
work policy before enablement.
