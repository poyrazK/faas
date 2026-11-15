# ADR-20261114013700: Lazy typed Python SDK pagination

- **Status:** accepted
- **Date:** 2026-11-14
- **Milestone:** SDK developer and agent integration
- **Related:** [ADR-20261113204237](20261113204237-node-sdk-lazy-pagination.md), [ADR-20261114004557](20261114004557-python-sdk-async-request-options.md)

## Context

Python callers currently orchestrate each collection request themselves. The
legacy generated deployment-list DTO decodes its timestamp cursor as datetime;
round-tripping a server cursor with nine fractional digits loses precision and
can move the page boundary. Existing datetime inputs and DTOs must stay compatible.
Agents also need bounded traversal and independent cancellation across accounts.

## Decision

Add six FaaSClient async helpers: item and page iterators for app deployments,
account deployments, and builds. Reuse generated route/query builders and row
parsers. Both public and preview facades expose these public collections with
public DeploymentResponse and BuildResponse row types. Keep existing generated
calls and response DTOs unchanged; widen deployment-list before inputs to also
accept exact strings through an idempotent, fail-closed generator adaptation.

Return frozen PaginationPage[T] with tuple rows and an opaque next_before string
read directly from the wire envelope, before legacy datetime conversion. Never
parse, normalize, or decode pagination cursors. Only a completely processed page
provides a safe resume checkpoint. Reject malformed and oversized envelopes and
cursor cycles before yielding the offending page. Empty nonterminal pages advance;
missing, null and empty terminal cursors stop. Propagate typed Problem failures
without restarting from the beginning.

Capture filters and HTTPX client configuration at iterator creation, with a
borrowed transport that preserves the owning connection pool. Perform no HTTP
until first consumption; buffer one page and never prefetch. Each traversal has
its own immutable PaginationOptions and request-policy scope. The total timeout
starts on first consumption and covers consumer pauses, retries and body reads;
check expiry before yielding buffered values. None inherits the client default,
zero disables the deadline, and no timer runs while paused. Page size is 1–200.
Nonnegative max_pages and max_items can stop traversal without HTTP at zero;
reduce the final request size to the remaining item allowance rather than slicing
a server page and retaining an unsafe boundary. HTTP retries count within a page.

AsyncPagination owns the pending driver task. Native task cancellation retains
CancelledError and its message. Explicit aclose cancels pending anext, including
stalled headers/body/backoff, without closing the shared transport. Use an async
context manager or contextlib.aclosing for deterministic early-exit cleanup;
bare async for does not implicitly close this iterator on break. Reject concurrent
anext on a single iterator; concurrent independent traversals remain supported.
Inherited async request machinery bounds waits even when a custom transport does
not cooperate with cancellation, and retires late responses and body resources.

Preserve the handwritten pagination module and explicit exports during generation.
Share language-neutral pagination fixtures between Node and Python source mirrors,
without adding test fixtures to runtime packages.

## Consequences and verification

Developers and agents can inspect bounded collections with generated row typing,
exact cursors and isolated request policy. Legacy datetime DTO cursors remain
lossy by design for compatibility; new page wrappers provide exact resume values.

Both SDKs execute the same public/preview, item/page contract cases for cursor
precision, filters, limits, empty pages, cycles, invalid envelopes, cancellation,
body/backoff deadlines and Problem identity. Python adds real HTTP concurrency
and socket cancellation, captured client settings, buffered deadline checks,
retry/page counting, close ownership, generator adaptation, and installed-wheel
runtime plus strict typing checks for new helpers and existing datetime callers.
