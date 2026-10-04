# ADR-482: Durable regional delegation and release recovery

Date: 2026-10-04
Status: Accepted.

## Context

ADR-481 supplies a regional credential boundary. The directory still requires
account-admin keys on every resume and cannot recover independently after an
outage or restart. Detach must also retain a way to revoke regional authority.

## Decision

Directory apid seals a random fixed token, grant UUID, expiry and complete
owner/global-app/cell/binding/origin identity before registration. Only its hash
is registered with a request-only regional admin key. Registration retries use
the same sealed identity. A scoped GET recovers a lost registration response;
subsequent receipt recovery uses the scoped token without another admin key.
Broad admin keys are never persisted. Ciphertexts use the existing age identity
and rotation loading, a purpose namespace and a bounded authenticated payload.
Missing identities fail closed. Decryption checks all immutable metadata and
hash, and occurs only after current owner, cell, binding and generation checks.

A durable per-app work queue uses bounded claims, leases and retry timestamps.
Claims never exceed app execution concurrency. Separate release/revocation pools
share an app deadline; a durable cursor gives retained grants bounded turns.
Only apid drives it. Superseded generations, detached/reverified targets and
inactive owners prevent new release dispatch. Serving and wake remain independent
of the directory. Claims are scheduling leases; regional receipt identities and
generation checks enforce idempotency and stale-result fencing.

Detach, re-verification and global-app/account deletion atomically retire usable
delegations into a revocation outbox. Immutable original endpoints remain usable
only for revocation. The outbox survives deletion of its global owner/app. A
positive matching revoked response or expiry ends it and erases ciphertext.
Authentication failure alone is not durable revocation proof; it remains retryable
until terminal evidence or expiry. Regional backend auth failures return 503.
Already dispatched requests can finish; the regional revocation/expiry check
under its app lock is the final authority for acceptance.

Credentials last six days, within the seven-day regional bound. Per-app live
journals, app batches, concurrency, deadlines and claim leases are in
pkg/api/limits.go. Runtime SQL uses SQLC. Database guards enforce immutable
identity, terminal states, retained revocation and populated-downgrade refusal.
These credentials expire and require renewed admin consent; they never renew
themselves. Release acceptance remains distinct from regional readiness.

## Validation

Verify sealed-before-I/O registration, lost responses, restart and outage recovery
without admin keys, concurrent allocation/claims, detach and delayed-worker
fencing, deletion survival, expiry and revoked credentials, tamper/identity/key
rotation checks, no plaintext in persistence/public/audit/error surfaces, and
PostgreSQL trigger/lock/downgrade guards. Full multi-region completion still
requires global identity/quota, health/ingress, managed placement and native KVM
recovery acceptance.
