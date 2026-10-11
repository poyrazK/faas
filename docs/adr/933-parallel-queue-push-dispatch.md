# ADR-933 · Parallel queue push dispatch with backlog-driven concurrency

- **Status:** accepted
- **Date:** 2026-10-09
- **Context:** Push queue bindings (ADR-231) are Gregale's handler-shaped
  consumer: schedd claims queue rows and posts them to gatewayd, which invokes
  the app's HTTP handler. Delivery is serial at every level. `runTriggerTick`
  walks every enabled trigger in turn, each trigger posts one batch per tick
  and blocks until the gateway returns, and the gateway invokes a batch's
  records one at a time. A handler that takes two seconds per message drains
  one message per two seconds no matter how many instances the app could run,
  and it holds up every other trigger on the node while it does. ADR-231
  deferred "parallel delivery and queue-driven scale-out" to a separate
  design; this is that design. Worker-mode apps are out of scope: they never
  join the gateway target set (ADR-051) and consume through pull bindings.
- **Decision:**
  1. *Eligibility.* Only in-platform queue push consumers run in parallel:
     `kind=queue`, `source=queue`, a named queue, an owned queue binding
     projection (`queue_binding_id` set) and no exclusive broker binding.
     External brokers (Kafka, SQS, NATS, AMQP) and legacy unbound queue
     triggers keep the serial path, because their ordering and offset
     semantics are per partition or per stream.
  2. *Lanes.* The trigger tick no longer dispatches an eligible trigger
     inline. It submits up to N dispatch lanes to a new bounded loop work
     kind, `queue_push_dispatch`, keyed `trigger_id#lane`, so the pool runs
     at most one dispatch per lane. Each lane runs the existing
     poll → claim → post → settle path once. Claims are atomic leases and
     `ClaimQueueTriggerInvocation` already enforces the binding's
     `max_concurrency` across all concurrent claimers, so lanes never deliver
     more records at once than the binding allows, and keyed rows keep their
     lane and fairness locks. A lane carries exactly one record: the binding
     projection sizes trigger batches at `max_concurrency`, so a lane using
     that batch size would claim the binding's whole in-flight cap and the
     gateway would invoke it serially, leaving every other lane idle. The
     handler sees one record per invocation either way, because the gateway
     already invokes batches record by record.
  3. *Backlog-driven concurrency.* N = min(binding `max_concurrency`, the
     trigger's current allowance, depth), and at least one
     lane while the binding is enabled so an empty queue is still polled.
     Depth is the binding's pending plus in-flight count
     (`QueueStateForBinding`). The allowance starts at one lane and grows by
     one after each lane that finishes without a gateway transport error. It
     halves (floor one) after a transport error, so a failing handler is
     backed off instead of flooded. Allowance is per schedd and in memory; a
     restart begins again at one lane and ramps up within seconds.
  4. *Bounds.* The pool has `api.QueuePushDispatchSlotsPerNode` slots shared
     by every trigger on the node; a lane that finds the pool full is dropped
     and counted, and the next tick retries it. Each lane builds its own plan
     resolver, because the tick's resolver cache is not safe for concurrent
     use.
  5. *Instances.* Concurrent lanes become concurrent gateway invocations of
     the same app. The gateway's existing wake, admission and request
     concurrency scaling turn them into more instances, within
     `max_concurrency` and the plan; this ADR adds no new instance scaler.
- **Consequences:** Throughput for a slow push handler scales with the
  binding's `max_concurrency` and the app's instances instead of being fixed
  at one record at a time, and one slow trigger no longer delays the rest of
  the node's triggers. Parallelism comes from lanes of one record each; the
  gateway's batch handler is unchanged. Ordering across records of one
  binding is no longer serial; keyed work policies keep per-key ordering
  through their lane locks. Each lane polls the database, so an idle binding
  costs one candidate query per tick, the same as before, and a busy one at
  most N.
- **Rejected alternatives:** Parallelizing records inside the gateway batch
  handler would keep the trigger tick blocked for the whole batch and would
  not let one trigger's capacity grow with backlog. A separate long-lived
  goroutine per trigger would bypass the bounded loop work pool (ADR-191)
  and its drain and panic handling. Scaling lanes on instance count rather
  than backlog would launch idle lanes for apps that are already warm but
  have nothing to deliver.
- **Validation:** Unit tests cover lane eligibility, the lane count against
  backlog, cap and allowance, the allowance ramp and back-off, and that an
  eligible trigger is submitted as lanes while other triggers stay inline.
  Existing claim tests already cover `max_concurrency` across concurrent
  claimers.
- **Rollback:** Reverting the change restores serial dispatch. No migration,
  no new state, and queue rows, bindings and triggers are unchanged.
