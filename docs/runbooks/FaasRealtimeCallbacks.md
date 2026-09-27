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

`FaasRealtimeCallbackReplayRestarting` means the replay supervisor restarted
at least three times in 15 minutes and the condition persisted for five
minutes. Check `realtimed_callback_replay_supervisor_restarts_total` and review
nearby `realtimed` log entries for the underlying filesystem or outbox error.
The counter resets when `realtimed` restarts.

`FaasRealtimeCallbackOutboxFull` means at least one message or disconnect
callback could not be persisted before its admission deadline. Check
`realtimed_callback_outbox_full_total`, pending bytes and capacity, and replay
progress. Restore callback receiver or node storage health. A live connection
whose message could not be persisted is closed with WebSocket code 1013 so the
client can reconnect and retry according to its application protocol.

`FaasRealtimeCallbackOutboxAdmissionFailed` means the outbox rejected an event
because of a storage or admission error other than capacity exhaustion. Check
`realtimed_callback_outbox_admission_errors_total`, realtimed logs, free disk
space and inodes, directory ownership, and filesystem sync errors. A live
connection whose message could not be saved is closed with WebSocket code 1013.
Restore node-local outbox storage health; clients can then reconnect and retry
according to their application protocol.

`FaasRealtimeCallbackUnpersisted` means a direct HTTP callback failed
while no durable outbox was configured. Check
`realtimed_callback_unpersisted_failures_total` and the receiver's health. The
client connection is closed with code 1013 after a failed message callback.
Configure a durable outbox for production delivery, and make clients retry
unacknowledged messages according to their application protocol.

`FaasRealtimeCallbackOutboxNearCapacity` means pending callback data has stayed
above 80% of the outbox byte limit for five minutes. Check
`realtimed_callback_pending_bytes` and
`realtimed_callback_pending_capacity_bytes`, then compare with the oldest
pending age and replay progress. Restore callback receiver or node storage
health so the replay loop can drain the queue; reaching the limit causes new
callback records to be rejected.

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
