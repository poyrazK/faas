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
