# ADR-20261114060000 · Retained notification fault evidence

- **Status:** accepted for implementation; production convergence separate.
- **Date:** 2026-11-14
- **Extends:** [deployment timelines](20261113180002-post-deployment-monitoring-timeline.md),
  [serving fault evidence](20261113200001-deployment-probe-failure-evidence.md),
  [advisory history](20261114030000-deployment-notification-history.md) and
  [stability diagnostics](20261114050000-deployment-stability-diagnostics.md).

Preserve confirmed causes on degraded aggregate-notification and receiver-check
transitions as optional `notification_failures` in schema-version-1 reports.
Current source snapshots are replaced by later polls; a recovery or unavailable
read must not erase which receiver, integration or pipeline signal failed.
Collect from already fetched, validated projections and the existing check
assessments. Keep authorization, qualification, fault flags, timing, exit codes,
notification behavior and API reads unchanged.

Retain current, fresh post-boundary observations that satisfy the same fault
conditions as the checks: Alertmanager up/reload values, dropped/failing counters,
zero queue capacity, busy queue length/capacity, integration failures, receiver
request failures, and receiver coverage faults supported by fresh collection
and intent. Capture fixed reviewed identities/reasons and named source values,
quality, original clocks, window and freshness policy. Coverage faults retain
collector/inventory evidence, not fabricated zero counters. Unknown intent
remains unknown. Exclude raw destinations, arbitrary labels/provider errors and
successful-delivery values. Rolling-window counters can include pre-boundary
events and do not prove per-episode delivery, release attribution or human receipt.

Fingerprint all eligible cause identities, categories and expectation/threshold
policy in canonical order. Exclude changing source values and clocks. Add a
transition when the cause set changes despite an unchanged degraded check
reason, including changes outside the retained preview. Values retain the first
observation of a semantic cause; subsequent values remain in the latest source
snapshot. Unknown or passing checks retain prior historical entries without
attaching them as current failures.

Use an independent shared budget for both notification checks: four details per
transition, 16 retained items and 8 KiB of added pretty-JSON transition properties,
including metadata. Keep limits in `pkg/api/limits.go`. Expose totals, omissions,
truncation, retained counts/bytes, dropped items and dropped snapshots. Omit a
whole snapshot if metadata cannot fit. Both fault scopes and serving faults
share the existing 256-transition cap; rollover frees detail budgets while
preserving check summaries. Retain pointer-owned evidence so later reads and
rollover cannot rewrite earlier captures.

Extend report fitting for the maximum fleet, all integration types, expected
receiver groups and all populated histories. Exhaust advisory events first,
omitting that optional section with its existing limitation when needed. If
primary histories still overflow the 1 MiB envelope, omit their oldest detailed
fault snapshots from a copy of the report. Preserve every check transition and
summary, current source payload, stability diagnostic and qualification field.
Disclose `envelope_omitted_items` / `envelope_omitted_snapshots` per fault scope,
adjust report retained counts/bytes and mark truncation, without changing
permanent drops or recorder contents. Later smaller reports may expose retained
detail again. This refines earlier envelope priority: primary historical detail
yields only after advisory history, while current evidence and check summaries
remain intact.

Render human and JSON evidence, and preserve it on deadline, cancellation and
report-write errors. Verify all fault classes, source freshness and redaction,
recovery through unknown reads, semantic changes versus numeric/clock refreshes,
omitted causes, owned snapshots, independent count/byte budgets, rollover,
private CLI reports, fitting priorities and the fully populated degraded fleet.
Production convergence remains separate.
