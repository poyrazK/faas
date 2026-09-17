# Durable queue backlog and dead letters

The schedd publishes bounded per-app gauges for the durable queue:

- `schedd_queue_depth` is pending plus dispatching invocations.
- `schedd_queue_in_flight` is dispatching invocations with a live lease.
- `schedd_queue_oldest_age_seconds` is the age of the oldest pending item.
- `schedd_queue_dead_letter` is the terminal dead-letter count.

`FaasDurableQueueStalled` means work is present but the oldest pending item
has been waiting for more than five minutes. Check queue-depth scaling,
worker admission capacity, and the schedd lease-recovery log. A non-zero
`FaasDurableQueueDeadLetters` alert means retries exhausted; inspect the
dead-letter endpoint, fix the worker error, then replay deliberately.

## Symptom

`FaasDurableQueueStalled` means work is present but the oldest pending item
has waited more than five minutes. `FaasDurableQueueDeadLetters` means one or
more invocations exhausted their retry budget and reached the terminal state.

## Check

Inspect the app's queue depth, in-flight leases, and oldest age together.
Check worker admission capacity, queue-depth autoscaling decisions, and
schedd lease-recovery logs. For dead letters, inspect the queue dead-letter
endpoint and the recorded last error before attempting a replay.

## Recover

Restore worker capacity or correct the worker failure first. Once the queue
age is draining, leave autoscaling enabled and watch the gauges return to
zero. Replay dead-letter rows deliberately after the underlying error is
fixed; do not bulk replay while the backlog or lease pressure is still high.
