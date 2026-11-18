# Observed delayed-task dispatch outcomes

Date: 2026-11-17

## Context

The durable invocation drain already exports settled delayed-task dispatch
attempts in `schedd_delayed_task_dispatch_total`. Verification reports expose
HTTP cron outcomes but do not yet carry delayed-task attempts. Retries and final
failure dispositions need to remain visible after later quiet observations.
The producer maps application errors into `failed`, so failure classification
alone cannot identify a platform incident.

## Decision

Add optional advisory `delayed_task_dispatch` evidence for single-box-v2 and
control-plane-v2. Observe schedd and, on the control-plane profile,
schedd-compute. Retain the fixed outcome order success, retry, failed,
dead_letter. Reuse existing counters; do not alter producer behavior,
scheduling policy, durable state or alert thresholds.

Use the existing exact process-local counter comparison protocol. First usable
points are baselines. Differences require fresh original sample clocks,
unchanged sampled process proof, advancing samples and uptime advance within
the existing one-second tolerance. Resets, changed processes, repeated samples,
source gaps and uncertain windows establish no delta. Valid sibling outcomes
and scheduler processes remain independently useful.

Summary counts classify signals across the full inspected target set. Exact
bounded target deltas count settled attempts, not unique tasks or deliveries.
Retry establishes neither its cause nor a final result. Failed includes
application errors; dead-letter remains a distinct disposition. Success does
not prove unique or exactly-once execution. No deployment gate or failure-rate
threshold is inferred.

Carry independently validated evidence through the API, CLI, SDKs, projected
reports and compact bounded history. Preserve a canonical sticky union of
increased outcomes, baselines and collection gaps without summing overlapping
poll deltas. Integrate with targeted diagnostic selection and its intent
metadata. Intentionally unselected groups do not create missing evidence.
V1 profiles remain core-only.

New source reads yield to loop evidence under the existing deadline. New target
previews yield before earlier advisory details under the existing response and
report caps. Full-set summaries and explicit omissions survive clipping.
Protected read counts and qualification behavior remain unchanged.

## Consequences

Operators can inspect retries, settled failures and dead-letter dispositions
without treating every failed attempt as an infrastructure fault. Sampled
process continuity cannot exclude every intervening restart. Older readers
retain compatibility through the optional section, and unavailable data remains
explicitly uncertain.
