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
and the body must be replayable.

To make bodies replayable, the proxy reads a body up front only when it
declares a `Content-Length` of at most `ServiceProxyReplayBodyMaxBytes`
(1 MiB) and keeps it in memory with a rewindable `GetBody`. Chunked or
unknown-length bodies (possibly long-lived streams), larger bodies, Upgrade
requests and native gRPC are forwarded unbuffered as before and are not
replayed; such a call still fails once and the next call wakes the target.
A declared body that cannot be read in full answers 400
(`request_body_read_failed`). Buffering also lets ordinary ADR-201 retries
replay idempotent calls with small bodies, as the public edge already does.

## Consequences

- A bodyless or small-body call — including POST/DELETE — that lands on a
  just-stopped instance is answered by the woken target in the same request.
- Repeated park/restart events no longer exhaust a quiet app's retry
  allowance through re-routes; real retries keep the ADR-201 bound.
- The extra attempt is bounded to one per call, so a node that keeps
  reporting `NotFound` cannot loop a request.

## Validation

`TestServicePostInsideLeaseAfterParkReplaysBodyMetal` parks the target right
after a call refreshed the proxy's lease on it, then POSTs a 256 KiB body to
the fixture's `/echo` route while the lease still names the parked instance.
On the n2 node, with only `gatewayd-internal` swapped between runs:

- with body buffering, 3 of 3 cycles answered 200 and echoed the body
  byte-for-byte (the GET variant also passed 3 of 3 in the same run);
- one commit earlier (re-route without buffering), 3 of 3 cycles answered
  503 `app_unavailable`.

## Rejected alternatives

- **Exempt every stale signal.** A broken stream after the init frame may
  already have reached a guest; replaying a non-idempotent call there could
  duplicate its effect.
- **The public edge's spool admission** (plan-sized limit, disk spool past
  1 MiB). The service target carries no plan, and nearly all service calls are
  small JSON; an in-memory 1 MiB bound covers them without disk or ownership
  handling. Larger bodies can adopt the spool later.
