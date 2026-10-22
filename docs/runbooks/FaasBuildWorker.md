# Builder claim, slot and durable-worker diagnostics

Applies to `FaasBuilderClaimsFailing`, `FaasBuilderRequeuesFailing`,
`FaasBuilderWorkerStalled` and `FaasBuilderQueueTelemetryUnavailable`.
All four warnings are operator-internal.

For delayed or dropped fast-path hints, inspect the separate bounded build/cancel
queues in [FaasBuildNotifications.md](FaasBuildNotifications.md). Notification
dispatch and durable polling have independent progress and recovery evidence.

## Establish usable evidence

```promql
builderd_build_queue_observation_healthy
builderd_build_slots_in_use{job="builderd"}
builderd_build_slots_limit{job="builderd"}
builderd_build_worker_enabled{job="builderd"}
builderd_build_worker_inflight{job="builderd"}
builderd_build_worker_progress_age_seconds
builderd_build_worker_progress_allowance_seconds
```

Telemetry is usable only when the builder is scraped, worker timestamps and
phase agree, budgets are positive, slot usage is within its limit, and all
fixed claim/requeue/slot-decision series are present. Missing or invalid input
is unknown and raises the telemetry warning after five minutes. A sibling
builder cannot supply missing evidence. Failed scrapes use existing daemon
health alerts; absent discovery does not prove an entire installation is down.

Slot gauges describe this builder process's allocator. The enforced limit is
currently one. A reserved slot can span preparation of VM execution and cleanup;
it does not prove a VM is running. Preparation before admission and cache-hit
completion consume no slot. Database `running` count can therefore differ from
reserved slots. Do not infer idle workers or free host RAM from zero slots, or
sum process limits to claim safe physical capacity on a shared host.

## Explain waiting work

```promql
sum by (job, instance, source, outcome) (rate(builderd_build_claims_total{job="builderd"}[5m]))
sum by (job, instance, outcome) (rate(builderd_build_slot_decisions_total{job="builderd"}[5m]))
sum by (job, instance, reason, outcome) (rate(builderd_build_requeues_total{job="builderd"}[5m]))
sum by (job, instance, outcome) (rate(builderd_build_worker_iterations_total{job="builderd"}[5m]))
```

Claims distinguish `poll` and `notification`. `claimed` means the store returned
a claim. `not_claimed` includes an empty queue, another claimant, a missing or
terminal notification target, and cache-affinity deferral. It never proves the
whole queue is empty. Compare fresh fleet queue depth/oldest wait separately in
[FaasBuildQueueBacklog](FaasBuildQueueBacklog.md).

Slot decisions distinguish `granted`, `limit` and `policy`. Repeated `limit`
with a healthy occupied slot is normal saturation. Existing queue-age warnings
still describe customer waiting time; do not change the slot fence to clear
them. Consult capacity and build execution before adding an eligible builder.

Requeues distinguish `no_slot`, `source_unavailable`, `context_canceled` and
`lookup_failure`. `accepted` means the existing store call succeeded; another
claim can immediately take that queued row. `not_requeued` includes protective
claim fencing or a missing/terminal row. Neither proves a database failure.
`canceled` means an error matched an actually canceled caller context. An
internal store deadline with a live caller is an `error`.

A `no_slot` iteration may hide a failed requeue because legacy `ProcessNext`
still returns `ErrNoSlot`. Inspect the requeue outcome and durable build state:
the row can remain running until existing recovery handles it. These counters
observe each store attempt, including state-mutation retries; they are not
unique-build counts. Error strings/build IDs remain in existing logs.

## Interpret worker progress and warnings

The durable poller executes the complete build in `ProcessNext`, including
source handling, cache completion and VM work. A fifteen-minute build can occupy
one iteration. Notification workers are independent and cannot renew the durable
poller's progress timestamps.

Idle allowance is twice `builderd_build_worker_interval_seconds` plus 60 s, and
the in-flight allowance carries the same 60 s. The margin covers scrape and
evaluation staleness: Prometheus sees a completion only at its next scrape, so
with a 2 s poll the observed age reaches ~17 s even on a healthy worker. Before any
completion, age starts at initialization; after completion, age starts at the
latest completed iteration. While in-flight, age starts at its attempt and the
allowance additionally includes `builderd_build_worker_busy_allowance_seconds`:
the effective normalized running-build recovery threshold. This is observation,
not a new execution deadline. The stalled warning holds a breach for five minutes.

`iterations_total{outcome="completed"}` means `ProcessNext` returned nil, not
that a VM started or a customer build succeeded. Idle/no-claim, slot denial and
failed iterations also advance completion. Use claim/requeue error counters and
existing build outcomes to explain repeated failures. Parent cancellation or
intentional drain sets enabled to zero. An unexpected cancel result with a live
parent retains expectation, allowing the stopped poller to be detected.

Claim/requeue failure warnings require more than 10% errors and at least three
errors in each rolling five-minute window for five minutes. Claims are checked
per source, requeues per reason. Canceled calls are excluded. Requeue protective
fences are excluded from the ratio; no-claim responses count as healthy claims.
An isolated transient burst does not satisfy the continuous hold. Recovery can
take a rolling window to clear the failure ratio.

## Investigate and recover

1. For telemetry unavailable, inspect `/metrics`, scrape/relabel configuration,
   clocks, and the same builder instance's complete metric families. Do not
   substitute another process's slot count or progress timestamps.
2. For claim failures, inspect database connectivity, pool pressure and builderd
   claim logs. Healthy notification work cannot prove polling claims work, or
   vice versa. Preserve claim selection, cache affinity and fairness settings.
3. For requeue failures, inspect `requeue on no-slot`, source-lag/storage,
   canceled-claim and lookup-failure warnings. Verify current claim ownership;
   preserve claim tokens/start times and durable recovery obligations.
4. For progress stalls, compare idle/in-flight state and its configured allowance,
   then capture goroutines and logs before restarting. Distinguish a blocked
   store operation, a slow full build and an exited poller. Use
   [FaasBuildRecovery](FaasBuildRecovery.md) for running claims and VM cleanup.
5. Confirm fresh usable evidence, resumed poll completions, declining operational
   error rates and improving fleet queue wait. Disappearing inputs are not
   recovery. These metrics perform no admission changes or automatic remediation.
