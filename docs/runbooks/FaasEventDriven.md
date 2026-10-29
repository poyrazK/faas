# Event-driven delivery

Operational owner: the scheduler owner (schedd: event routing
`pkg/sched/event_fanout.go`, the invocation drain `pkg/sched/drain.go` and
`pkg/sched/drain_fair.go`, broker trigger lanes
`pkg/sched/broker_trigger_lanes.go`), with apid for the publish, subscription
and dead-letter API (`pkg/apid/appevents`) and vmmd for in-app publishing
(`cmd/vmmd/guest_event_flags.go`). Pages go to `faas-platform-oncall`
([escalation](../ops/escalation.md)). Capabilities: `events` and its
`event-*` sub-features, `event-triggers`, `queues` (all preview), and
`failed-events-console`. ADRs:
[ADR-614](../adr/614-event-delivery-backpressure.md) (delivery capacity),
[ADR-647](../adr/647-independent-event-routing-default.md) (recipient claims),
[routing failures in the dead-letter queue](../adr/20261026163443-event-routing-failures-in-the-dead-letter-queue.md),
[events kill switch](../adr/20261027125625-events-kill-switch.md),
[fair drain](../adr/20261027160930-continuous-fair-drain.md),
[broker trigger lanes](../adr/20261027162741-broker-trigger-lanes.md) and
[the paging set](../adr/1236-paging-set.md).

Covers `FaasEventRoutingStalled` (page), `FaasEventRoutingSweepSlow`,
`FaasEventDeliveryCapacityBacklog`, `FaasDeadLetterSpike`,
`FaasBrokerTriggerPollErrors` and `FaasEventsPublishingPaused` (warn).

## How events flow

1. A publish (API, batch, app producer route, platform-tenant route, in-app
   runtime, commit relay, object storage, inbound webhook automation) writes
   an `event.published` row; a trigger turns it into a receipt in
   `event_fanout_outbox` in the same transaction.
2. Every schedd runs the routing sweep (`event_fanout` loop work, one slot,
   every 5 s and on each `event_published` notify). It claims receipts,
   captures one recipient per matching subscription and routes each recipient
   into an invocation (or a workflow run).
3. The invocation drain delivers those invocations: a shared pool per node,
   at most a share per app, so one slow consumer delays only itself.
4. Broker triggers (Kafka, NATS, Redis Streams, SQS-compatible) are polled by
   the schedd that owns their app, one batch in flight per trigger.
5. Settled receipts are pruned after the plan's window; their routing
   failures move to the dead-letter queue as `event_route` entries.

## Release and rollback

- **Pause the whole event bus:**
  `gregalectl flags disable --key events --reason <incident> --yes`. Within
  seconds every customer publish path answers `503 events_paused` with
  `Retry-After`, nothing new is accepted from customers, and schedd routes
  nothing. Object-storage notifications, inbound webhook automations and
  commit relay events are still accepted and wait. Invocations already
  admitted finish. `gregalectl flags enable --key events` resumes routing;
  nothing accepted is lost. Delivery-age limits keep counting while paused.
- **Pause one account:**
  `gregalectl flags deny --key events --account-id <uuid> --reason <why>`.
  The account's publishing answers 503 and its customer events wait;
  platform-produced events still route.
- **Stop one consumer:** pause its subscription with the delivery controls
  (`gregale events subscription-pause`); the account sees it.
- **Code rollback:** schedd, apid and vmmd release independently
  ([release compatibility](../release-compatibility.md)); roll back the unit
  that introduced the regression. Receipts, recipients and dead-letter rows
  are forward-compatible within one release.

## FaasEventRoutingStalled

No schedd has completed a routing sweep for fifteen minutes. Events are
accepted but none is delivered.

1. Confirm the signal:
   `sum by (job, instance) (increase(schedd_loop_work_duration_seconds_count{kind="event_fanout"}[10m]))`
   and `sum by (job, instance, outcome) (rate(schedd_loop_work_total{kind="event_fanout"}[10m]))`.
   Only `coalesced` and `dropped` outcomes mean a sweep is stuck holding the
   kind's slot.
2. Check every schedd is up (`FaasDaemonDown`, `FaasDaemonNotReady`) and that
   Postgres answers (`FaasPostgresDown`, `FaasDBPoolStarved`).
3. In schedd's logs, look for `sched: claim event fanout failed`,
   `sched: claim event recipient failed` and long database waits. A sweep
   blocked on a lock shows in `pg_stat_activity` as a long-running
   `event_fanout_outbox` or `event_fanout_recipients` statement.
4. If one sweep is hung, restart that schedd; the claims it held lapse and
   another sweep adopts them.
5. If the database is slow for everyone, treat it as a database incident.

Resolution: clears once any schedd completes a sweep.

## FaasEventRoutingSweepSlow

Sweeps finish, but the p95 takes longer than 30 seconds. Events arrive late.

1. Break the sweep down by its database work: the recipient claim, adoption
   claim, recovery and backfill queries appear in `pg_stat_statements`.
2. A large backlog of due recipients after an outage drains at the sweep
   budget; check `schedd_event_delivery_capacity_waiting` and the oldest
   pending age.
3. Many running recovery or backfill jobs slow every sweep; list an
   account's with `gregale events recovery-list` and pause the largest
   (`gregale events recovery-pause`).

Resolution: clears when the p95 falls back under 30 seconds.

## FaasEventDeliveryCapacityBacklog

More than 10,000 deliveries platform-wide wait for consumer capacity
(ADR-614 delivery limits).

1. Find the accounts: `gregale events backlog` for a suspected account, or
   the `event_routing_backlog` projection grouped by account.
2. One account: its consumer is slower than its producers. It is the
   customer's to fix (scale the consumer, raise its concurrency, or pause the
   subscription); nothing is lost while it waits.
3. Many accounts: deliveries are not being executed. Check the invocation
   drain (schedd logs `drain:`), node capacity (`FaasHighResidentRamPct`) and
   the gateway.

Resolution: clears below 10,000 waiting.

## FaasDeadLetterSpike

More than 200 invocations and trigger records reached the dead-letter queue
in ten minutes.

1. Break it down:
   `topk(10, sum by (app, error_kind) (increase(faas_dlq_events_total[10m])))`.
2. One app with `failed` or `max_attempts`: its handler is failing; the
   customer replays after fixing it (`gregale dlq list <app>`).
3. Many apps, or `timeout`, `broker_error` or `other`: a platform cause.
   Check the gateway, wakes (`gateway_wake_latency_seconds`) and broker
   connectivity.

Resolution: clears once the rate falls under 200 per ten minutes.

## FaasBrokerTriggerPollErrors

More than half of one broker kind's trigger polls failed for fifteen
minutes.

1. Find the triggers: each trigger's consumer health shows its last poll
   error (`gregale triggers` for the account, or schedd logs
   `sched trigger tick: dispatch lane`).
2. One trigger: the customer's broker is unreachable or its credentials are
   wrong; the trigger's health tells the customer.
3. Many triggers: check tenant egress (the egress dialer's denials in schedd
   logs, `FaasTenantEgressAbuse`), DNS and the sealed-credential path.

Resolution: clears when errors fall to half of polls or fewer.

## FaasEventsPublishingPaused

The `events` kill switch has been off for at least an hour while producers
keep publishing; each publish answers 503 and routing is held.

1. Check why it was turned off: `gregalectl flags show --key events`.
2. Once the incident is over, `gregalectl flags enable --key events --reason
   <incident> --yes`. Routing resumes and producers' retries are accepted.

Resolution: clears once publishes stop being refused.
