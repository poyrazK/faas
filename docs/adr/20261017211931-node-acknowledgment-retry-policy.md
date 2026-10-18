# ADR: Adaptive Node acknowledgment intent retry scheduling

- Status: Accepted
- Date: 2026-10-17

## Context

Go saved-callback recovery now offers optional capped exponential full jitter.
Node still uses a fixed delay. Both use the same application-owned schema, which
already retains recovery-claim count and database-clock due time. Node needs
equivalent scheduling with its existing option, abort and pool ownership rules.

## Decision

Add `EventAcknowledgmentIntentRetryPolicy` with optional integer millisecond
initial/maximum caps, default 1000 and 300000. Accept it on existing delivery,
single-iteration and bounded-pass options. Reject a simultaneously defined fixed
backoff, malformed bounds and unknown/accessor/symbol policy fields. Read own data
properties and snapshot/freeze the effective schedule before I/O. Redact option
descriptor failures to a fixed TypeError. Preserve fixed delays when omitted.

Return the atomically incremented delivery count as decimal text from the
existing claim update. Validate it before COMMIT and retain it only inside the
private process-local capability. Use bounded saturating doubling and positive
whole-millisecond full jitter. Match Go's domain-separated SHA-256 and unsigned
big-endian modulo calculation with BigInt to avoid entropy rounding. Randomness
already exists before claim I/O; no new entropy read follows uncertain HTTP.

Keep the callback/preflight/settlement protocol and its SQL unchanged. Only the
selected delay argument changes for unconfirmed settlement. Keep receipt checks,
platform fences, safe-integer generation rejection and unknown-commit handling.
Pass admission budgets, joined abort, pool-release guards and frozen scalar
results retain their existing contracts.

## Consequences

Applications can spread saved-callback retries across Go and Node without a
schema upgrade. The schedule persists, while worker settings govern future
settlements and must be coordinated. Python retains fixed delay until a separate
increment. The durable count includes crashes and uncertain claim commits, not
exact HTTP sends. A failed settlement may not persist a new due time.

This adds no lifetime exhaustion, automatic negative decision or durable remote
Retry-After handling. Callback-level retries and Retry-After behavior remain
bounded by the existing worker protocol. See the
[Node policy contract](../event-acknowledgment-node-retry-policy.md).

## Validation

Node generation parity, TypeScript/declaration build and example syntax passed.
The package contains the policy/recovery/declaration files with built bytes.
Source comparisons confirmed unchanged callback/receipt/fenced settlement and
pass lifecycle/cleanup/progress code apart from schedule forwarding. Shared
schema parity, ADR uniqueness and documentation checks passed. No tests or test
assertions were added/changed, and no local tests or SDK helper smoke checks were
run. Hosted timing, restart, concurrency, abort and uncertainty qualification
remains pending.
