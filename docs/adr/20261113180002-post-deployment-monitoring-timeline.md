# ADR-20261113180002 · Post-deployment monitoring timeline

- **Status:** accepted for implementation; production activation separate.
- **Date:** 2026-11-13
- **Extends:** [post-deployment evidence](20261113130000-post-deployment-monitoring-evidence.md).

The deployment verifier currently replaces each poll's assessment and preserves
only `had_confirmed_faults` when a fault recovers or its evidence disappears.
That flag cannot identify the earlier failing check, reason or timing.

Add an optional `timeline` to schema-version-1 reports. Record the first assessed
state and subsequent state/reason changes per check, with poll attempt and UTC
assessment time. Retain the latest 256 transitions across the three fixed
checks, declaring the limit, truncation and number of dropped transitions.
Repeated identical polls do not consume the transition budget. Keep summaries
independently of truncation: first fault time/reason, most recent recovery,
last observation, estimated fault seconds and an unresolved-fault flag.

A confirmed degraded assessment opens a fault. Only a subsequent passing
assessment establishes observed recovery; unknown evidence does not. Estimate
fault seconds by assigning each interval between assessments to its previous
state. Unknown intervals do not accumulate fault seconds. Use monotonic elapsed
time when available, clamp negative intervals to zero, and never extrapolate
past the last assessment. These estimates do not measure continuous downtime
or identify an outage's actual onset. Assessment timestamps remain wall-clock
UTC values and depend on the local clock.

Each saved report receives an independent bounded snapshot. Preserve history on
deadline expiry, handled cancellation and report-write failure. Cancellation is
an overall verification result and does not itself add a check observation.
Retain atomic owner-only file writes and surface I/O errors; a failed write
cannot promise an updated on-disk artifact. Store only reviewed check metadata,
not prior serving/notification payloads, credentials or provider diagnostics.

Report the fault summaries in human output and the full bounded timeline in
JSON. Accept older reports without this optional field. Keep the latest
assessment as the existing convergence gate and preserve all exit codes,
polling budgets, authorization, notification and rollback behavior.

See the [operator guide](../ops/post-deployment-monitoring.md).
