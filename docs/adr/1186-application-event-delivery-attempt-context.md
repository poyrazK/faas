# ADR-1186: Application event delivery-attempt context

Status: Accepted
Date: 2026-10-14
Related: ADR-1178, ADR-1185

## Context

Application event consumers receive event/source/subscription/acceptance identity and a synthetic invocation ID, but cannot reliably inspect the executing attempt and replay counter. Those counters and replay parent/root IDs already exist in the invocation ledger.

## Decision

Extend the private scheduler-to-gateway invocation dispatch envelope for async_invoke and replay with invocation_attempt, invocation_replay_generation, invocation_replayed_from and invocation_replay_root derived from the claimed invocation. Validate signed counter ranges, eligible source, nonzero invocation/lineage UUIDs and immediate self-cycle shapes before forwarding supplied metadata. Legacy envelopes with all metadata absent/zero remain accepted without guessing an attempt.

The internal gateway adapter clears all case aliases of four reserved attempt headers after persisted headers and platform identity, then stamps authoritative invocation fields for async_invoke/replay with positive attempts:

- X-Faas-Invocation-Attempt: canonical positive signed-int32 attempt number.
- X-Faas-Invocation-Replay-Generation: canonical nonnegative signed-int64 retry-budget reset counter, explicitly including zero.
- X-Faas-Invocation-Replayed-From: immediate parent invocation UUID when retained in the ledger.
- X-Faas-Invocation-Replay-Root: root invocation UUID when retained in the ledger.

These headers pass the ordinary/streaming/upgrade guest boundary only under the existing scheduler-marked synthetic context. Noneligible sources and legacy dispatch cannot inherit producer-supplied values. Workflow and queue attempt contracts are unchanged. Existing trace propagation and stable event headers are preserved. Metadata is transient and is not written back into retained invocation headers or event envelopes.

Expose EventConsumerDeliveryContextFromHTTP in Go, eventConsumerDeliveryContextFromHeaders in Node and event_consumer_delivery_context_from_headers in Python. Reuse the existing authenticated-owner-scope event request constructor to validate stable identity and original CloudEvents body. Validate case-insensitive duplicate/empty headers, canonical bounded counters, eligible source and UUID lineage. Reject malformed/partial counter metadata. Distinguish fully absent attempt/lineage metadata as unavailable on older deployments; never silently use attempt one or generation zero. Node returns replayGeneration as a canonical decimal string to preserve int64 precision. Keep helpers and exports across Python regeneration through the generator's handwritten-module preservation/template.

## Semantics and rollout

The current attempt increments with the existing execution claim; routing retries that have not admitted an invocation are not execution attempts. Replay generation fences in-place retry-budget resets and can remain zero on a newly created child replay. Child replays have a new invocation ID and Source replay, with parent/root IDs when available. Missing lineage IDs do not prove original delivery. These fields are diagnostics rather than authorization claims, lease fences for consumer writes or receipt keys.

Stable business deduplication remains the owned acceptance and consumer identity. Attempt, generation, invocation ID and lineage are excluded from the transactional receipt digest. Authenticate the delivery route before reading context; public headers alone grant no authority. External effects still require their own idempotency strategy, and handler execution remains at least once.

Upgrade gateways and scheduler together, preferring the gateway first. New gateways accept old zero-metadata envelopes; older gateways may ignore the added private fields and cannot promise metadata. New helpers report unavailable instead of inventing values. No schema, migration, new public endpoint, quota or compliance registry entry is added.

## Validation

Build scheduler/gateway daemons and Go/Node SDKs; regenerate Python while preserving tests and parse final sources; inspect private transport/header authority, synthetic-only forwarding, parser bounds and unchanged business identity/digest code. No local tests are added or run under the user's constraint. Runtime retry/replay and spoofing qualification remains for CI. Previous compliance registry approvals remain pending.
