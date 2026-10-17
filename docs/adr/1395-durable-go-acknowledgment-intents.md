# ADR-1395 · Durable Go acknowledgment intents

Status: accepted

## Context

The Go worker can repeat callbacks safely, but process death between a business
commit and callback loses in-memory intent unless the application persists it.
Negative dispositions also need a stable saved action and reason across restarts.

## Decision

Add an opt-in application PostgreSQL schema and SDK helpers. An internal pre-commit
hook stages completion in the same transaction as the consumer receipt/business
writes and optional preparation lease release. Existing public transaction helpers
keep their behavior without requiring the optional table. Negative intent staging
serializes with receipt writes and refuses an existing receipt.

Bind immutable callback payload and token digests to the owner/invocation/attempt
and replay generation. Serialize local admission by invocation after the existing
receipt identity lock. Newer metadata supersedes unfinished old intents; conflicting
or stale writes fail closed. Recovery claims one due intent with SKIP LOCKED, checks
payload hashes, sends only the saved callback, and settles only under a current,
unexpired database claim capability. A committed receipt prevents sending a negative
callback when discovered during recovery.

Return bounded payload-free diagnostics and sanitize secret-bearing storage errors.
Clear protected tokens/results after local terminal settlement, retain metadata,
and leave retention decisions to application owners.

## Consequences

A durable recovery host and protected database/backup access remain application
responsibilities. There is no distributed transaction across HTTP and PostgreSQL;
uncertain callbacks repeat identically and server fencing rejects replaced claims.
Old workers cannot settle newer local claims, though already in-flight HTTP calls
cannot be canceled by local supersession. Owners must stop work before negative
staging and avoid mixing immediate/legacy callbacks for the same attempt. Pruning
newest intent metadata erases its local supersession fence. No platform schema,
registry, deployment or notification changes are included.
