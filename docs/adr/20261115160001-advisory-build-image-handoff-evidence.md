# ADR-20261115160001 · Advisory build-to-image handoff evidence

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-15
- **Extends:** [builder diagnostics](20261115150000-advisory-builder-progress-and-pressure.md)
  and [observed runtime history](20261115100001-observed-build-history.md).

Successful builds can await image preparation while daemon identity, readiness
and runtime heartbeats remain usable. Add optional `handoff_health` for imaged
members of the existing explicit v2 profiles, reusing eight existing handoff
gauges. V1 and unset profiles retain their scope. Image-preparation phases,
checkpoint counters, query/error rates and downstream snapshot/activation work
remain separate increments. No producer, database read, scrape, probe, alert,
execution deadline, workflow activation or remediation is added.

Share the builder reader's strict batched gauge machinery. Read one fixed
job/name selector and its original timestamps, preserving metric names before
timestamp removes them. Validate closed families, full series-frame pairing,
duplicates, exact up-target labels and the existing 4096-sample per-family
bound. One imaged reader runs alongside the other fixed readers under the same
pinned evaluation time and shared three-second runtime budget. Skip it without
up targets, retaining expected missing targets and unknown configured inventory.

Retain independent collection-status, backlog and recovery components. Each
requires a fresh successful up and fresh original sample clocks, with existing
120-second age and 30-second boundary/future skew. Collection metadata preserves
the success bit and last successful read's start time, including a fresh zero
bit. Zero can mean no successful observation yet; it is not necessarily a failed
attempt. Failed, expired, absent or pre-boundary collection cannot make backlog
values usable, even if cached zero gauges remain. The four snapshot gauges must
share one original scrape clock, as must the four recovery gauges. Preserve valid
pending evidence if recovery metadata is incomplete. Data-origin clocks obey
the existing ten-year sanity bound and cannot postdate their original scrape by
more than two existing skew allowances across producer/collector clocks.

Backlog means eligible succeeded build rows without an image-preparation
checkpoint in that imaged recovery scope. Pending is a finite exact nonnegative
integer; its JavaScript-safe representation ceiling is centralized as
`ObsRuntimeHandoffPendingMax`, not an admission quota. Zero requires an absent
oldest finish time and zero age. Positive counts require a valid finish clock;
it may precede the release boundary. Age is clamped at zero for the existing
future skew. Legacy empty-owner routing and empty-reader compatibility can
overlap replica scopes: never sum pending values into a fleet or unique-build
total. Aggregates count waiting/empty observation scopes instead.

Recovery state is waiting, working, stopping or not_started, with enabled,
inflight, attempt and completion clocks. A pass can legitimately perform long
image work. Do not invent a stall threshold from its two-second ticker. Context
cancellation can disable expectation while a pass remains in flight. Completion
means the pass returned, including failure/no-op; it does not establish a
committed checkpoint, prepared image, drained work or ready deployment.

Aggregate the full inspected set before the existing 32-item preview. Waiting
scopes take priority, oldest wait first, then incomplete and empty observations.
Incompleteness remains explicit alongside positive waiting evidence. Handoff
detail yields first under the existing 64 KiB API and 1 MiB report envelopes,
with explicit omissions and retained aggregate scope counts. The CLI, SDKs and
closed Python adapter keep this section optional and isolate malformed JSON,
including absent/null required zero values, from all existing usable evidence.
Private artifacts and summaries exclude raw labels, endpoints and provider text.

State/count/coverage transitions share the existing 16-event/16 KiB history.
Routine ages, metric clocks, pending magnitudes and per-target values do not add
events. Separate progress keeps first/last waiting and complete empty
observations, observed transitions from empty to waiting, the throughout flag
and sticky incomplete evidence. Gaps cannot become empty or erase prior
waiting evidence. These are sampled aggregate observations across an evolving
scope, not successful-delivery/recovery counters or proof about the same builds
or processes. No clear/recovery claim is inferred when work disappears into a
checkpoint, leaves eligibility, is canceled or the scope changes.

Verify all profiles, known empty versus unknown, unsuccessful/expired collection,
long passes and stopping in-flight work, partial positives, source/clock/frame
failures, large exact counts and overlapping scopes, missing/unavailable
inventory, faults beyond previews, byte fitting, history ownership/clipping,
optional JSON isolation, unchanged health qualification and SDK round trips.
