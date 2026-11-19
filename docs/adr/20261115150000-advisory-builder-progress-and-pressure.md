# ADR-20261115150000 · Advisory builder progress and pressure

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-15
- **Extends:** [work-loop reports](20261115140001-advisory-work-loop-health.md)
  and [observed history](20261115100001-observed-build-history.md).

A ready builder with a responsive runtime heartbeat can stop processing builds.
Reuse existing builderd durable-poller, build-slot and build/cancel notification
queue gauges in optional `builder_health` evidence for explicit v2 profiles.
V1 coverage remains frozen. This does not add instrumentation, probes, alerts,
database reads, execution deadlines, admission changes or automatic remediation.
Image-preparation and build-to-image handoff evidence remain a separate step.

Read the fifteen fixed gauge families in two instant queries: one fixed family
selector and its original timestamps, preserving the metric name in a reviewed
label before `timestamp` drops it. Queue names are the closed build/cancel set.
Validate full series frames, exact original up-target membership and duplicates.
The seven worker, two slot and six-per-queue signals yield twenty-one signal
keys, each bounded by the existing 4096-sample limit. Batch bounds are the
product of that same per-signal limit and the closed policy, not a new quota.
Run the one builder reader alongside loop readers, under the same pinned
evaluation time and shared three-second runtime evidence deadline. Skip reads
without up targets; retain missing members and unknown configured inventory.

Validate worker, slot and each queue independently. Each usable component needs
fresh successful up and fresh original metric clocks, using existing 120-second
age and 30-second boundary/future skew. Expose the earliest original sample time
within each complete component. Missing, stale, conflicting or invalid components
omit values. Preserve independently valid overdue-worker and pressure evidence
when another component is incomplete. Raw labels, endpoints and provider errors
never leave the domain. Decode required zero-valued JSON fields explicitly so
absent/null enabled flags, counts and ages cannot become complete zero reports.

Worker state is waiting, working, draining or overdue. Before the first completed
iteration, initialization is the progress origin; otherwise use last completion,
or last attempt while in-flight. Match existing recording-rule allowance:
two configured poll intervals plus the existing 60-second scrape/evaluation
margin, additionally the configured busy allowance while in-flight. Reuse twice
the central 30-second clock tolerance for that margin. Validate chronological
consistency and finite nonnegative durations with the central ten-year sanity
bound. This is observation, not the existing alert's five-minute hold, build
success, VM execution, an enforced execution deadline or proof of continuous work.

Queue observations retain waiting/active/capacity/workers, enabled and oldest
waiting age, with waiting plus active bounded by capacity plus workers to allow
dispatch handoffs. A zero buffer is valid. Zero waiting requires absent oldest
enqueue time and zero wait age. Notification hints are not durable fleet backlog;
oldest enqueue time may precede the release boundary. Slots are local allocator
reservations, not running VMs or available physical capacity. Equal usage/limit
is saturation, not failure. Reuse the existing 4096 telemetry sanity bound for
counts, without changing producer configuration or limits. Counter-rate/error
assessment, per-build IDs and fleet aggregation are outside this increment.

Full-set counts precede the separate 32-target preview. Fresh overdue workers
take aggregate and preview priority, while incompleteness remains explicit.
Builder detail yields first within the existing 64 KiB API and 1 MiB report
envelopes, with omissions counted; summaries always cover the full inspected set.
The CLI and closed Python adapter keep the optional section beside existing
runtime identity evidence. Missing or malformed optional data preserves valid
identity/readiness/process/loop data and health qualification, polls and exits.

Only aggregate state/count/incompleteness changes enter the existing shared
16-event/16 KiB history; routine clocks, ages and queue depths do not add events.
Keep first/last overdue and enabled-within-allowance observations, last sampled
regression/recovery and sticky incomplete evidence outside that event retention.
Gaps break the throughout flag but preserve pending overdue evidence. Intentional
drain cannot record recovery. Recovery requires a later complete aggregate of
enabled workers within allowance; it can include a restarted/replaced process
and does not attest recovery of the same process or events between polls.

Verify long builds, initialization, idle and in-flight overdue work, drain,
pressure and dispatch handoffs, component and source failures, release/future
clocks, duplicate frames, unknown/empty inventory, faults outside previews,
malformed optional JSON, restart/gap recovery, owned history, envelope fitting,
all profiles and SDK round trips without contacting production.
