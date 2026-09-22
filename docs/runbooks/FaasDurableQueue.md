# Durable queue backlog and dead letters

The schedd publishes bounded per-app gauges for the durable queue:

- `schedd_queue_depth` is pending plus dispatching invocations.
- `schedd_queue_in_flight` is dispatching invocations with a live lease.
- `schedd_queue_oldest_age_seconds` is the age of the oldest pending item.
- `schedd_queue_dead_letter` is the terminal dead-letter count.
- `schedd_queue_binding_worker_demand` is the uncapped worker demand for each
  queue binding before app and account caps are applied.

Kafka bindings also publish `schedd_esm_consumer_lag_messages` and
`schedd_esm_consumer_lag_age_seconds` by bounded source shard. These are
broker-native lag snapshots, distinct from the dispatch latency histogram.

The unified EPIC #1278 ledger exposes cross-source operator signals:

- `faas_dlq_events_total{app,error_kind}` counts durable routing decisions.
- `faas_dlq_replayed_total{app,status}` counts replay attempts.
- `faas_dlq_purged_total{app,status}` counts ledger purges.

The corresponding audit kinds are `app.dlq.event_routed`,
`app.dlq.event_replayed`, and `app.dlq.purged`.

For a binding-scoped control-plane snapshot, use
`GET /v1/apps/{slug}/queue-bindings/{id}/status`. It combines the binding's
push projection (`consumer_state` is `active`, `paused`, `not_configured`, or
`external`) with depth, live leases, dead letters, and oldest-pending age.
`consumer_liveness` is `healthy`, `degraded`, `stale`, or `not_observed` for
push bindings and `external` for pull bindings. The response also includes the
last poll/success/error timestamps, the last error text, and broker-native lag
when the source exposes it. A stale snapshot means schedd has not completed a
poll in 30 seconds; use the schedd metrics for fleet-wide liveness and alerting.

For the common operator workflow, the `gregale queue status <slug>` command
combines app queue state, the scaling target, every binding's consumer
liveness, broker lag, dead letters, and the last consumer error. Use `--json`
for automation. The command is read-only and does not lease or acknowledge
messages.

## Simple workload setup

For the common worker/job case, `gregale queue setup <slug>` (or
`PUT /v1/apps/{slug}/queue-workload`) is the one-step path. The control plane
idempotently converges the app's `default` push binding, consumer projection,
and `queue_depth` scaling target (defaults: queue `default`, target depth 10,
per-worker concurrency 1). Use `--queue-name`, `--target-depth`, and
`--max-concurrency` for routine overrides; `--force` is required to move an
existing default binding to another queue. Advanced bindings and policies
remain available through the individual queue APIs.

`FaasDurableQueueStalled` means work is present but the oldest pending item
has been waiting for more than five minutes. Check queue-depth scaling,
worker admission capacity, and the schedd lease-recovery log. A non-zero
`FaasDurableQueueDeadLetters` alert means retries exhausted; inspect the
dead-letter endpoint, fix the worker error, then replay deliberately.

## Symptom

`FaasDurableQueueStalled` means work is present but the oldest pending item
has waited more than five minutes. `FaasDurableQueueDeadLetters` means one or
more invocations exhausted their retry budget and reached the terminal state.
`FaasBrokerConsumerLagHigh` means a broker high-water mark is more than 1,000
messages ahead of the schedd consumer.

## Check

Inspect the app's queue depth, in-flight leases, and oldest age together.
Check worker admission capacity, queue-depth autoscaling decisions, and
schedd lease-recovery logs. For dead letters, inspect the queue dead-letter
endpoint and the recorded last error before attempting a replay. For broker
lag, check the binding's group/partition health, retry rates, and broker
connectivity.

## Recover

Restore worker capacity or correct the worker failure first. Once the queue
age is draining, leave autoscaling enabled and watch the gauges return to
zero. Replay dead-letter rows deliberately after the underlying error is
fixed; do not bulk replay while the backlog or lease pressure is still high.
