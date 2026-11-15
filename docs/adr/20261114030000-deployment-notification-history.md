# ADR-20261114030000 · Advisory notification history in deployment reports

- **Status:** accepted for implementation; production convergence separate.
- **Date:** 2026-11-14
- **Extends:** [deployment timelines](20261113180002-post-deployment-monitoring-timeline.md),
  [retained serving faults](20261113200001-deployment-probe-failure-evidence.md)
  and [notification evidence reasons](20261114020000-serving-probe-notification-telemetry.md).

Add optional `timeline.probe_notifications` to schema-version-1 deployment
reports. Preserve direct node/channel observations across polls so a later
successful or unavailable snapshot does not erase an observed submission failure
or telemetry gap. Keep the history advisory: retain the original three health
checks, qualification, `had_confirmed_faults`, exit codes and wait behavior.
Use only already fetched evidence, with no extra API reads, probes, sends or
retained server-history writes. Render retained observations in human and JSON
output; older report readers can ignore the optional section.

Validate optional notification metadata before retention. Require current
snapshot, inventory and channel evaluations at or after the evidence boundary;
validate source freshness again at observation time. Preserve original clocks
and nullable unknowns through an explicit projection of reviewed fields. Label
each attempt as before versus at/after the boundary, without attributing an old
failure or a current observation to the requested release.

Record semantic state/reason changes, boundary crossings and genuinely newer
failed submissions. Deduplicate evaluation/run-clock refreshes and unchanged
successes. Track an unresolved observed failure until a later observed success
has a completion clock strictly newer than the latest observed failure. Unknown
evidence, clean credential rotation without history, disablement, preview
omission and retirement do not establish successful delivery. Describe the
closing event as a later successful submission, not same-provider recovery or
receipt; destination identity is deliberately unavailable.

The fleet preview remains at most 32 channels. Distinguish `not_in_preview`
from `not_in_current_expectations` when a complete preview excludes a tracked
channel. Missing, invalid, pre-boundary or unavailable evidence records an
explicit unobserved reason without copying source error text. Preserve a
monotonic observed-failure flag and disclose unresolved tracked failures rather
than treating omitted channels as healthy.

Retain the latest 96 events with an independent 48 KiB serialized-event
budget, including conservative indentation/separator charges. Keep these
limits in `pkg/api/limits.go`; do not evict existing health-check transitions or
serving fault details. Track at most the existing 2,000 node/channel bound,
disclosing dropped identity observations. Retain drop counters, maximum preview
omissions, incomplete-read counts and current source status. Event eviction
does not erase tracked failure state. Fixed summaries plus all retained data
must fit the existing 1 MiB report envelope at maximum fleet size.

Prioritize primary health evidence within that envelope, including for a fully
degraded latest fleet. Trim oldest advisory entries from the report view when
needed and disclose `envelope_omitted_events` separately from permanent drops.
Preserve internally retained entries for later smaller reports. If fixed
advisory metadata cannot fit, omit the optional section with an explicit
report-envelope limitation. Fit copies so neither prior reports nor the
recorder change, and never change qualification to accommodate advisory data.

Own every retained clock and snapshot so later reads and rollover cannot mutate
earlier reports. Preserve collected evidence on deadline, cancellation and
report-write failure returns. Test failure through telemetry loss, rotation,
disablement and later success; old/equal/new success clocks; genuine retries;
pre-boundary and in-transit stale evidence; omitted/retired channels; source
failures; redaction; record, byte and tracking caps; immutable snapshots; the
complete CLI/report path and maximum report envelope. Production convergence
remains separate.
