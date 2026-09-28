# Outbound webhook delivery health

These alerts use fleet-wide `schedd_webhook_delivery_*` metrics. They contain no
account or webhook identifiers. Customers can inspect one subscription through
`GET /v1/apps/{slug}/webhooks/{id}/health` (or its account release / platform
tenant equivalent), then open its deliveries and attempt history.

## Queue overdue

`FaasWebhookDeliveryQueueOverdue` means the oldest due `pending` or expired
`in_flight` delivery has been overdue for more than 15 minutes for five minutes.
Check that schedd is running and that `webhook: claim` errors are absent. Check
database availability and the size of the due queue:

```sql
SELECT status, count(*)
FROM app_webhook_deliveries
WHERE status IN ('pending', 'in_flight') AND next_attempt_at <= now()
GROUP BY status;
```

The gauge resets to zero when no delivery is overdue. A sustained large queue
may need more dispatch capacity; a small queue with growing age points to claim
or worker failure.

Schedd claims at most the free portion of its 64 delivery slots each tick.
`schedd_webhook_delivery_inflight` shows running workers, and
`schedd_webhook_delivery_saturated` is one when every slot is allocated
(including a claim still being fetched). If saturation stays at one while the
oldest-overdue age grows, inspect slow receivers and database write latency
before increasing capacity. A backlog with no saturation points to claim
failures or scheduler availability.

## Dead delivery spike

`FaasWebhookDeliveryDeadSpike` means more than 20 deliveries were newly marked
dead in ten minutes. Inspect recent attempt errors and response codes through
the scoped API or dashboard. A rise in HTTP 4xx usually needs a receiver fix;
zero response codes with signing or egress errors need platform investigation.
Retry dead deliveries only after the cause is resolved.

## Health poll failure

`FaasWebhookDeliveryHealthPollFailed` means schedd cannot read the oldest due
delivery. Check database connectivity and the `webhook: delivery health poll`
log. The overdue gauge retains its last value during a failed poll; treat it as
stale until the success gauge returns to one.

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
