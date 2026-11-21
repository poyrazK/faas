# ADR: Queue review fixes — leases, retention, push holds and binding lifecycle

Date: 2026-11-12
Status: Accepted
Amends: [ADR-1140](1140-worker-queue-pull-leases.md) (pull leases), [ADR-20261022105723](20261022105723-worker-queue-limits-cpu-pricing-sizes.md) (retention), [ADR-20261101093812](20261101093812-queue-replay-meets-queue-limits.md) (replay and retention), [ADR-972](972-parallel-queue-push-dispatch.md) (push lanes)
Related: [ADR-1162](1162-billing-delivery-finality-spend-cap-and-plan-history.md), [ADR-1352](1352-push-queue-binding-completion-canaries.md)

## Context

A four-round read-only review of the Gregale queue (send, push delivery,
pull leases, dead letters, bindings, SDKs) on 2026-11-12 found defects that
strand, lose or misroute customer messages, plus several that cost capacity
or mislead callers. Two were confirmed with store probes before this change:
a renamed pull binding's earlier messages could be leased but never settled,
and pull nacks ignored the binding's retry policy.

## Decision

**Pull leases**

1. A receipt settles (ack, nack, extend) under the binding's current queue
   name as well as the name the message was sent under. Leases select by
   binding identity, so renaming a binding's `queue_name` — through the API,
   `gregale.yaml` or `queue setup --force` — no longer strands its messages.
2. A send stamps its retry policy from the request, else the selected
   binding, else the app, capped by the plan. Pull nacks and lapsed leases
   are budgeted by that policy, so the binding's `retry_policy` now governs
   pull consumers as the docs promise.
3. An extend cannot move a lease past the plan's async invocation deadline
   from the lease's issue. SDK consumers extend while their handler runs; a
   hung handler used to hold its message and a concurrency slot forever.
4. Lease long-polls are capped at 64 per account and 4096 per apid. Past the
   cap a call leases once without waiting instead of failing. A woken waiter
   coalesces notifications for 25 ms before it leases.
5. Leases whose caller disconnected before the response return to pending
   with the attempt they never used (`ReleaseQueueLeases`).
6. A push verification canary is never leased by a pull worker, and a push
   binding holding keyed (work-policy) messages cannot switch to pull, which
   never leases keyed work (`queue_binding_keyed_work`, 409).

**Retention**

7. A pending message's retention counts from the latest of its send, its last
   replay and its `due_at`, so a retry or nack delay longer than the plan's
   retention no longer turns into `queue_message_expired`. A suspended owner's
   messages do not expire while it is suspended.

**Push delivery**

8. A queue push trigger is not polled while its app is held by the spend cap
   or a compute budget, as the drain holds async work (ADR-1162). Polling met
   a refused wake that spent attempts until the messages dead-lettered.
9. Only the scheduler that owns the app polls a queue push trigger
   (`triggerAppOwned`, as broker triggers already do). The lane allowance map
   forgets triggers no longer polled.
10. A failed gateway dispatch is settled with a detached context when the
    scheduler is shutting down; the cancelled context left every claim leased
    until the claim lease lapsed, stalling bindings at their concurrency after
    each restart. A hard crash still waits for the claim lease: shortening it
    below the gateway's batch timeout would risk duplicate delivery.
11. A claim the poller hands back undelivered (a message bumped from a full
    batch) returns the attempt it took, so canaries stay claimable.

**Bindings**

12. Re-creating a deleted (retired) production binding with the same `name`
    and `queue_name` revives it, and its held messages drain. Migration
    20261112193138262 lets `guard_queue_binding_retirement` clear `retired_at`
    with nothing else changed when the transaction names the binding in
    `gregale.queue_binding_revive`; the binding then takes the requested
    settings through the ordinary update. Any other reuse of a retired queue
    name is still refused.
13. A stage environment refuses an enabled pull binding: leases serve
    production only and the stage delivery primitive has no public consumer,
    so such a binding accepted sends no worker could take.
14. `queue setup` keeps an existing binding's concurrency when none is given,
    needs `force` to switch a pull binding to push, and creates new bindings
    with `max_concurrency` 10. The scaler never runs more workers than the
    binding's concurrency, so the old default of 1 capped scaling at one
    worker. The CLI sends `--max-concurrency` only when set.

**Callers**

15. A send into a named queue nothing reads (an app with no binding or
    consumer) answers with a `Warning` header. Legacy worker scaling counts
    only messages something consumes.
16. A malformed `before` cursor on peek and dead-letter listing answers 400.
17. The Node and Python consumers name the API-key fallback when workload
    identity is unavailable; the Go SDK gains personal-app lease methods.

## Not changed

- The expiry sweep's index range still uses the shortest plan retention; a
  per-message expiry column would bound it per plan and needs its own
  migration and backfill.
- The `queue-pull-leases` catalog entry still owns the receive, peek and
  state routes. Catalog routes drive the capability gate, so moving the lease
  routes under that capability changes which platform flag hides them in
  production; that needs the flag owner's decision.
- Stage environments still have no pull consumer path; activating the private
  stage delivery primitive is separate, qualified work.

## Consequences

Pull consumers keep their messages across binding renames and deletions,
and retries and suspensions no longer expire work early. Idle long-polls and
idle push triggers cost bounded database work. Behavior visible to callers:
a new `Warning` header, a 409 for push-to-pull with keyed work, a 400 for an
enabled stage pull binding and for a malformed cursor, and `lease_expired`
for an extend past the lease lifetime.
