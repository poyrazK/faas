# ADR: External broker triggers run on lanes, polled by their app's node

Date: 2026-10-27
Status: Accepted
Amends: [ADR-972](972-parallel-queue-push-dispatch.md) (external brokers kept serial), [ADR-100](100-triggers-event-source-mappings.md) (every schedd walks every trigger)
Related: [ADR-1180](1180-long-async-invocations.md), [ADR 20261027160930](20261027160930-continuous-fair-drain.md)

## Context

A GA scale blocker from the 2026-10-25 event audit: on each schedd, every
Kafka, NATS, Redis Streams and SQS-compatible trigger was polled and
delivered one after another on the trigger tick's single work-pool slot.
Each waited for its whole batch, which the gateway runs record by record (up
to 60 s for a default app, 16.5 min for a long-timeout one), so one
tenant's slow function or unreachable broker delayed every other tenant's
triggers. Every schedd also walked every enabled trigger on the platform,
with no ownership, so each had the same head-of-line wait.

ADR-972 kept brokers serial "because their ordering and offset semantics
are per partition or per stream". That holds per trigger: one poller per
trigger carries a Kafka reader's open offsets, a NATS consumer and a Redis
group member.

## Decision

1. **A lane per broker trigger.** Every trigger kind other than cron, queue
   and realtime runs its batch on the `broker_trigger_dispatch` work kind:
   `BrokerTriggerDispatchSlotsPerNode` (16) slots, overflow dropped, keyed by
   the trigger. One batch per trigger is in flight, so its partition and
   stream order and its offset bookkeeping are unchanged; a tick that finds
   the lane busy coalesces into it and the next tick resubmits. A trigger
   with an exclusive broker binding keeps the serial path, as push queues do.
2. **Polled by the app's node.** Only the schedd that owns the trigger's app
   polls it, with the invocation drain's rule: the app's node, or any node
   while the app is not yet placed. A trigger whose app moved closes its
   poller on the old node at the next tick, so a Kafka reader leaves the
   consumer group. Ownership is cached for 30 s per app.
3. **No shared poller lock during a build.** A broker connect (a NATS dial,
   a Redis ping) runs outside the lock every lane takes to find its poller.
4. **Eviction waits for the lane.** A trigger that is disabled or moved
   keeps its poller until its lane's batch finishes.

## Consequences

- A slow or unreachable broker, or a slow handler, holds only its own
  trigger. With N nodes, each polls its own apps' triggers.
- Up to 16 broker batches run at once per node; a 17th trigger waits for the
  next tick.
- Unplaced apps' triggers are still polled by every node, as before: their
  first delivery places the app. Hashing them onto one node would leave them
  unpolled while that node is down or has no gateway.
- A trigger's poller lives on one node, so Kafka partitions of one trigger
  are no longer split across schedds.

## Rejected alternatives

- **Lanes on every node.** Keeps NATS and Redis consumers racing across
  schedds and multiplies broker connections by the node count.
- **Several lanes per trigger.** Breaks per-partition order and the Kafka
  commit watermark, which live in one poller.
