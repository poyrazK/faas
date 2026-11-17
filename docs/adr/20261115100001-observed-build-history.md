# ADR-20261115100001 · Observed build history during verification

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-15
- **Extends:** [advisory build evidence](20261115080000-advisory-runtime-build-evidence.md)
  and [collection transport](20261115090001-monitoring-build-evidence-transport.md).

Each verification poll replaces the current build snapshot. A final matching
observation can therefore hide earlier mixed revisions or missing sources.
Add optional `runtime_builds.progress` and `runtime_builds.history` when an
expected role is selected. Preserve the existing health gates, stability
qualification, confirmed-fault flag, API calls, polling schedule and exit codes.
Unselected roles keep the existing current unknown diagnostic without history.

Track the actual observed polls, first/last observation, first/last mismatch
(mixed or mismatched state), first/last matching observation, last observed
recovery and regression, and regression count. Keep sticky incomplete-evidence
and unavailable-source flags. `matching_throughout_observed_polls` is true only
when at least one poll was observed and every observed assessment matched.
An unknown or incomplete poll makes it false. Unknown gaps never clear pending
mismatches or turn source recovery into build recovery: a later matching
assessment confirms observed build recovery. A mismatch after a prior matching
assessment records a regression, including when a source gap intervenes.

These are sampled assessments of selected critical jobs, not full-fleet release
convergence or continuous availability. First matching is independent of the
health stability window. Do not add elapsed-time estimates or extra polls.
Use coordinator assessment timestamps and clamp regressions to the last
recorded assessment time; keep original source generation clocks separately.
Initial and terminal saves do not invent observations or advance history.

The history scope is `observed_build_state_and_coverage_counts`. Record initial
observations and semantic changes in aggregate state/reason, source HTTP status,
full-set coverage/revision counts and the fixed job inventory/source records.
Exclude source clock churn, preview order, omission counts, node/target/version
labels and individual revision changes that leave these aggregates unchanged.
Do not claim per-target transition coverage. Events retain the original source
generation clock when available, plus assessment time and observation ordinal.
Clone summaries, event clocks, counts and job lists so later polls, fitting or
consumers cannot rewrite retained views.

Retain the latest 16 events within 16 KiB of conservatively indented event-array
JSON. Declare independent bounds in `pkg/api/limits.go`; expose scope, item/byte
limits, total/retained/dropped events, retained bytes and truncation. Preserve
progress through count/byte eviction. Full-report fitting first removes current
advisory build preview items, then the oldest build events, before existing
advisory notification history, qualification events and primary fault detail.
Preserve progress and retention metadata, including when all events are omitted.
Count envelope omissions separately from permanent recorder eviction. Fit copies.

Extend the SSH adapter's closed projection with typed, bounded validation of
progress, temporal order, observation ordinals, source metadata, aggregate count
relationships and retention accounting. Bind observations to the run's attempts
and start time. Validate the latest retained event against the current aggregate
assessment while allowing unchanged later polls. Keep historical source clocks
as historical evidence rather than reclassifying them against collection time.
Drop unknown properties and recompute stored byte accounting after projection.

Malformed history drops the history/progress pair and adds fixed
`history_unavailable_reason=invalid_history`, retaining valid current build and
health evidence. Older reports without the pair remain compatible. Adapter
fitting also removes build events after current preview items. Preserve progress
and retention metadata through ordinary fitting; if optional metadata must be
removed to keep mandatory evidence within 1 MiB, disclose unavailable history
with `report_envelope`. Existing whole-report integrity failure still stays
unknown verification when mandatory data cannot fit.

Human and job summaries distinguish the final build state from earlier observed
mismatches, matching observations, gaps and regressions. Include first/last times
and retention accounting without interpolating raw node or version labels into
Markdown. Test recovery, gaps, regressions, timestamp-only deduplication, full-set
count/job changes, source-status changes, clock regressions, ownership, count and
byte limits, terminal paths, combined report fitting, transport validation,
redaction and older-report compatibility. Operational activation remains separate.
