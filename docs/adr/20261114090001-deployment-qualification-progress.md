# ADR-20261114090001 · Deployment qualification changes and wait timing

- **Status:** accepted for implementation; production convergence separate.
- **Date:** 2026-11-14
- **Extends:** [qualification evidence](20261114080000-deployment-qualification-evidence.md)
  and [bounded report fitting](20261114060000-deployment-notification-fault-evidence.md).

A check can remain unknown while its evidence blockers change. The current and
latest-readable previews explain the last assessment, but discard earlier
causes and cannot describe the time spent awaiting usable evidence. Extend the
optional schema-version-1 `qualification_evidence` with per-check `progress`
and a bounded shared `history`. Preserve the existing three checks, stability
rules, fault flags, read budgets, exit codes and API calls.

Keep nullable first-observed, first-blocked and first-passed assessment times,
current diagnostic-state start and current blocker-set start. Count sampled
blocked and unavailable seconds separately, attributing each inter-assessment
interval to its preceding diagnostic state. Use raw coordinator monotonic
clocks when available; clamp regressions without moving the elapsed-time anchor
backward. Do not count startup time or advance durations during terminal saves.
Confirmed fault time remains in the existing health timeline. First pass refers
to the raw check, before any stability wait; later faults cannot erase it.
Per-check durations can overlap and do not establish continuous availability.

Record first observations and changes in diagnostic state, reviewed reason,
source HTTP status or blocked cause. Fingerprint the complete sorted inspected
blocker set before preview truncation, retaining only approved identities,
reasons, source state and freshness policy. Exclude timestamps and ages so
routine fresh observations do not fill history. Capture a bounded blocker
preview with its original nullable clocks and ages on each blocked event.
Preserve the existing current/last-readable previews separately.

Retain the latest 16 events within 16 KiB of conservatively indented event-array
JSON. Declare independent limits in `pkg/api/limits.go`. Expose total, retained
and dropped event counts, retained bytes, count/byte limits and truncation.
Progress summaries survive event eviction. Own saved summaries, clocks and
previews so later polls or consumers cannot rewrite the recorder's history.

When fitting the complete 1 MiB report, exhaust advisory direct-notification
history first, then omit the oldest qualification events before older primary
detailed fault snapshots. Preserve current sources, current/last-readable
blockers, progress, health transitions and stability diagnostics. Report
qualification `envelope_omitted_events` separately from permanent recorder
drops, including when every event is omitted. Fit copies rather than evicting
recorder data. This extends the preceding ADR's last-preview-only retention
with a bounded event list; it introduces no unbounded history.

Render matching human and JSON diagnostics. Verify cause changes within unknown
checks, clock-update deduplication, full-fleet changes beyond the preview,
policy changes, unavailable gaps, recovery, confirmed faults, coordinator clock
regressions, retained first-pass timing, independent saved views, count/byte
rollover, terminal paths, protected CLI reports, workflow transport and maximum
combined report fitting. Production convergence remains separate.
