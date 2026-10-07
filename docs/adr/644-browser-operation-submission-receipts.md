# ADR-644: Browser operation submission receipts and scoped lookup

## Status

Accepted — 2026-10-06.

## Context

The browser feature controller deduplicates clicks and retains uncertain requests
in memory. Reload or signout loses their key, so a later click can submit another
logical operation after the first was accepted without an HTTP response.

## Decision

Add read-only bearer-authorized `POST /v1/platform-tenant-self/customer-operations/submissions/lookup`.
It reads the existing idempotency ledger using authenticated account/customer,
app, environment, operation name and key. It neither admits work nor requires
current deployment/code availability. Responses distinguish accepted retained
work, expired known identity and unresolved acceptance. Unresolved is not proof
of rejection: a request can be in flight or its receipt pruned. No raw input,
result, execution authority or caller-selected owner is returned.

An optional expected_identity on submission and lookup compares the verified
account/customer with the current bearer principal. It is a fence, never an
ownership grant. Optional expected_scope fences the immutable definition
against the browser feature's app, environment and name. The principal fence
prevents credential rotation to another customer between identity verification
and sending a request. Legacy submission stays compatible.

Offer opt-in durable browser receipts through the feature session. Verify the
principal before selecting storage. Save bounded versioned metadata (API,
account/customer, app, environment, name, immutable definition, key, local input
fingerprint and conservative one-day replay deadline) before POST. Do not store
credentials, raw input, results or progress. The local fingerprint is only for
matching user-supplied retry input; platform canonical idempotency remains
separate and authoritative. Save acceptance separately in the same receipt.

The built-in browser store uses localStorage under Web Locks. The lock spans
receipt read, publish, lookup and explicit retry, so tabs share one pending
identity. Missing/unavailable storage, unsupported locks and corrupt receipts
fail before mutation. An application can supply an equivalent store with the
same transaction contract. Persistence is optional; existing memory-only
sessions continue to work.

Resume performs lookup only, then reads current status/SSE. It never submits
work automatically. An unresolved receipt requires identical user-supplied input
for an explicit retry using its frozen definition and key. After its replay
window, or a known expired identity, it remains blocked for inspection instead
of generating a new key. Current authenticated identity is rechecked and fenced
on each lookup/POST. The session binds its first verified scope and rejects later
principal changes. Explicit keys must exactly match the supported HTTP header
representation before publishing metadata. Signout aborts UI work but preserves pending metadata for
the same customer; accepted receipts do not impersonate current business state.
A new explicit start can replace an acknowledgement already resolved by that
session, while another tab's pending request must first be resolved.

## Consequences

The customer can resolve a lost response after reload without retaining input
on disk or building a status table. Tests cover real durable admission plus
lookup, credential switches, lost responses/reload, multiple tabs, input conflicts,
retention and store failures. No new SQL tables or changes to execution,
notification recovery or preview admission are needed.
