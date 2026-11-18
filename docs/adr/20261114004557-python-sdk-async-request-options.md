# ADR-20261114004557: Python SDK async request options and diagnostics

- **Status:** accepted
- **Date:** 2026-11-14
- **Milestone:** SDK developer and agent integration
- **Related:** [ADR-20261113202200](20261113202200-node-sdk-request-options-and-diagnostics.md)

## Context

The Python SDK exposes generated sync and async operations over HTTPX clients.
Concurrent agent tasks need individual deadlines, retry policies, and response
correlation without changing shared HTTPX settings. HTTPX phase timeouts do not
provide a total budget across an entire operation, retry delays, and body reads.

## Decision

Add FaaSClient.arequest with a factory returning a fresh generated coroutine and
immutable RequestOptions. The generic return type retains the operation's parsed
or detailed response type. Existing generated signatures, imports, and sync APIs
remain available. The preview facade inherits this method and policy types.

Bind request policy to the specific async transport using task-local context.
Copy request extensions before applying per-call I/O timeouts; never update the
shared client's timeout, headers, or retry defaults. Nested or concurrent clients
retain separate policies. Options use seconds; an absent total timeout inherits
the constructor default, and zero disables it. Asyncio task cancellation retains
CancelledError and its message.

Own the operation in a worker task and bound the caller's wait independently.
On timeout or cancellation, cancel the worker and close active bodies without
waiting indefinitely for arbitrary custom transport cleanup. Retire late
responses without delivering diagnostics or allowing success after expiry.
Cooperative cancellation releases HTTPX resources; custom transports remain
responsible for cooperating with close/cancellation. Streaming bodies must be
consumed inside the operation's lifetime.

Scoped retries inspect raw statuses before decoding the final Problem. Preserve
typed errors and mutation identity across attempts, honor Retry-After seconds or
HTTP dates within the total deadline, and send one-shot uploads once. Immutable
RetryPolicy counts total attempts (1–9). Legacy RetryOptions continues to count
retries, and is converted only when inheriting defaults for scoped calls.
Existing unscoped chains retain their behavior.

Client-wide and per-call async response observers receive the same frozen
ResponseInfo: validated UUIDv4 request ID or None, status, one-based attempt,
and monotonic duration to headers in milliseconds. Exclude URLs, credentials,
bodies, and arbitrary headers. Ignore observer failures and do not await their
completion. Clear request-policy context before invoking observers, and retire
observer tasks when the async client closes. Synchronous calls are unchanged.

Preserve the handwritten module and authored export barrels during generation.
Import-only changes to these barrels export real capabilities and must not be
reverted by cosmetic drift reconciliation. Publish shared test fixtures alongside
the Node and Python source mirrors without adding runtime package data.

## Consequences and verification

Python agents can budget and diagnose individual async operations with typed
results. Socket-phase errors remain distinct from RequestTimeoutError for a
total deadline. Cancellation does not establish that a server-side mutation was
rolled back; callers must retain idempotency identity and reconcile uncertain writes.

Both SDKs execute common fixtures for success, retries, typed errors, identifier
redaction, cancellation, header/body/backoff deadlines, network failures, and
mutation identity. Python tests also cover real concurrent HTTP accounts,
independent policies and budgets, observer failure/context isolation, late
responses, one-shot uploads, sync compatibility, and public/preview exports.
Wheel consumers run outside the checkout and verify strict generated result
typing and immutable options. Generator regression tests preserve new exports.
