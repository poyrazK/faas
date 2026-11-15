# ADR-20261114050000 · Source diagnostics for deployment stability

- **Status:** accepted for implementation; production convergence separate.
- **Date:** 2026-11-14
- **Extends:** [optional stability windows](20261114040000-deployment-monitoring-stability.md).

Add optional `stability.diagnostics` to schema-version-1 deployment reports when
the existing stability option is enabled. Explain sources holding back the
current window and retain evidence of the latest reset. Use only already
fetched, validated health projections; keep the three health checks, stability
calculation, wait budgets, exit codes and advisory-notification role unchanged.

Amend the earlier decision to keep every source identity private: expose only
a bounded, explicit projection of identities already approved for operator
reports. Include fixed source kind, fixed pipeline name, validated node and
hashed URL identity, or approved receiver/integration names. Do not copy raw
URLs, provider errors, payloads, arbitrary labels or delivery-success values.

Disclose `not_observed`, `available` or `unavailable` source status and the
remaining local observation time. For available sources, enumerate clocks whose
advance from the current baseline is less than the requested duration. Include
original nullable baseline/previous/latest clocks, advance and remaining source
seconds. Sort by least advancement and then canonical identity. All bounded
sources contribute to totals, including pairs beyond diagnostic previews.
Unavailable evidence omits the current source preview rather than asserting
zero blockers. Include current non-passing health-check reasons.

Retain at most four items and 4 KiB of conservatively indented item-array JSON
in each of the current-blocker and latest-reset previews. Put both limits in
`pkg/api/limits.go`. Expose total, omitted, truncation, count/byte limits and
retained bytes. Match identities without policy when describing changed probe
budgets or ages; count one policy change per affected clock rather than an
unrelated add/remove pair. Source changes and regressions use deterministic
identity ordering and preserve original before/after clocks and policy values.

Keep only the latest actual reset's timestamp, reason, preceding observation
time and applicable check reasons. For source-set or source-clock resets,
label the preview `source_context=reset_cause`. For health/source loss, local
clock regression or long observation gaps, retain the previous window's source
blockers with `source_context=previous_window_blockers`. This historical evidence
is not re-stamped, and does not attribute unavailable health to an old blocker.
Disclose the observed polling gap when applicable. Subsequent recovery and
repeated unknown polls preserve the last actual reset without manufacturing
additional history. Local-only waiting is distinguishable from slow sources.

Own diagnostic snapshots so later polls, resets and terminal handling cannot
rewrite saved reports. Preserve the last observation on timeout, cancellation
and report-write errors. Keep the entire report within the existing 1 MiB
envelope, trimming advisory notification history before primary health and
stability diagnostics. Reports without the stability option retain their shape;
older readers may ignore the new optional diagnostic object.

Verify every required source, late/unchanged actual probe runs, local-only waits,
full-fleet sorting and omissions, identity/policy changes, clock regressions,
health loss and historical context, polling gaps, repeated unknowns, recovery,
snapshot independence, count/byte caps, redaction, private CLI reports, terminal
paths and both populated previews at the maximum report envelope. Production
convergence remains separate.
