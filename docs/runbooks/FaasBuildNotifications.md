# Builder notification queues

`FaasBuilderNotificationWaitHigh`, `FaasBuilderNotificationDropsHigh` and
`FaasBuilderNotificationTelemetryUnavailable` describe builderd's bounded
LISTEN/NOTIFY work queues. They are internal operator warnings. The fixed queue
labels are `build` and `cancel`; all gauges are local to one builder process.

Build hints call ProcessOne. Cancellation hints call the existing VM cancel
helper, whose results are exported separately as
`builderd_ops_total{op="build_cancel",code=~"ok|vmmd_error|not_wired"}`.
Postgres notifications are hints. Dropped build hints rely on durable polling;
cancellation teardown is persisted and retried by the cleanup reaper. A dropped
hint does not prove a lost build, and an accepted hint does not prove execution
or successful cancellation. These metrics exclude malformed JSON rejected by
the dispatcher before enqueue; inspect existing payload warnings in logs.

## Inspect the fast path

```promql
builderd_notification_queue_waiting{job="builderd"}
builderd_notification_queue_active{job="builderd"}
builderd_notification_queue_capacity{job="builderd"}
builderd_notification_queue_workers{job="builderd"}
builderd_notification_queue_oldest_wait_seconds
builderd_notification_queue_enabled{job="builderd"}
sum by (job, instance, queue, outcome) (
  increase(builderd_notification_queue_enqueues_total{job="builderd"}[5m])
)
```

Waiting counts accepted IDs that have not entered a handler. Active counts the
handlers currently executing; it is not a resident-VM count. The default buffer
has 64 entries and two workers per queue. Capacity is the buffered channel's
bound. A receive-to-handler handoff can briefly add up to one waiting ID per
worker outside the buffer, so waiting plus active is bounded by capacity plus
workers. This does not create additional workers or change the enqueue bound.

The raw `builderd_notification_queue_oldest_enqueued_timestamp_seconds` is the
oldest waiting ID's Unix enqueue time, or zero when no IDs wait. Active IDs are
excluded. Duplicate hints coalesce while queued or active and do not reset age.
Age grows in Prometheus even without fresh arrivals. A long active build with
no waiting hints has zero waiting age. Queued hints behind busy handlers can
raise a delay warning during otherwise healthy work; capture the queue and
handler evidence before diagnosing a stall.

Enqueue outcomes are initialized even before traffic:

- `accepted`: entered the fast path; no execution/success guarantee.
- `coalesced`: the same ID was already queued or active; harmless deduplication.
- `full`: buffer rejected a new ID; the hint requires durable recovery.
- `stopping`: rejected after worker-context cancellation.
- `invalid`: empty ID rejected at enqueue. Payload validation usually rejects
  these before enqueue in production.

`FaasBuilderNotificationWaitHigh` requires oldest waiting age above 300 seconds
for build hints or 60 seconds for cancellation hints, continuously for five
minutes. `FaasBuilderNotificationDropsHigh` requires full drops above 10% of
accepted/full attempts and at least three drops in each rolling five-minute
window, sustained for five minutes. Coalesced, invalid and stopping attempts do
not dilute the ratio. One brief burst does not satisfy the sustained hold.

Both warnings require healthy telemetry and enabled expectation. Canceling the
queue context sets enabled to zero; pending and active work remain observable.
Zero expectation does not prove successful drain. Check the existing
`notification queue drain incomplete` logs when shutdown times out.

## Establish evidence before intervention

For waiting build hints, compare durable queue age/count, claim outcomes, slot
decisions and worker progress in [FaasBuildQueueBacklog.md](FaasBuildQueueBacklog.md)
and [FaasBuildWorker.md](FaasBuildWorker.md). Durable polling can claim a build
before its queued hint runs; notification waiting is not fleet build waiting.

For cancellation delay or drops, check actual cancel outcomes and durable cleanup
age/retry state in [FaasBuildRecovery.md](FaasBuildRecovery.md). Capture builderd
and vmmd logs and, when available, goroutine evidence around blocked driver calls.
Do not assume the VM was stopped because enqueue succeeded or a callback returned.

Upstream DB notify hub drops/reconnects are separate transport evidence. A healthy
hub does not establish downstream work-queue health. Compare the build and cancel
queues separately and keep the affected `job` and `instance`; aggregate traffic
from another queue or process must not hide a local problem.

## Unknown telemetry

```promql
builderd_notification_queue_observation_healthy
up{job="builderd"}
```

The healthy recording is one per complete, consistent queue and absent otherwise.
Derived age is zero only for a known-empty healthy queue; missing evidence stays
absent. Each successfully scraped builder is expected to export both queues.
Missing capacity, workers, state, enqueue outcomes, future/inconsistent oldest
timestamps, impossible occupancy or invalid enabled state raises the telemetry
warning after five minutes. A healthy sibling queue or builder cannot fill the
gap. Failed scrapes use existing target-health alerts.

Inspect deployed binary version, scrape/metric relabeling, clock synchronization
and raw exposition first. Restore trustworthy evidence before diagnosing queue
health. These rules provide observation only; they do not resize queues, restart
daemons, force cancellation, or bypass admission and claim fences.
