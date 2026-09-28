# Outbound webhook delivery health

These alerts use fleet-wide `schedd_webhook_delivery_*` metrics. They contain no
account or webhook identifiers. Customers can inspect one subscription through
`GET /v1/apps/{slug}/webhooks/{id}/health` (or its account release / platform
tenant equivalent), then open its deliveries and attempt history.

## Queue overdue

`FaasWebhookDeliveryQueueOverdue` means the oldest claimable due `pending` or
expired `in_flight` delivery has been overdue for more than 15 minutes for five
minutes. Deliveries held by a receiver cooldown or a full subscription claim
slot are tracked separately below.
Check that schedd is running and that `webhook: claim` errors are absent. Check
database availability and the size of the due queue:

```sql
SELECT status, count(*)
FROM app_webhook_deliveries
WHERE status IN ('pending', 'in_flight') AND next_attempt_at <= now()
GROUP BY status;
```

The claimable-age gauge resets to zero when no due delivery can be claimed. A
sustained large queue may need more dispatch capacity; a small queue with
growing age points to claim or worker failure.

Schedd claims at most the free portion of its 64 delivery slots each tick.
`schedd_webhook_delivery_inflight` shows running workers, and
`schedd_webhook_delivery_saturated` is one when every slot is allocated
(including a claim still being fetched). If saturation stays at one while the
oldest-overdue age grows, inspect slow receivers and database write latency
before increasing capacity. A claimable backlog with no saturation points to
claim failures or scheduler availability.

## Held backlog

`schedd_webhook_delivery_held_due_count` counts due deliveries that a
subscription cannot claim while its receiver cooldown is active or its live
claim slots are full. `schedd_webhook_delivery_oldest_held_due_seconds` measures
the age of the oldest such delivery. Neither gauge carries account or webhook
labels. Both reset to zero when no due delivery is held. The poll-success gauge
must be one before trusting either age gauge.

`FaasWebhookDeliveryHeldBacklog` warns when at least one due delivery remains
held for over an hour for ten minutes. Check scoped webhook health for
`cooling_down` or `probing`; inspect recent attempt status codes and
`Retry-After` deadlines. A full four-slot subscription without cooldown may
need receiver throughput or dispatch-capacity investigation. A deliberate long
receiver cooldown is expected to defer delivery, but the warning keeps the
customer impact visible. Do not manually replay a pending delivery to bypass a
receiver's rate limit.

Find the affected subscriptions, including rows held by a live recovery probe:

```sql
WITH live_claims AS (
  SELECT webhook_id, count(*) AS n
  FROM app_webhook_deliveries
  WHERE status = 'in_flight' AND next_attempt_at > now()
  GROUP BY webhook_id
)
SELECT w.id AS webhook_id, w.account_id, w.receiver_cooldown_until,
       count(*) AS held_due_count, min(d.next_attempt_at) AS oldest_held_due_at
FROM app_webhook_deliveries d
JOIN app_webhooks w ON w.id = d.webhook_id
LEFT JOIN live_claims live ON live.webhook_id = w.id
WHERE d.status IN ('pending', 'in_flight') AND d.next_attempt_at <= now()
  AND (coalesce(w.receiver_cooldown_until > now(), false)
       OR coalesce(live.n, 0) >= CASE WHEN w.receiver_cooldown_until IS NULL THEN 4 ELSE 1 END)
GROUP BY w.id, w.account_id, w.receiver_cooldown_until
ORDER BY oldest_held_due_at
LIMIT 20;
```

## Dead delivery spike

`FaasWebhookDeliveryDeadSpike` means more than 20 deliveries were newly marked
dead in ten minutes. Inspect recent attempt errors and response codes through
the scoped API or dashboard. A rise in HTTP 4xx usually needs a receiver fix;
zero response codes with signing or egress errors need platform investigation.
Retry dead deliveries only after the cause is resolved.

## Health poll failure

`FaasWebhookDeliveryHealthPollFailed` means schedd cannot read the fleet queue
snapshot. Check database connectivity and the `webhook: delivery health poll`
log. The claimable and held gauges retain their last values during a failed
poll; treat them as stale until the success gauge returns to one.

## Event outbox relay failure

`FaasWebhookEventOutboxRelayFailed` means a drained app park could not be
reconciled or a transactional webhook event could not reach the delivery
ledger. Usage statement events remain in `app_webhook_event_outbox`; incomplete
park requests remain in `app_park_transitions`. Check schedd logs for
`webhook: reconcile drained app parks` or `webhook: event outbox relay` and
database availability. Once recovered, the event outbox row is removed in the
same transaction that creates delivery rows.

```sql
SELECT event, count(*) AS pending_events, min(created_at) AS oldest_event_at
FROM app_webhook_event_outbox
GROUP BY event;

SELECT p.app_id, p.id AS transition_id, p.requested_at, a.status
FROM app_park_transitions p
JOIN apps a ON a.id = p.app_id AND a.park_transition_id = p.id
WHERE p.completed_at IS NULL AND p.superseded_at IS NULL
ORDER BY p.requested_at;
```

The request handlers also attempt immediate relay after statement finalization
or app drain, so empty queues are normal. A park transition may legitimately
wait while live instances drain. If the gauge is zero but both queries show no
pending rows, inspect the relay error before assuming events were lost. Avoid
manually inserting delivery rows; the unique event/subscription key and replay
worker own fan-out.

## Retention and storage

Schedd deletes up to 500 `succeeded` or `dead` deliveries per minute once
their last update is over 90 days old. Attempt history and any remaining
unified dead-letter projection are deleted with each delivery. `pending` and
`in_flight` deliveries remain until they finish. Dead deliveries can be
replayed through the webhook retry API throughout the retention window. A replay
updates the delivery timestamp and starts a new window when it finishes.

`FaasWebhookDeliveryRetentionFailed` means the cleanup or storage query has
failed for five minutes. Check `webhook: delivery retention pass` logs and
database connectivity. The `schedd_webhook_delivery_retention_failures_total`
counter records failed passes; `schedd_webhook_delivery_pruned_total` records
removed delivery rows.

`FaasWebhookDeliveryStorageLarge` means the delivery and attempt tables,
including their indexes, exceed 5 GiB. Check the age and status mix:

```sql
SELECT status, count(*), min(updated_at), max(updated_at)
FROM app_webhook_deliveries
GROUP BY status;
```

If terminal rows older than 90 days remain, compare their count with the
500-per-minute cleanup budget and check retention failures. If active rows
dominate, investigate queue dispatch and receiver failures. PostgreSQL may
reuse deleted space before its reported relation size falls; inspect vacuum
and table bloat if counts decline but storage stays high.
