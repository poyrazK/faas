# ADR 1083: Python catch-up stall detection

Status: Accepted; runtime validation deferred.

## Context

A connected replay subscription can remain below its frozen target while application processing stops advancing. Connection quality alone cannot describe this.

## Decision

Add optional stall_after_seconds (None default; finite non-boolean 0.1–86400) to the catch-up tracker. Measure monotonically from catch-up begin/first target installation or strict processed advancement. While below a known target, classify stalled once elapsed age reaches the threshold. Duplicates, backward updates and transport traffic do not reset timing. Strict progress restores catching-up/caught-up. Unknown targets, completed replay and resync-required states are outside stall detection.

Expose progress_age_seconds and stall_seconds (age beyond threshold) on immutable status. Evaluate on snapshot through existing application ticks and notify only on state transition; add no timer. New generations reset timing; disposal freezes observation. Extend health with catch_up_stalled reason/label, after connection/resync priority and before degraded quality. Validate elapsed inputs.

## Consequences

Stall is a local warning; long valid work can trigger it. Applications retain retry, replay, ACK, fencing and recovery policy. No routes, grants, schema or migration changes. Static lint/compilation, SDK AST/options/health-state checks and diff checks verify implementation; runtime validation remains deferred by user request.
