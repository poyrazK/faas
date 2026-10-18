# ADR-1420 · Node expired backlog ratio conditions and version 2 checkpoints

Date: 2026-10-16
Status: Accepted

## Context

Go and CLI watches support expired backlog ratios with minimum cohorts. Node's
watch currently exposes only total and reason counts, and its version 1 checkpoint
contract requires integer count values.

## Decision

Add an optional expired-ratio rule to the Node deadline watch. Normalize integer
basis-point warning/recovery thresholds and a required positive safe-integer minimum
cohort. Reuse the existing complete aggregate read and shared sustained confirmation
policy. Treat insufficient/empty cohorts as unknown independently of count rules.
Preserve Node's existing incident retention and fresh proof requirements on evidence
loss rather than changing lifecycle transitions for existing callers.

Use transient BigInt cross-products for all ratio warning/recovery comparisons.
Retain source numerator/denominator as JSON-safe integer samples in ratio conditions
and alerts; approximate basis-point values are display projections only. Validate
retained sample/value consistency and exact alert boundaries during checkpoint load
and acknowledgment. Never restore sample/timer proof as fresh evidence.

Keep version 1 strictly count-only and serialize unchanged selections in that format.
Use version 2 only for selections containing the one supported ratio rule, optionally
with existing count conditions. Bind minimum cohort and threshold policy canonically,
reject unsupported/downgraded/mismatched state, and retain existing bounded outbox,
closing reservations and acknowledgment semantics. Do not make checkpoints portable
to Go/CLI or Python.

## Consequences

Node can monitor relative backlog degradation without an extra request or schema
change. Count-only checkpoints and identities remain compatible. Ratio checkpoints
require upgraded readers, and adding/removing a ratio requires a separate watch
selection. Low cohorts cannot resolve incidents. Display rounding cannot change
live or historical alert validation, and aggregate recovery still does not establish
business completion.

See [configuration and compatibility](../event-deadline-node-watch.md#expired-backlog-ratios).
