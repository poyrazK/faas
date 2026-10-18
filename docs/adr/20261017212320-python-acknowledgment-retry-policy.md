# ADR: Adaptive Python acknowledgment intent retry scheduling

- Status: Accepted
- Date: 2026-10-17

## Context

Go and Node now offer optional capped exponential full jitter for saved callbacks.
Python still uses a fixed persisted delay. The shared application-owned table
already holds the recovery-claim count and database-clock due time. Python needs
equivalent scheduling with its native integer and cancellation contracts.

## Decision

Add a frozen slotted `EventAcknowledgmentIntentRetryPolicy` with native integer
initial/maximum caps, default 1000 and 300000 milliseconds. Validate exact policy
type and bounds 1..3600000, rejecting booleans and subclasses. Copy values before
I/O. Accept the policy in existing delivery/single-iteration APIs and append it
to pass options without changing earlier positional field order.

Use `backoff_ms=None` to mean omission. Without a policy it selects the existing
1000 ms fixed delay; any explicit fixed delay conflicts with an enabled policy.
Existing explicit fixed delays retain their bounds. Add the policy module/export
to the generator's handwritten-module registry and root template.

Return the atomically incremented count from the existing claim update and
validate it before COMMIT. Retain it only in the redacted local claim. Calculate
bounded capped doubling and positive whole-millisecond full jitter with the
same SHA-256 domain and unsigned big-endian modulo as Go/Node. Reuse claim
entropy generated before I/O; no new randomness is needed after uncertain HTTP.

Reuse the existing callback/preflight/settlement path with the selected delay.
The unchanged conditional unconfirmed settlement writes database time plus that
delay. Preserve receipt checks, saved input hashes, full int64 generations,
claim/platform fences, shielded cancellation and unknown-COMMIT handling. Pass
admission, joined scopes/cleanup, result/error categories retain their contracts.

## Consequences

All three SDKs can use the same adaptive schedule without a schema change.
Persisted due times survive restart; future settlements still use worker
configuration and require coordination. Recovery counts claims rather than
physical HTTP sends. Failed settlement may not persist the selected delay.

This policy adds no lifetime attempt exhaustion, automatic negative decision or
durable Retry-After propagation. Existing callback-level retry guidance remains
bounded by the worker protocol. See the
[Python policy contract](../event-acknowledgment-python-retry-policy.md).

## Validation

Isolated generation without tests produced 6514 byte-identical source/schema
files. Python 3.11 syntax and Ruff passed. Wheel/sdist policy, recovery, export,
schema and type-marker files match source bytes. Source comparisons confirmed
unchanged callback/receipt/fenced settlement and pass cancellation/cleanup/progress
apart from policy forwarding. Shared schema parity, ADR uniqueness and
documentation checks passed. No tests or test assertions were added/changed and
no local tests or SDK helper smoke checks were run. Hosted timing, restart,
concurrency, cancellation and uncertainty qualification remains pending.
