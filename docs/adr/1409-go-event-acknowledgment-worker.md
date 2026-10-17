# ADR-1409 · Go receipt-aware event acknowledgment worker

Status: accepted

## Context

The deferred acknowledgment endpoints and application-owned receipt transactions
exist separately. Worker authors must coordinate renewal, commit uncertainty,
terminal callback retries and durable restart recovery themselves.

## Decision

Add an opt-in Go helper constructed from authenticated delivery metadata. Keep the
capability private and redact formatting/serialization. Require a committed receipt
before completion. Read existing receipts before renewal so a lost completion
response can be reconciled without business execution. Starting new work requires
positive token-fenced renewal; an offered-state GET alone is insufficient.

Renew the platform lease while the receipt transaction runs and cancel processing
on uncertainty or confirmed loss. Stop/join renewal before terminal callbacks.
Retry identical terminal inputs with bounded calls and retry-hint handling. Return
receipt confirmation separately from platform acknowledgment. Explicit failure
methods refuse a discovered committed receipt; never map arbitrary business errors
to negative acknowledgments. Support the existing local processing lease for the
final transaction without attempting to renew its locked database row.

## Consequences

Applications retain ownership of durable job scheduling, protected token storage,
exclusive workers, saved negative-disposition intent and receipt retention. The
helper installs no schema, runs no deployment and provides no distributed
transaction across application PostgreSQL and the platform. Cancellation remains
cooperative and business callbacks must avoid external effects. Recovery uses the
same immutable input and committed receipt; a new platform claim requires a new
helper from its authenticated delivery metadata. No HTTP schema or registry changes
are needed for this SDK-only feature.
