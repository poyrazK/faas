# ADR-20261114080000 · Diagnostics for unqualified deployment evidence

- **Status:** accepted for implementation; production convergence separate.
- **Date:** 2026-11-14
- **Extends:** [source read diagnostics](20261114070000-deployment-source-read-diagnostics.md),
  [stability diagnostics](20261114050000-deployment-stability-diagnostics.md)
  and [bounded report fitting](20261114060000-deployment-notification-fault-evidence.md).

Explain readable source observations that leave an existing deployment health
check unknown before it can start or continue a passing window. Add optional
`qualification_evidence` to schema-version-1 reports, with the last coordinator
observation time and fixed entries for serving probes, notification pipeline
and notification receivers. Keep the existing assessments, qualification,
stability, fault flags, wait budgets, exit codes and API reads unchanged.

Distinguish unobserved, passed, faulted, blocked and unavailable diagnostics.
Unknown checks with readable sources expose bounded current blockers; failed
reads or invalid basic responses remain unavailable without a fabricated empty
preview. Confirmed faults use the existing retained fault evidence. A peer's
confirmed fault must not hide blockers of a different unknown check.

Follow the existing snapshot/inventory prerequisites before examining downstream
observations. Then inspect every expected probe and receiver group and the
required API, pipeline and integration signals. Explain missing clocks, missing
signals/samples, pre-boundary and stale observations, future clocks, unavailable
collection, unknown probe state, missing expectations and invalid metadata.
Use the same boundary/skew comparisons and actual-run age policy, including the
existing millisecond comparison for probe runs. Retain original nullable clocks,
age at observation, freshness/skew policy and reviewed source state/reason.
Receiver success evidence uses observation freshness, not a new delivery.

Project only fixed kinds/names, validated nodes and hashed URL identities, and
approved receiver/integration names. Do not copy metric values, successful-
delivery clocks, addresses, arbitrary labels/reasons or provider errors. An
otherwise valid stale raw pipeline observation may explain its clock even when
the current projected signal is unavailable. Validate its structural fields
independently before exposing that timestamp; malformed metadata gets a generic
diagnostic without copying unreviewed clocks or identifiers.

For each check, retain at most four items and 4 KiB of conservatively indented
item-array JSON. Declare independent limits in `pkg/api/limits.go`. Expose totals,
omissions, truncation, count/byte limits and retained bytes. Sort deterministically
by kind and identity. Count blockers at the inspected prerequisite stage; do not
claim downstream evidence was assessed when its parent snapshot is unusable.

Retain the latest readable blocked observation across recovery, confirmed faults
and source loss as `last_blocked`, labelled historical in human output. Keep
current and historical previews mutually exclusive, with at most one preview
per check and no new unbounded history. Own snapshots so subsequent reads and
terminal handling cannot rewrite source clocks or ages. Preserve diagnostics on
deadline, cancellation and report-write failure without a synthetic observation.

Keep qualification diagnostics alongside current sources and check summaries
when fitting the complete 1 MiB report; advisory and then older detailed fault
history yield space with the existing explicit omission counters. Verify exact
blockers, freshness/boundary/skew equality, actual probe runs, quiet receivers,
structural redaction, full-fleet omissions, byte bounds, recovery through missing
reads, independent saved snapshots, fault precedence, stability interaction,
terminal paths, private CLI reports and the maximum combined report envelope.
Production convergence remains separate.
