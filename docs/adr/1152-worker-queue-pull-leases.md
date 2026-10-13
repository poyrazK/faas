# ADR-1152 · Pull leases for worker queue consumers

- **Status:** accepted
- **Date:** 2026-10-11
- **Context:** Worker and job apps cannot consume Gregale queues. Push
  delivery reaches only gateway targets, and worker instances never join the
  target set (ADR-051), so push is for function apps (ADR-231). Pull bindings
  exist, but no public API leases a message: `POST /queues/receive` returns
  rows the drain already delivered and completed, and `POST /queues/{id}/ack`
  is informational. The invocation drain never dispatches named-queue or
  binding-owned rows, so a message on a pull binding has no consumer at all
  and waits in `pending` indefinitely, while worker pools scale on the depth
  of that queue. `gregale queue setup` compounds this by creating a
  push binding for worker and job apps, a combination that can never deliver.
  Stage environments already have a receipt-based lease primitive (ADR-531);
  production queues have none.
- **Decision:**
  1. *Lease API.* An app with an enabled pull binding for a queue can lease
     its production messages:
     `POST /v1/apps/{slug}/queues/{queue}/leases` takes up to
     `api.QueueLeaseMaxMessages` messages for a visibility timeout
     (default `api.QueueLeaseDefaultVisibilitySeconds`, at most the plan's
     async deadline) and returns each message's id, payload, headers,
     attempt and an opaque receipt. `…/leases:ack`, `…/leases:nack` and
     `…/leases:extend` take receipts. An empty queue returns an empty list;
     clients poll with back-off.
  2. *Receipts and fencing.* A receipt is the message id plus a random
     token; only the token's SHA-256 is stored, in a new
     `invocation_queue_lease_receipts` table. A receipt acts only while it
     matches the row's current attempt and replay generation, the row is
     still dispatching and the lease has not expired. Ack completes the row
     through the existing completion transaction (quota, webhook
     destination and operation transitions included). Nack applies the
     row's frozen retry policy and the plan attempt budget: back to pending
     after the delay, or dead-letter. Extend moves the visibility deadline.
     An expired lease is returned to pending by the existing expired-lease
     reaper and redelivered with a new attempt; its old receipt no longer
     matches.
  3. *Capacity.* Leasing honors the binding's `max_concurrency` as the cap
     on unexpired leases, so worker parallelism stays the operator's choice.
  4. *Authentication.* A caller presents either an API key with the new
     `queues:consume` scope (or admin / deploy-write), or a workload-identity
     token for audience `gregale:queues` minted for a live instance of the
     same app through the guest's loopback token endpoint. Workload tokens
     need no secret in the image and record the leasing instance on the row.
  5. *Drain.* No change: the invocation drain already excludes named-queue
     and binding-owned rows, and the expired-lease reaper already returns any
     lapsed `dispatching` row to `pending`.
  6. *Scale-in.* A worker that holds unexpired leases ranks as busy when the
     pool picks scale-in victims, so idle-first ordering (ADR-933's
     companion change) works for pull workers, not only HTTP apps.
  7. *Push to worker/job is rejected.* The binding API, the store and the
     GitOps compiler reject `mode=push` with `workload_class` worker or job,
     with a message that points to pull leases. `gregale queue setup`
     creates pull bindings for worker and job apps. Existing push bindings of
     that shape can still be switched to pull or removed.
- **Consequences:** Worker pools become usable with Gregale queues: they
  scale on queue depth and drain it through leases, with at-least-once
  delivery, visibility timeouts, per-message ack/nack and the binding's retry
  policy and dead-letter queue. Pull-leased messages never appear on
  `/queues/receive`, which stays the result surface for pushed work. Stage
  environment queues keep ADR-531's primitive. Keyed work-policy rows are not
  leased in this version; they keep their existing paths.
- **Rejected alternatives:** Reusing the stage receipt table would hide
  production rows from `production_invocation_work`, which treats any
  receipt as stage ownership. Delivering into the guest through guest-init
  would add a second transport and couple queue semantics to the VM agent.
  Long-polling leases can follow later on the same notification channel the
  receive endpoint uses; polling with back-off is enough for the first
  version.
- **Validation:** Store tests cover lease, ack, nack to retry and to dead
  letter, extend, expiry and redelivery with a stale receipt, the
  `max_concurrency` cap and binding-mode checks. API tests cover both
  authentication paths and the rejected push + worker/job combination. A
  scheduler test covers lease-holding workers surviving scale-in.
- **Rollback:** Reverting removes the endpoints; pull-bound messages would
  again wait without a consumer. The receipts table is additive and can be
  dropped once no leases remain.
