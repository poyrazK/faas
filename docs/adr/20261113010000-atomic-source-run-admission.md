# ADR-20261113010000: Atomic source Run admission

- **Status:** Accepted for implementation; source Runs remain preview and operator gated
- **Date:** 2026-11-13
- **Amends:** ADR-171 source admission and ADR-20261022204036 for `POST /v1/executions` only

## Context

The general idempotency middleware reserves a key, calls the handler, then
stores a response separately. Its storage-error path calls the handler without
a reservation. A crash or lost commit acknowledgement can leave admitted work
without a replayable response. Its 1 MiB fingerprint ceiling also leaves larger
source requests unbound. Artifact capability grants make a later response-cache
miss especially harmful: the first admission has already consumed the grant.

## Decision

Source Runs use `ExecutionAdmissionStore` instead of general response caching.
The PostgreSQL implementation locks the account row, looks up the admission
identity, and replays a matching live entry before invoking preparation. On a
new key, preparation resolves artifacts and seals the request, then the existing
admission checks quota, redeems grants and writes the Run, payload and queued
event. The identity is inserted in that same transaction. Any failed write
rolls everything back. An uncertain commit must be retried with the same key.

Preparation receives a transaction-bound reader for receipts and artifact
grants. It never acquires another connection while holding the account lock,
so a saturated connection pool cannot deadlock those reads. It performs no
external calls. The memory store serializes keyed preparation and publishes
the identity and Run under its existing mutex; it has no restart durability.

The identity contains account ID, SHA-256 of the existing caller namespace,
SHA-256 of the customer key, a keyed request fingerprint, Run ID and timestamps.
The namespace retains general middleware's delegated actor/project scoping.
Runs-only credentials use their stable API-key family; broad credentials and
sessions retain account-wide replay behavior. Keys are 1–255 printable ASCII
characters without spaces. The new ledger stores no raw keys, source, input,
capability tokens or credentials.

The seal-domain inventory classifies these three binary fields as unsealed
hashes/MACs, like the existing `idempotency_keys.request_digest`. They do not
contain ciphertext requiring an age reader or plaintext secret material.

The fingerprint covers the decoded request before defaults, integration
normalization and artifact expansion. Re-encoding JSON with `UseNumber` sorts
object keys, including `input`, while retaining number spellings. Explicit
defaults versus omitted defaults may differ. Every accepted body up to the
existing 3 MiB request cap is covered. Fields ignored by the API decoder have
no admission semantics. A domain-separated HMAC-SHA256 derives from the host
HMAC key with `gregale source Run admission v1`; four bytes of SHA-256 of that
derived key identify it, followed by 28 bytes of the request MAC. Changed
requests receive the existing `422 idempotency_key_reused` problem.

All apid replicas must share a stable host HMAC key. A different fingerprint
key ID returns a capacity error and never unbinds or replaces a live admission.
Keep the original key available for retries throughout the retention window.
This increment does not add a key ring or automatic rotation migration.

Retention is 24 hours from admission, independent of Run completion. Replay
returns `202`, `Idempotent-Replayed: true`, and the **current receipt for the
same Run**, including terminal state; it never extends the deadline or retention.
Expired keys may admit new work. The execution sweeper prunes expired ledger
entries in its existing bounded batches. Admission can replace its own expired
entry without waiting for that sweep.

Unkeyed requests keep existing admission semantics. Persistence failures on
keyed requests fail closed. This is duplicate-free admission within retention,
not exactly-once execution or exactly-once external effects; dispatch leases,
guest failures and destination idempotency remain separate contracts.

## Release and rollback

The migration adds a table and indexes; existing binaries continue to work.
The control-plane guarantee begins only once **all** source-admitting apid
replicas have been upgraded. Disable source admission during cutover if old
and new hosts would otherwise both accept source requests. Do not roll source
traffic back to an old binary while live atomic identities exist: disable the
gate, retain the new table, and reconcile receipts before re-enabling an older
admission path. Other APIs and runtime binaries are unchanged.

A live matching key in the old `idempotency_keys` table returns `409` with
receipt-recovery guidance. Old cached responses cannot prove that their
admission was atomic or bind every supported request size, so they are not
silently adopted or resubmitted. Inspect the existing Run receipt; these keys
age out under their existing 24-hour policy. Pre-upgrade admissions for which
the old cache write was lost cannot be reconstructed from the new ledger.

## Validation

Contract tests exercise concurrent same-key admission, full quota replay,
changed requests, key rotation, preparation and persistence rollback, legacy
key fencing, and namespace separation in memory and PostgreSQL. PostgreSQL
tests cover a new pool/store after admission, preparation with a one-connection
pool, and a failed final ledger insertion. HTTP tests cover concurrent grant
retries, unavailable persistence and a lost commit acknowledgement. Native VM
qualification remains a separate roadmap milestone; this change does not alter
VM lifecycle.
