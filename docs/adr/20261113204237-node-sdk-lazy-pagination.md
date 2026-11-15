# ADR-20261113204237: Lazy typed Node SDK pagination

- **Status:** accepted
- **Date:** 2026-11-13
- **Milestone:** SDK developer and agent integration
- **Amends:** [ADR-20261113173610](20261113173610-node-sdk-client-isolation.md)
- **Related:** [ADR-20261113202200](20261113202200-node-sdk-request-options-and-diagnostics.md)

## Context

App deployments, account deployments, and builds expose typed single-page
responses and opaque `next_before` cursors. Developers and agents currently
write cursor loops themselves, risking incomplete inventories, unbounded work,
or mixing client configuration between pages.

## Decision

Add item and page async iterators on the isolated public and preview
`client.deployments` namespaces. Derive parameters and responses from generated
services. Preserve the existing single-page methods, CancelablePromise return
types, legacy static services, and capability boundaries. Detect helper-name
collisions rather than replacing a generated method.

Capture client configuration, filters, and copied options at helper invocation,
before lazy consumption. Fetch only when needed; item iteration consumes the
current page before requesting another. Round-trip cursors verbatim and retain
all filters. Empty nonterminal pages advance. Detect cursor cycles and malformed
or oversized page envelopes with a typed PaginationError that excludes payloads
and cursor values. Preserve HTTP errors without resetting or restarting.

PaginationOptions extends request policy with maxPages and maxItems. These
optional nonnegative safe integers count fetched pages and items; zero performs
no I/O. Reduce the next requested page size to the remaining item budget instead
of truncating a server page and losing its resumable boundary. Retain the API's
1–200 page-size contract. Empty pages count toward maxPages; retries do not.

One monotonic deadline begins with the first next(), using the explicit timeout,
client timeout, or 30-second default. Zero disables it. The remaining budget is
passed to each page's request scope, covering header resolution, attempts,
backoff, and body decoding. Consumer pauses count, but no timer or caller event
listener is retained between requests: the next consumption checks expiry and
abort before yielding buffered data or fetching. Signal abort preserves its
reason. Iterator return/throw abort active work before delegating to the native
generator, so close does not queue indefinitely behind a pending next().

Response observers and HTTP retry policies use the existing transport contract.
Attempt numbers describe one page's HTTP call. Concurrent traversals own separate
signals, deadlines, cursors, counters, and configuration snapshots.

## Consequences and verification

Callers can stream histories without accumulating all rows, using explicit
work budgets. Pages are live retained reads, not an atomic snapshot. A cap can end
iteration before exhaustion. A saved cursor is safe to resume only after its
whole page is processed with the same filters; partial item consumption does
not produce a durable checkpoint or an exactly-once guarantee.

Public/preview tests verify laziness, opaque cursors, filters, empty pages,
backpressure, exact limits, invalid inputs and envelopes, cursor cycles,
configuration snapshots, detached methods, concurrent HTTP accounts, retry
overrides, independent deadlines, body cancellation, prompt close/throw, and
idle resource cleanup. Packed-package JavaScript and strict TypeScript checks
exercise all helper exports and preserve existing signatures. Generator checks
verify the helpers require no generated service changes.
