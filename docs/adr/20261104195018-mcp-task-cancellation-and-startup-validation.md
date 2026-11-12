# ADR-20261104195018: MCP Task cancellation and startup validation

## Status

Accepted; extends ADR-20261104143950 and ADR-20261104050000.

## Context

All three Task runtimes could reclaim an expired lease with a recorded
cancellation and execute its handler before the next heartbeat observed that
cancellation. Node startup, migration and doctor sampled one encrypted field
per key ID, missing unreadable legacy data encrypted under another retained
native key. Node handler registration also accepted visible ASCII tool names
that admission and worker startup rejected. Native release journals could enter
deployment sources through a symlinked parent and invalidate their own source
fingerprint when saved.

## Decision

1. Settle a recorded cancellation when its lease expires, independently of the
   remaining attempt budget. Claim selection and the final update exclude
   cancelled work. Settlement clears execution and retry state without another
   attempt, while a live lease retains cooperative cancellation.
2. Node startup, migration and doctor authenticate every non-null encrypted
   field of every unexpired Task. Legacy envelopes do not identify their key;
   fingerprint registration and one readable sample cannot establish that all
   retained work is readable. A transaction-local cursor bounds memory during
   verification. Expired payloads do not prevent key retirement.
3. Use the same visible ASCII tool-name rule across Node registration, handler
   inventory, admission, worker registration and Task creation: 1–128 characters,
   excluding spaces, control characters and non-ASCII characters. Handler-version
   validation remains unchanged.
4. Resolve native release source roots to their canonical filesystem paths
   before journal containment checks and fingerprinting. Symlinks within the
   source contents remain rejected. A journal outside the sources must remain
   resumable after it is saved.

No platform schema, quota or Task wire-format changes. External effects remain
idempotent by Task ID, and cancellation of an active executor remains cooperative.

## Validation

PostgreSQL regressions cover cancellation with both exhausted and available
retries, preservation of live leases and progress for other queued Tasks. Node
tests cover missing legacy keys in arguments, result, error and input state
through startup, migration and doctor, plus expiry permitting retirement. Tool
name tests cover registration, worker execution, results and admission disable.
Native CLI tests cover journals inside both source roots via real and aliased
paths, plus an external journal that still resumes after saving.
