# Event-driven delivery

Operational owner: the scheduler owner (schedd: event routing
`pkg/sched/event_fanout.go`, the invocation drain `pkg/sched/drain.go` and
`pkg/sched/drain_fair.go` and `pkg/sched/drain_walk.go`, broker trigger lanes
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
[resumed drain walk](../adr/20261101091437-drain-walk-resumes.md),
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
   at most a share per app, so one slow consumer delays only itself. Each
   tick lists the head of the due backlog and resumes the rest of its walk
   where the last tick stopped, so a backlog the drain cannot dispatch (an
   account at its async cap, a slow app past its share) delays other apps'
   rows by at most one round of ticks instead of hiding them.
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
  admitted finish. `gregalectl flags enable --key events --reason <incident>
  --yes` resumes routing; nothing accepted is lost. Delivery-age limits keep counting while paused.
- **Pause one account:**
  `gregalectl flags deny --key events --account-id <uuid> --reason <why> --yes`.
  The account's publishing answers 503 and its customer events wait;
  platform-produced events still route.
- **Stop one consumer:** the account pauses its subscription with the
  delivery controls (`gregale event subscription pause`), and sees the pause.
  There is no operator command for one account's consumer; to stop routing
  for one account, deny it the `events` flag (above).
- **Code rollback:** schedd, apid and vmmd release independently
  ([release compatibility](../release-compatibility.md)); roll back the unit
  that introduced the regression. Receipts, recipients and dead-letter rows
  are forward-compatible within one release.

## Database restore or failover

Events live in the control-plane database, so moving it back in time changes
what was published and what was delivered.

- **Unplanned failover to the standby:** events accepted within the
  standby's replication lag are lost. `ControlPlaneFailover.md` step 4 gives
  the window and the customer notice.
- **Restore to an earlier point** (`migration-rollback.md`, a backup
  restore): every event accepted after the restore point is lost. Every
  delivery that finished after it is pending again and runs again, so
  handlers see those events twice.

After either one:

1. Record the window: from the restore point (or the promote play's last
   replay time) to the moment the old database stopped taking writes.
2. Before customers resume publishing in bulk, decide whether routing
   should wait. To hold it while you assess,
   `gregalectl flags disable --key events --reason <incident> --yes` once
   apid is up; deliveries already pending wait and nothing is lost.
3. Tell the affected accounts the window, with both halves of the
   consequence:
   - **Lost:** republish events from the window with the same identity
     (event id, or idempotency key). A survivor answers `duplicate`; a lost
     one is accepted again.
   - **Repeated:** handlers may receive events from the window again. The
     `x-gregale-event-id` header is unchanged, so handlers that deduplicate
     by it are safe (`docs/events.md`, delivery identity).
4. Re-enable routing (`gregalectl flags enable --key events --reason
   <incident> --yes`) and watch `FaasEventRoutingStalled` and
   `FaasDeadLetterSpike` while the backlog drains.

## FaasEventRoutingStalled

No schedd has completed a routing sweep for fifteen minutes, or sweeps run
but every claim fails and nothing is routed. Events are accepted but none is
delivered.

1. Confirm the signal:
   `sum by (job, instance) (increase(schedd_loop_work_duration_seconds_count{kind="event_fanout"}[10m]))`
   and `sum by (job, instance, outcome) (rate(schedd_loop_work_total{kind="event_fanout"}[10m]))`.
   Only `coalesced` and `dropped` outcomes mean a sweep is stuck holding the
   kind's slot. Sweeps that run but cannot claim show as
   `sum by (job, instance, outcome) (increase(schedd_event_routing_outcomes_total[10m]))`
   with only `claim_error` rising.
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

## FaasEventRoutingFailing

More than half of the event recipients routed in fifteen minutes failed or
were put back to retry. Routing runs but does not deliver.

1. Break it down:
   `sum by (job, instance, outcome) (increase(schedd_event_routing_outcomes_total[15m]))`.
2. In schedd's logs, `sched: event recipient routing failed` and
   `sched: event recipient fanout permanently failed` carry the failure code
   and `subscription_id`. One subscription whose target app is gone or not
   deployed is the usual cause; the account sees it in `gregale event
   backlog` and its dead letters.
3. Many subscriptions at once: check the database (`FaasPostgresDown`,
   `FaasDBPoolStarved`) and the invocation queue.

Resolution: clears when fewer than half of the recipients fail, or fewer
than 50 fail in fifteen minutes.

## FaasEventRoutingSweepSlow

Sweeps finish, but the p95 takes longer than 30 seconds. Events arrive late.

1. Break the sweep down by its database work: the recipient claim, adoption
   claim, recovery and backfill queries appear in `pg_stat_statements`.
2. A large backlog of due recipients after an outage drains pass after pass
   (`schedd_loop_work_total{kind="event_fanout",outcome="rerun"}` rises);
   each pass is still one sample here, so a slow p95 means slow passes. Check
   `schedd_event_delivery_capacity_waiting` and the oldest pending age.
3. Many running recovery or backfill jobs slow every sweep. Find them on the
   control-plane database (`select account_id, app_id, count(*) from
   event_recovery_jobs where state = 'running' group by 1, 2`, and the same
   on `event_replay_jobs`). The account lists and pauses its own with
   `gregale event recovery list` and `gregale event recovery pause`.

Resolution: clears when the p95 falls back under 30 seconds.

## FaasEventDeliveryCapacityBacklog

More than 10,000 deliveries platform-wide wait for consumer capacity
(ADR-614 delivery limits).

1. Find the accounts: the `event_routing_backlog` projection on the
   control-plane database, grouped by `account_id`. (`gregale event backlog`
   shows an account its own backlog; it cannot read another account's.)
2. One account: its consumer is slower than its producers. It is the
   customer's to fix (scale the consumer, raise its concurrency, or pause the
   subscription); nothing is lost while it waits.
3. Many accounts: deliveries are not being executed. Check the invocation
   drain (schedd logs `drain:`), node capacity (`FaasHighResidentRamPct`) and
   the gateway.

Resolution: clears below 10,000 waiting.

## FaasDeadLetterSpike

More than 200 invocations, trigger records and event recipients whose
routing failed reached the dead-letter queue in ten minutes.

1. Break it down:
   `topk(10, sum by (app, error_kind) (increase(faas_dlq_events_total[10m])))`,
   and routing failures with
   `sum by (job, instance) (increase(schedd_event_routing_outcomes_total{outcome="failed"}[10m]))`
   (see `FaasEventRoutingFailing`).
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
   error (`gregale trigger list`, run by the account, or schedd logs
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
