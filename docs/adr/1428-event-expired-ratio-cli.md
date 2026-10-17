# ADR-1428 · Expired backlog ratio alerts in the CLI

Date: 2026-10-16
Status: Accepted

## Context

Go watches support expired-backlog ratios with a minimum cohort and separate
warning/recovery basis points. CLI operators can select only count conditions.

## Decision

Expose decimal percentage warning/recovery flags and an explicit minimum recipient
flag on the existing delivery-health watch. Convert at most two fractional digits
directly into basis points without float parsing. Reject duplicate ratio settings,
out-of-range values, missing minimums and orphaned recovery/minimum flags before
authentication or checkpoint preparation. Permit ratio-only and combined watches.

Reuse existing shared deadline horizon/sustain options and Go policy/incident logic.
Show percentages and raw cohort/timestamp in final and opt-in live text output;
mark approximate display values, undefined zero denominators and unavailable or
insufficient evidence. Keep JSON units in basis points and existing schemas intact.

Reuse existing checkpoint persistence before notification, resume matching,
acknowledgments and confirmation diagnostics. Add no endpoint, migration, transport
or delivery action. Preserve count output and behavior when ratio alerts are absent.

## Consequences

Operators can monitor backlog-relative expiry without custom Go code. Equivalent
decimal spellings map to the same resume policy. Low cohorts retain the existing
confirmation_lost lifecycle and never fabricate successful recovery. Display rounding
cannot affect the exact basis-point decisions in the watch.

See [CLI usage and limits](../event-deadline-ratio-alerts.md#cli-ratio-watch).
