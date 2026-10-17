# ADR-1423 · Python expired backlog ratios and version 2 checkpoints

Date: 2026-10-16
Status: Accepted

## Context

Go/CLI and Node deadline watches expose expired backlog ratios. Python's watch
currently supports only count conditions in its version 1 checkpoint contract.

## Decision

Add an optional keyword-only expired-ratio policy to Python deadline watches,
preserving existing positional arguments. Normalize integer basis-point warning,
recovery and required minimum cohort settings; reject booleans, floats and unsafe
counts. Reuse full scoped window evidence and shared sustained confirmation timing.
Low/empty cohorts reset only ratio proof and preserve Python's unknown-incident
lifecycle, without successful recovery notifications.

Compare ratios with exact Python integer products in live proof, diagnostics and
checkpoint alert validation. Store JSON-safe source counts on ratio conditions and
alerts; float values are approximate display projections only. Validate samples,
minimum cohort and projection consistency, and never restore sample/timer proof.

Keep count-only selections in version 1 and require version 2 for ratio selections.
Bind canonical metric identity, cohort minimum, thresholds and durations on resume.
Reject downgraded, unsupported or inconsistent state while retaining existing
outbox/closing reservations, atomic acknowledgments and callback/cancellation result
snapshots. Keep Python checkpoint kinds distinct from Node and Go/CLI.

## Consequences

Python closes the SDK ratio-monitoring gap without new reads, endpoints or schema.
Existing count-only checkpoint shapes and policy ordering remain compatible. Ratio
checkpoints require upgraded readers; adding/removing a ratio requires a separate
selection. Retained source counts keep boundary validation independent of display
rounding. Unknown or recovered aggregate counts do not establish business completion.

See [configuration and compatibility](../event-deadline-python-watch.md#expired-backlog-ratio-conditions).
