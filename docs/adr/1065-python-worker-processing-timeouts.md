# ADR 1065: Python worker processing timeouts

Status: Accepted; runtime validation deferred.

## Context

A stalled Python handler can occupy a slot and receive repeated lease renewals indefinitely. Applications need an optional processing budget without settling work while its effects are still running.

## Decision

Add `processing_timeout_seconds`, disabled by None, accepting finite non-boolean numbers from 0.1 to 86400. Measure monotonically from handler invocation, excluding terminal settlement waiting/transport. A per-claim owned timer sets the cooperative context stop event, cancels/joins renewal and emits one secret-free handler_timeout lifecycle event. Keep the lease/deadline watcher running and await the handler rather than force-cancelling it. Join all monitors during existing cleanup. A handler returning after the budget before the timer can run is also classified as timed out.

After safe exit and while the observed lease/deadline remains valid, settle retry with reserved handler_timeout code, bypassing the custom failure classifier. Existing explicit/group retry policies, batching and receipt retries apply. After confirmed retry/dead settlement, pass TimeoutError to the existing handler-error callback. Timeout takes precedence over handler success or an ordinary exception after the budget. Cooperative self-cancellation after timeout is treated as safe exit; unexpected handler cancellation retains the existing fatal behavior.

Lease/deadline loss or internal failure takes precedence over timeout settlement. Caller shutdown retains existing graceful-release rules after safe exit. An uncertain terminal attempt never leads to release. Cancelling renewal cannot retract an extension already accepted by the server.

## Consequences

Default behavior is unchanged. A noncooperative handler can still hold a slot and delay shutdown, but receives no new renewals after signalling. Event-loop blocking delays timers; downstream fencing/idempotency remain necessary when expired leases are reassigned. No routes, grants, schema or migration change. Verify syntax/lint, generated-export preservation and static diff checks; runtime validation remains deferred by user request.
