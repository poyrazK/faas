# ADR 1074: Python coalesced read assertions

Status: Accepted; runtime validation deferred.

## Context

Frequent explicitly viewed messages can generate redundant read-position writes. Python already tracks confirmed receipts but lacks an owned coalescing lifecycle for outbound read assertions.

## Decision

Export RealtimeReadProgressPublisher, RealtimeReadPublishError and create_managed_realtime_read_publisher, preserved in generation. Bind one publisher to one reader/stream on one asyncio loop. Accept strict nonnegative signed-64-bit positions, ignoring zero/duplicate/older assertions. Retain only the highest pending value. Serialize writes in one owned task with default 250 ms throttle (50–30000). The first write is prompt; subsequent writes coalesce.

Bound each write to five seconds. flush bypasses throttling and awaits pending work under a five-second total budget. aclose rejects new marks, flushes by default, and cancels/joins tasks; flush=False cancels local work. Transport failures/cancellation fail closed, retain pending diagnostic state and expose sanitized errors. Ordinary failure callbacks are isolated. Never automatically retry or invent server read receipts from pending/handoff state.

Provide an account HTTP adapter for existing channel/inbox read-progress routes. Validate successful response sequence is at least the assertion. Caller owns authentication/client closure; existing principal assertion, deploy-write and MFA rules apply. User WebSocket senders remain application-owned.

## Consequences

Applications must assert actual viewing, keep pending and confirmed UI state separate, avoid concurrent flush/close and refresh authoritative state after uncertain failures. Cancellation-cooperative transports are required; remote writes may succeed despite lost responses. No routes, grants, schema or migration change. Static lint/compilation, SDK AST/export/preservation and generated-interface checks verify implementation; runtime validation remains deferred by user request.
