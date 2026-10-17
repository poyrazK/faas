# ADR-1501: Reconcile worker pools once at scheduler start

Date: 2026-10-17
Status: Accepted
Related: ADR-420, H10-28 (worker boot backoff), H10-136

## Context

Worker pools converge on events only: instance transitions, node loss,
deploy-live and lifecycle changes. ADR-420's continuous recovery covers
services and excludes worker-mode apps. Since H10-28, a worker's second and
later consecutive failures delay its replacement by 5 s doubling to 300 s.
That delay is an in-memory timer in the owning schedd. A schedd restart while
the timer is pending (every rollout restarts schedd) drops it, and no later
event arrives for an app whose only worker is FAILED. The pool stays at zero
until a deploy, a lifecycle change or a manual wake (production-us hunt #10,
H10-136).

## Decision

Thirty seconds after start, schedd lists the apps it owns (the reaper's
owner-scoped listing) and calls the existing worker reconciler once for each
active worker-mode app. The delay lets vmmd and node presence settle after a
rollout. The pass runs on its own one-slot work kind, one app at a time.
Reconciling a pool at its target is a no-op. A boot that fails reaches FAILED
and enters the existing backoff in the new process.

This is a one-time pass. It is not ADR-420's durable ledger: failure counts
still live in memory, and an admission refusal that creates no instance (no
capacity) is not retried by this pass.

## Consequences

- A restart costs a crash-looping worker at most one immediate retry, as the
  backoff comment already promised.
- Startup reads every owned app row once. This is the same cost as one reaper
  tick.
- Durable worker recovery (an ADR-420 ledger row per worker app) remains the
  path if worker pools need retries that survive restarts.

## Alternatives

- **Extend ADR-420 to workers.** This is more robust, but needs a migration
  and a new ledger contract. Deferred until worker pools need it.
- **Leave it.** This only affects workers that were already failing during a
  rollout, but the empty pool is silent.
