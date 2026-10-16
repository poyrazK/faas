# ADR 1086: Python catch-up completion waits

Status: Accepted; runtime validation deferred.

## Context

Applications can await subscription readiness but still need to gate actions on successfully processed replay reaching its frozen target.

## Decision

Add wait_caught_up to the catch-up tracker and export RealtimeCatchUpWaitError. Bind a wait to one local generation; return immutable caught-up status only when a known target is processed. Unknown target waits. Typed failure reasons cover stalled, resync-required, obsolete and disposed. Default fail_on_stall=True fails on configured processing stalls; false keeps waiting without changing warning/processing policy.

Wake bounded waiter futures on tracker changes before callbacks and reread state/generation after callback reentrancy. A fail-on-stall wait owns a deadline wakeup for the current no-progress interval, avoiding external polling. Overall optional timeout accepts finite 0–86400 seconds. Cancellation/timeouts remove registration without changing progress or other waits. max_waiters defaults to 128 (1–8192); use one loop/execution context. Begin/disposal wake obsolete/terminal waits.

## Consequences

Subscription acknowledgement and processed replay completion remain separate gates. Success refers to the frozen target, not future live messages. No background task, replay, ACK, retry or recovery policy is introduced. No routes, grants, schema or migration changes. Static lint/compilation, SDK AST/export/signature checks and diff checks verify implementation; runtime validation remains deferred by user request.
