# ADR 1062: Python receipt-backed settlement retries

Status: Accepted; runtime validation deferred.

## Context

A Python worker can successfully settle a lease but lose the response. ADR 1055 provides durable settlement receipts; the Node helper already uses these for bounded opt-in retries.

## Decision

Add `RealtimeWorkerOptions.settlement_retries`, a strict integer from zero to three, default zero. For both account and scoped workers, enabled complete/retry/release requests receive one UUID idempotency key per logical settlement. Copy the request once and reuse its key and body for every attempt. Preserve an existing key. Renewals and claims do not receive automatic retries; replaying a renewal receipt would return the original lease expiry.

Retry httpx network errors, timeouts and remote protocol errors, plus HTTP 408/500/502/503/504. Wait 100/200/400 milliseconds before successive attempts. Local protocol/configuration errors, invalid responses, authentication/authorization failures, validation, conflicts and capacity errors propagate immediately. Exhausted attempts stop the worker through the existing error path. This changes settlement transport reliability, not application handler retries or exactly-once external side effects.

Existing worker request cancellation cancels the whole settlement coroutine, including its backoff. Graceful shutdown releases retain their independent five-second total budget across attempts and delays. A terminal settlement already attempted is never followed by release, including when cancellation leaves its result uncertain. Release retries occur only inside the existing authorized graceful-release path. Keep transport-level mutation retries disabled so this helper owns receipt keys and retry bounds.

## Consequences

No new routes, grants, OpenAPI changes or migration are needed. Opting in requires a server with ADR 1055 receipt support. Default behavior remains one request without a receipt key. Static Python lint/compilation and diff checks verify the change; runtime tests remain deferred at the user's request. This supersedes ADR 1061's deferral of helper receipt retries; settlement batching remains deferred.
