# ADR-1427 · Expired backlog ratio alerts with a minimum cohort

Date: 2026-10-16
Status: Accepted

## Context

Complete deadline count alerts detect absolute urgency but cannot express the
fraction of deadline-bearing pending work that has expired. A small cohort can
make a percentage misleading, and large exact counts can overflow naive scaled
integer arithmetic or round across warning/recovery boundaries.

## Decision

Add optional Go expired-ratio conditions under deadline-window watch options.
Define the ratio as expired divided by all pending deadline-bearing recipients in
the same complete snapshot. Require an explicit positive minimum denominator and
integer basis-point warning/recovery policies. Keep below-minimum/empty cohorts
unknown, including for active incidents. Reuse confirmation_lost as the closing
transition when active proof is lost, without claiming successful recovery. Later
breaches require a new incident and sustained proof. Preserve full-window evidence
validation and existing shared sustained warning/recovery timing.

Use 128-bit multiplication and exact quotient/remainder. Project fractional values
strictly inside their adjacent integer basis-point boundaries so integer policy
comparisons remain exact after float64 and JSON round trips. Bind metric identity
to horizon and minimum cohort; bind thresholds/durations through existing checkpoint
fields. Allow fractional values only for recognized ratio metrics, keeping count
validation unchanged. Append the ratio after existing count conditions.

Reuse version 2 checkpoints, local confirmation diagnostics, incident lifecycle and
durable outbox. Clone nested option pointers. Use the existing aggregate read with
no extra per-ratio request, endpoint or database migration.

## Consequences

Operators can combine backlog-relative degradation with absolute count alerts.
Low-cohort observations cannot clear an incident, even after the backlog drains.
New work or retention can change the ratio without successful business processing.
Older checkpoint readers reject the new identity. CLI and other SDK watch exposure
remain separate follow-ups; existing selections retain behavior.

See [configuration and limitations](../event-deadline-ratio-alerts.md).
