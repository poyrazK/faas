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
