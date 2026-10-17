# ADR-1417 · Configurable recovery boundaries for deadline count alerts

Date: 2026-10-16
Status: Accepted

## Context

Deadline-window total and waiting-reason alerts currently recover at warning minus
one. Sustained confirmation reduces short fluctuations but cannot configure a wider
gap for a backlog that stays near its warning boundary.

## Decision

Expose optional inclusive recovery counts independently for upcoming and expired
totals and their selected waiting reasons. Default to warning minus one. Use pointers
for optional totals and map presence for optional reasons so zero remains explicit.
Reject recovery counts outside 0 <= recovery < enabled warning. Clone all mutable
options before polling, retaining deterministic condition ordering and identities.

Reuse the existing sustained recovery engine, including fresh two-sample proof,
independent warning/recovery durations, middle-band incident retention and reset on
lost confirmation. Preserve resumed incidents as unknown until new warning or
recovery proof. Allow bounded integer recovery counts below the warning only for
known deadline-window count metrics in checkpoint validation. Keep other metrics'
recovery policies unchanged. Bind resume to the existing effective policy fields.

## Consequences

Operators can choose separate opening and clearing counts without extra reads or
schema changes. Existing defaults and version 2 checkpoints remain compatible with
updated clients. Older clients reject custom lower recovery counts. No checkpoint
version bump is needed because the serialized policy already carries the boundary.
Default omission and explicit default values have identical effective policy.
Recovery remains evidence about rolling aggregates, not business completion.

See [configuration](../event-subscription-deadline-window.md#separate-opening-and-recovery-counts).
