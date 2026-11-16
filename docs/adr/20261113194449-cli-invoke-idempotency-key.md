# ADR-20261113194449: Explicit async invocation retry keys

Status: Accepted
Date: 2026-11-13
Related: ADR-20261022204036 (request-bound idempotency); spec §6.7

## Decision

Expose `--idempotency-key KEY` on `gregale invoke --async`. Only the async
invocation endpoint is wrapped in the existing receipt middleware, so reject
this flag without `--async` rather than suggesting the synchronous route has
that protection. Use the existing CLI logical-key validator and
`api.ContextWithIdempotencyKey`; do not change SDK, middleware, schemas or queues.

Validate before resolving or reading a payload and before authentication or
network access. Trim surrounding whitespace, reject an explicitly blank key,
and retain the CLI validator's existing 255-byte bound and rejection of internal
spaces and ASCII controls. Do not echo the key or payload in validation errors.
An omitted flag retains the SDK's fresh key per mutating request.

The key is request-scoped HTTP metadata, never part of the invocation payload or
shared client configuration. Reuse it for the same logical submission after an
uncertain response, with identical input, target, options, API and credentials.
The CLI adds no automatic submission retry loop. Existing successful receipt
output, JSON errors and exit codes are preserved, including server responses
409 for an in-flight receipt and 422 `idempotency_key_reused` for a changed bound
request. A later invocation read must not carry the submission key.

Replay uses the existing 24-hour server policy, without extending retention or
claiming permanent exactly-once execution. ADR-20261022204036's unbound/legacy,
large-body and host-key-rotation behavior remains applicable: fingerprint
comparison is not always available. Receipt-store failures retain the server's
existing fail-open behavior. Retry the original request unchanged and use a
fresh key for new work. These are documented constraints, not new server rules.

## Verification

CLI tests drop a response after acceptance is stored, then retry the same
command and verify the original ID and a single logical invocation. The fixture
uses the production request fingerprinter and in-memory receipt store. A changed
request gets 422, and the original still replays after that refusal. Tests also
cover explicit versus omitted keys, trimming and maximum length, rejection before
payload I/O or network access, and key isolation from later reads. Built-CLI
journeys verify human/JSON validation and submission; a deployed-API journey
checks real receipt replay and fingerprint mismatch refusal.
