# ADR-1133: Undelivered service calls re-route outside the retry budget

- Status: Accepted
- Date: 2026-10-12
- Related: ADR-196, ADR-201, ADR-288
- Amends: ADR-201 (retry admission for the internal service proxy)

## Context

When a service call is routed to an instance that has just stopped (a park,
restart, runtime-config rollout or deploy drain inside the proxy's 5 s
endpoint lease), the service proxy now evicts the instance and re-routes the
call, waking the target if nothing routable remains. That re-route ran as an
ordinary ADR-201 retry, so it inherited two limits that exist for a different
failure — a target that might have received the request:

- **Idempotency.** A POST/PATCH (without the caller's explicit opt-in) is
  never replayed, so it failed once even though the target no longer existed.
- **The aggregate retry budget.** A quiet app is allowed `max(1, 10%)`
  replays per 10 s bucket. Two stale-instance events inside one bucket —
  measured on the n2 node with parks 7.5 s apart — failed the second call
  once.

Both limits guard against duplicating a side effect or amplifying load on a
struggling target. Neither applies when the request provably reached no guest.

## Decision

The forwarding bridge marks a stale target as **undelivered** only when the
failure proves no guest saw the request:

- no gRPC client exists for the target's node;
- the forward stream could not be opened (`Unavailable` on open);
- vmmd answered `NotFound` for the instance before any response — vmmd checks
  that the instance has a live network namespace before it dials the guest
  bridge, so nothing was forwarded.

Failures that may have reached a guest (an init or body send that broke, any
error after the response started, other codes) stay plain stale and keep the
full ADR-201 rules.

An undelivered failure earns the call **one** extra attempt on top of its
policy's, for any method, without spending the aggregate retry budget and
without backoff. The request's own deadline (`MinRemaining`) still applies,
and the body must be replayable: the proxy does not buffer request bodies, so
a call carrying a body without `GetBody` still fails once and the next call
wakes the target.

## Consequences

- A bodyless call — including POST/DELETE — that lands on a just-stopped
  instance is answered by the woken target in the same request.
- Repeated park/restart events no longer exhaust a quiet app's retry
  allowance through re-routes; real retries keep the ADR-201 bound.
- The extra attempt is bounded to one per call, so a node that keeps
  reporting `NotFound` cannot loop a request.

## Rejected alternatives

- **Exempt every stale signal.** A broken stream after the init frame may
  already have reached a guest; replaying a non-idempotent call there could
  duplicate its effect.
- **Buffer bodies to make POST bodies replayable.** Worth doing, but it is
  the public edge's spool admission (bounded size, disk, ownership) brought to
  the service proxy — a separate change.
