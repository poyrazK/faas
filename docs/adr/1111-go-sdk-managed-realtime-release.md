# ADR-1111: Typed Go SDK methods for the managed realtime release

## Decision

Provide concrete `api.Client` methods and schema-derived request/response types
for all 65 new realtime REST routes. Preserve path escaping, explicit optional
zero/false values, query/header options and caller-supplied idempotency keys.
Scoped publisher/worker calls use a separate credential-configured client, reject
redirects and omit account cookie jars. JSONL exports stream to an `io.Writer`
rather than attempting to decode multiple objects as one JSON document.

Keep a deterministic route manifest and generator under `scripts/`, sourcing
contracts from canonical OpenAPI. The SDK coverage gate maps these operation IDs
to actual typed methods; it does not exclude new routes or weaken its checks.
Required request headers are validated before network calls. Validation and
business policy otherwise remain server-owned.

## Verification

SDK route coverage and transport tests verify typed group calls, escaped paths,
explicit TTL zero, stable retry identities, required keys, scoped credential
redirect/cookie isolation, and JSONL streaming. No production or migration
qualification follows from these client contract tests.
