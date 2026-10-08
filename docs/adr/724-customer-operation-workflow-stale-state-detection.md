# ADR-724: Detect stale customer workflow states

## Status

Accepted

## Context

Applications report current business workflow states, and terminal-state
declarations distinguish finished runs from active ones. Operators still need
an application-defined way to find active runs that have not progressed within
the expected time for their current state.

## Decision

Workflow declarations may define `state_stale_after`, a map from active state
names to positive Go duration strings. Thresholds are pinned in seconds, must
be whole-second values from one second through ten years, and cannot target
terminal or undeclared states.

Current workflow-state reads compare the pinned threshold to the app-reported
`occurred_at` time. Responses include `stale`, the threshold when configured,
and `occurred_at`. Business-reference reads may set `stale_only=true` to filter
the current `workflow_states` list. Milestone facts and state history retain
their existing pagination and contents. The dashboard and CLI expose the
filter.

Gregale only reports the stale classification. It does not transition state,
retry work, cancel Operations, or start recovery. The application remains
responsible for business decisions and state changes.

## Consequences

- Operators can find active business runs that exceed an app-declared age
  threshold without hard-coding state-specific timing in Gregale.
- The age uses the app's occurrence timestamp, so recovery publication delay
  does not distort the classification.
- Threshold changes affect newly deployed immutable definitions; existing
  state reports continue to use the definition that reported them.
