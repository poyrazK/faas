# ADR-20261113202200: Node SDK request options and response diagnostics

- **Status:** accepted
- **Date:** 2026-11-13
- **Milestone:** SDK developer and agent integration
- **Amends:** [ADR-20261113173610](20261113173610-node-sdk-client-isolation.md)
- **Related:** [ADR-20261113180307](20261113180307-node-sdk-bound-execution-workflows.md)

## Context

Independent Node clients isolate accounts and transports, but concurrent agent
tasks still need different deadlines, retry policies, cancellation signals, and
response correlation without changing shared client defaults. Developers also
need diagnostics that preserve typed response bodies and errors.

## Decision

Add an optional second `RequestOptions` argument to isolated generated service
methods: `signal`, `timeoutMs`, `retry`, and `onResponse`. Preserve the first
API parameter object, required parameters, return types, and `.cancel()`.
Methods without API parameters accept `undefined` in the first slot when
options are supplied. Use this consistent position rather than ambiguous
argument-shape detection. Legacy static services retain their signatures.

Each invocation captures client configuration and copies request options and
retry policy. A private generated-core `REQUEST` hook delegates bound calls to
a request scope, without changing hundreds of generated service signatures.
The existing generator post-processor owns this idempotent, fail-closed patch.
Legacy static requests bypass the hook.

One scope owns the timer, caller listener, and underlying CancelablePromise.
Its deadline includes header resolution, attempts, retry delays, and body
decoding. A per-call timeout overrides the client timeout; ordinary calls
default to 30 seconds. Zero disables it. Reject invalid timer values rather
than allowing Node's timer overflow clamping. Signal abort preserves its
reason, deadline expiry uses TimeoutError, and `.cancel()` keeps CancelError.
Retire listeners and timers on every completion path. Signal-bound response
streams also cancel custom fetch bodies that ignore the fetch signal; late
responses after cancellation are retired without observer delivery.

Expose client-wide and per-call `onResponse` observers. Both receive one
frozen metadata object per HTTP response, before decoding: UUIDv4 request ID
or null, HTTP status, one-based attempt, and monotonic duration until headers.
Exclude URLs, credentials, arbitrary headers, and bodies. No-response failures
emit nothing. Callback exceptions and rejected promises are ignored; async
observers are not awaited. Existing typed errors and bodies remain authoritative.

High-level execution helpers retain their own options and cancellation
contract. Their shared client transport also reports response metadata for
submission, SSE connections, and final receipts.

## Consequences and verification

Concurrent calls can use different policies without changing siblings or
future requests. A deadline stops local work and is not evidence of server-side
rollback; callers must retain idempotency identity and reconcile uncertain writes.
Diagnostics measure individual HTTP attempts, not complete workflow duration.

Local HTTP tests correlate forty concurrent requests across two clients.
Tests cover retry overrides and stable mutation keys, independent deadlines,
header resolution, Retry-After waits, JSON/binary/Problem body cancellation,
late custom responses, listener/timer cleanup, observer failure isolation,
typed errors, detached methods, and legacy coexistence. Packed-package tests
exercise the exported runtime and strict TypeScript declarations. Generator
tests and regeneration verify the hook survives generation.
