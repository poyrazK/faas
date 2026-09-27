# Managed realtime callback delivery

`FaasRealtimeCallbackReplayStalled` means the node has pending callbacks older
than five minutes and replay has delivered none for five minutes. Check
`realtimed_callback_pending`, `realtimed_callback_oldest_pending_age_seconds`,
and `realtimed_callback_replay_deliveries_total`, then inspect the `realtimed`
logs for outbox or receiver errors. The replay supervisor retries unexpected
outbox failures with a delay capped at 30 seconds. Confirm the node-local
outbox is writable and has free space; replay resumes after the underlying
storage problem clears.

## Symptom

`FaasRealtimeCallbackDeadLettersNearCapacity` means the node's retained
callback dead letters use more than 80% of their configured byte limit.
`FaasRealtimeCallbackDeadLettersPresent` means the node has retained one or
more dead letters that need operator review.
`FaasRealtimeCallbackDeadLettersEvicted` means at least one dead letter was
removed in the past hour, including during daemon startup.

## Check

1. Identify the affected `realtimed` target in Prometheus. Check
   `realtimed_callback_dead_letters`, `realtimed_callback_dead_letter_bytes`,
   `realtimed_callback_dead_letter_capacity_bytes`, and
   `realtimed_callback_dead_letter_evictions_total`. Compare with
   `realtimed_callback_errors_total` and `realtimed_callback_pending`.
2. On that node, inspect `/var/lib/faas/realtime-callbacks/dead/` (or the
   configured `FAAS_REALTIME_CALLBACK_OUTBOX` path). Files contain callback
   payloads and bearer tokens; keep access restricted to operators. Review
   callback status and application logs to correct the delivery failure.

## Recover

1. Copy records needed for investigation or manual replay to a restricted
   location before retention evicts them. The outbox does not replay dead
   letters automatically. If replaying an event, use its event ID to
   deduplicate application effects.
2. If the configured limit is too small for the investigation window, set
   `FAAS_REALTIME_CALLBACK_DEAD_MAX_BYTES` to a positive byte count in the
   realtimed environment and restart the daemon. Size the limit against
   available node disk space. Lowering it evicts oldest files at startup.

The default dead-letter limit is 64 MiB, separate from the 64 MiB pending
callback limit. Retention removes the oldest files by modification time,
breaking ties by event ID. Evictions are counted and surfaced in metrics.

Pending callback recovery uses eight workers by default. Set
`FAAS_REALTIME_CALLBACK_REPLAY_WORKERS` to tune the per-node concurrency; values
above 32 are capped. Events from one connection remain ordered, while callbacks
from different connections can run at the same time. Account for this
concurrency when sizing callback receivers across the fleet.
