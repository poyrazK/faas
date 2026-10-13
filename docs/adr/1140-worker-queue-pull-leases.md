# ADR-1140 · Pull leases for worker queue consumers

- **Status:** accepted
- **Date:** 2026-10-11
- **Context:** A push binding delivers each message to the app over HTTP, so
  only an app that listens for deliveries can consume it. A worker or job
  that runs its own loop with no HTTP listener (a broker-style consumer, the
  common shape for background work) has no way to consume a Gregale queue.
  Pull bindings exist, but no public API leases a message:
  `POST /queues/receive` returns rows the drain already delivered and
  completed, and `POST /queues/{id}/ack` is informational. The invocation
  drain never dispatches named-queue or binding-owned rows, so a message on a
  pull binding has no consumer at all and waits in `pending` indefinitely,
  while worker pools scale on the depth of that queue. Stage environments
  already have a receipt-based lease primitive (ADR-531); production queues
  have none.
- **Decision:**
  1. *Lease API.* An app with an enabled pull binding for a queue can lease
     its production messages:
     `POST /v1/apps/{slug}/queues/{queue}/leases` takes up to
     `api.QueueLeaseMaxMessages` messages for a visibility timeout
     (default `api.QueueLeaseDefaultVisibilitySeconds`, at most the plan's
     async deadline) and returns each message's id, payload, headers,
     attempt and an opaque receipt. `…/leases:ack`, `…/leases:nack` and
     `…/leases:extend` take receipts. One call stops adding messages once
     their payloads and headers would pass `api.QueueLeaseMaxResponseBytes`
     (8 MiB) and always returns at least one ready message; the budget is
     checked on the locked row before it is claimed, so nothing is leased
     that is not returned.
  1a. *Long-poll.* `wait_seconds` (at most `api.QueueLeaseMaxWaitSeconds`,
     20) holds a call that finds nothing until a message is ready. The call
     subscribes to `invocation_due` through apid's notify hub, which shares
     one LISTEN connection per daemon, so a waiting consumer holds no
     Postgres connection of its own. A notification for the app's queue
     wakes it at once; a 2-second recheck covers delayed messages that
     become due (no notification fires) and wake-ups the hub drops. Zero
     returns at once with an empty list.
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
  7. *Push stays valid for workers.* A worker that listens for deliveries
     keeps using push bindings (environment qualification and GitOps
     delivery already push to worker-mode instances); pull is the option for
     workers without an HTTP listener. Binding validation and
     `gregale queue setup` defaults are unchanged.
  8. *SDK consumers.* `@gregale/sdk-node/queues/runtime` (`consumeQueue`)
     and `faas_sdk.queue_consumer` (`consume_queue`) run the loop: long-poll
     lease up to the free concurrency, handler, ack or nack with the error,
     lease extension at half the visibility timeout (the handler's signal /
     `lease_lost` fires when an extension reports the lease gone, and the
     message is then left to its redelivery), and a graceful stop on SIGTERM
     that finishes and settles in-flight messages. They take a workload token
     for `gregale:queues` from the guest's loopback endpoint, or an API key.
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
  Long-polling through `db.WaitForNotification`, as `/queues/receive`
  does, holds a dedicated Postgres connection per waiting call; a pool of
  long-polling workers would pin as many connections, the failure ADR-190's
  notify hub exists to prevent. Client-side polling with back-off trades
  latency for load and pushes the loop into every consumer.
- **Validation:** Store tests cover lease, ack, nack to retry and to dead
  letter, extend, expiry and redelivery with a stale receipt, the
  `max_concurrency` cap, the response byte budget and binding-mode checks.
  API tests cover both authentication paths; `pkg/apid/queueleases` tests cover a wait woken
  by a notification, the recheck without notifications, an empty wait and
  the wait/batch bounds. A scheduler test covers lease-holding workers
  surviving scale-in. Node and Python SDK tests cover ack and nack, the
  cached workload token, the concurrency cap, extension and lease loss,
  graceful stop and option validation.
- **Rollback:** Reverting removes the endpoints; pull-bound messages would
  again wait without a consumer. The receipts table is additive and can be
  dropped once no leases remain.
