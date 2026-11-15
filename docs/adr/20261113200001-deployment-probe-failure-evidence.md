# ADR-20261113200001 · Deployment probe failure evidence

- **Status:** accepted for implementation; production rollout separate.
- **Date:** 2026-11-13
- **Extends:** [deployment timeline](20261113180002-post-deployment-monitoring-timeline.md)
  and [probe diagnostics](20261113190000-serving-probe-failure-diagnostics.md).

The timeline retains general check reasons, while each poll replaces its latest
source payload. Recovery therefore loses the earlier failing node/URL and
detailed cause. Two distinct causes can also look like the same degraded check.

Retain a bounded `probe_failures` preview on degraded serving-check transitions.
Project only validated, fresh post-boundary fault evaluations: stable node and
hashed URL identity, coverage state/reason, source evaluation/run clocks and
structured diagnostics. Copy pointer values so a later source read cannot
rewrite history. Missing diagnostics stay unknown. Recovery and unknown reads
retain earlier entries without presenting them as current evidence.

Fingerprint all eligible causes, including omitted pairs, excluding changing
timestamps. A semantic cause change adds a transition even when general check
state/reason stays constant. Expose the fingerprint for comparing incomplete
previews, with no claim of external receipt or complete retained evidence.

Retain at most four details per transition and share 64 items and 64 KiB of
added serialized transition properties across the timeline. Measure bytes in the
existing indented report format, including snapshot counts and fingerprints.
Declare omitted counts and retained limits, bytes, dropped items/snapshots and
truncation. Omit the entire snapshot if its metadata cannot fit. Evict its
details with old timeline entries, return their space to the budget, and count
the loss. Keep the existing 256-transition limit and first-fault summaries.

Use optional additive fields in version-1 reports and show retained evidence in
human output. Keep the report within its existing transport envelope, verified
with the maximum serving fleet, receiver inventory and populated history.
Preserve authorization, success criteria, cancellation/timeout semantics,
owner-only atomic writes, exit codes and notification/rollback behavior.

See the [operator guide](../ops/post-deployment-monitoring.md#retained-probe-failure-details).
