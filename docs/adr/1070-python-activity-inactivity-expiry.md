# ADR 1070: Python activity inactivity expiry

Status: Accepted; runtime validation deferred.

## Context

The Python temporary activity publisher refreshes until explicitly cleared. A missed application stop event can keep typing/editing status active indefinitely.

## Decision

Add optional idle_timeout_seconds to the generic publisher and account HTTP factory. Default None preserves indefinite refresh; finite non-boolean numbers from 0.1 to 86400 enable expiry. Reset a monotonic last-update deadline on every valid accepted update, even with unchanged data. Publication refreshes do not reset it.

Reuse the owned publication task and wake event. Wake at the earlier refresh/throttle or idle deadline. On idle, serialize a null/TTL-zero clear after any existing write, using the existing five-second write budget, and stop refreshing. A later update restarts a healthy publisher. Updates received during an idle clear advance the version and retain the latest data; process them after clear if their own idle deadline has not passed. Manual clear/close remain authoritative and reject conflicting updates as before.

Idle timing is independent from remote TTL, which still removes state when a clear is interrupted or fails. Clear failures use existing sanitized error state/callback, stop publication and require publisher replacement. Do not add automatic ephemeral retries or another timer/task.

## Consequences

Typing can stop without an explicit application stop callback. Slow writes or a blocked event loop delay the clear; no hard remote removal deadline is guaranteed. Existing cancellation/close cleanup applies. No routes, grants, schema or migration change. Static lint, compilation, AST/factory option inspection and diff checks are used; runtime validation remains deferred by user request.
