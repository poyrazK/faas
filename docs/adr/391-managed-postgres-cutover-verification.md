# ADR-391 · Managed PostgreSQL cutover verification

- **Status:** accepted
- **Date:** 2026-10-01
- **Decision:** Add durable control-plane SQL verification of every staged
  credential and expose preparation, status, verification, and cancellation
  through the account API and CLI.
- **Why:** Sealed credentials alone do not prove that a restored target accepts
  authentication or retains the intended role grants. Activation needs a
  recoverable, inspectable checkpoint before scheduler-owned writer draining.

ADR-390's dependency pins and private staging remain in force. `prepared` means
sealed envelopes. `verifying` queues a bounded SQL probe batch; `verified` means
all staged members have successful SQL evidence. Neither state publishes app
secrets or moves workloads. The runtime refresh path currently starts new
instances before retiring old writers and cannot serve as a restore drain.
Scheduler-owned ingress/work dispatch fencing, writer draining, atomic binding
publication, restart recovery, and activation remain a separate implementation.

The credential sink opens the exact staged envelope with its recorded age
recipient, checks the deterministic future binding reference, requires the
expected single environment key, and verifies the existing value HMAC. It uses
rotation-aware host identities; missing identities or a changed HMAC key fail
closed. Staging must be cancelled and recreated before retiring a required key.
No fresh provider credential is issued during verification.

The SQL probe accepts connection URLs with one explicit TLS mode (`require`,
`verify-ca`, or `verify-full`), rejects extra connection options and plaintext
fallbacks, and requires TLS 1.2 or later. It uses simple protocol for pooled
connections and a read-only transaction. Host PGOPTIONS are excluded. A fixed
sqlc query checks login/database identity, PostgreSQL major version, row security,
public schema access, table/sequence ACLs, and unsafe login/effective role flags.
Read-only credentials must lack DML and sequence write grants. Runtime
credentials must have no schema creation, database creation/temporary privileges,
role memberships, table ownership, or truncate grants, and must retain their
login as current role. Migration credentials must have schema creation rights; their provider-configured schema
owner role may differ from the login. Only membership in that current owner
is accepted; additional memberships and database create/temporary privileges
fail verification. Permission checks inspect grants and do not execute DDL or
DML. SQL errors are reduced to stable safe codes.

A batch deadline is bounded by the provider timeout and half of the lease
interval. Each successful member is recorded against the exact identity,
reference, recipient, HMAC, and ciphertext while holding a live intent lease.
PostgreSQL checks the server clock after acquiring the lock, so lock waits cannot extend an expired probe lease.
Intermediate saves retain the lease; final completion clears it atomically.
After a crash or retry, fresh member evidence is reused. Evidence older than
five minutes or dated in the future must be probed again. Customer freshness
requires every member to be fresh, so the last completion timestamp cannot hide
an older successful probe. Re-requesting an already verified intent atomically
clears all evidence and queues a new batch. Re-requesting a running verification
preserves its lease and progress.

Cancellation invalidates the intent timestamp, retains a running lease, and
fences late verification saves. Existing recovery revokes every possible staged
identity before releasing pins. Cleanup remains available with provisioning
turned off; verification obeys the provisioning and canary gates. The additive
migration rejects rollback while any intent is verifying or verified. Cancel verification before reverting this additive migration. Reverting the
preparation schema still requires completed revocation and pin release.

The API requires the existing managed PostgreSQL scopes, MFA policy, and
verified email for preparation/verification. Mutations support idempotency keys.
Responses contain only resource IDs, binding keys/access, states, timestamps,
freshness, and stable error codes. Credentials, ciphertext, provider IDs, backend
fingerprints, and lease tokens never cross the customer boundary. CLI commands
are asynchronous; `get` observes background reconciliation:

```
gregale postgres cutover prepare SOURCE TARGET APP --scope production
gregale postgres cutover get CUTOVER_ID
gregale postgres cutover verify CUTOVER_ID
gregale postgres cutover cancel CUTOVER_ID
```

A verified result establishes control-plane SQL authentication and ACL evidence
only. It does not establish application VM reachability, restore data correctness,
application compatibility, ongoing availability, or permission to activate.
Validation covers native TLS SQL probes, ACL drift, unsafe roles, durable partial
progress, crash recovery, exact-envelope and expired-worker fencing, cancellation,
tenant boundaries, and API/CLI projections.
