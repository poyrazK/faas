# ADR 1085: Python subscription readiness waits

Status: Accepted; runtime validation deferred.

## Context

Applications need to gate actions on required subscription acknowledgements without adding polling or conflating transport readiness with channel readiness.

## Decision

Add async wait_ready to the existing tracker for a fixed generation and selected requested channels/inbox. Default selection covers all requirements; empty explicit selections succeed for an active generation. Return an immutable selected ready summary. Denial/resync are typed sanitized RealtimeSubscriptionWaitError results; denial wins when both apply. Fail old-generation waits on begin/reset, and disposed waits on disposal.

Use bounded waiter futures woken before tracker callbacks, then recheck generation/state. Support optional finite 0–86400 timeout via asyncio; cancellation and timeout clean registration without mutating subscriptions. Bound simultaneous wait calls with max_waiters (128 default, 1–8192). Bind all waits/state updates to one loop/execution context. Preserve new error exports in the generator barrel.

## Consequences

Success only proves selected subscription acknowledgement, not replay or fresh data. Unknown/duplicate selections fail validation; unselected failures do not block selected readiness. No polling task, request or retry policy is added. No routes, grants, schema or migration change. Static lint/compilation, SDK AST/export/signature checks and diff checks verify implementation; runtime validation remains deferred by user request.
