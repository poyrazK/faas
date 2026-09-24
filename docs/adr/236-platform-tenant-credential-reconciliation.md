# ADR-236: Platform tenant credential reconciliation

Status: accepted

## Context

Platform tenants link app-local API consumers, but operators still need to
issue each credential separately. The existing create endpoint generates and
returns plaintext once. Generic HTTP idempotency could persist that response
in `idempotency_keys`, violating the intended hash-only storage boundary.
Cross-app rotation also has a gap: separate revokes and creations can leave a
customer partially cut over.

## Decision

Add an account-scoped credential reconciliation endpoint for one platform
tenant. A caller generates a `ck_` key locally, saves it in its own secret
store, and sends only the key prefix and SHA-256 digest with consumer ID, name,
scopes, and optional expiry. The server checks account and tenant ownership,
active consumer state, exact-match retries, unique names and prefixes, and
per-app/account plan limits. Requested revocations and creations commit in one
database transaction under an account lock. A dry run performs the same checks
without writes. Listing and apply responses contain metadata only.

Credential quotas count unrevoked keys, including expired keys until they are
explicitly revoked. The legacy key-create quota check follows the same rule,
so a revocation frees a slot. Revoked rows remain available as metadata and
for audit history; a separate retention policy may be needed at scale.

An identical apply is repeatable, including after a lost response. A changed
hash under the same name conflicts; callers rotate to a new name and revoke
the previous key in the same bundle. Omission does not revoke. Suspended
tenants cannot issue keys but may revoke them. This is atomic for Gregale's
credential database, not for distribution into a customer's external secret
store or clients. Callers should persist the new plaintext first, apply the
rotation, then roll their clients; revoking the old key at apply time is for
coordinated cutovers, not a long dual-key overlap.

Remove the generic idempotency wrapper from legacy plaintext key creation,
send `Cache-Control: no-store`, and delete historically cached plaintext key
responses in a migration. Legacy retries must use a new key name or inspect
metadata; they can no longer replay plaintext. The new declarative endpoint
also bypasses that generic wrapper: exact-match reconciliation is its
idempotency guarantee, independent of a request header.

## Consequences

Gregale does not receive or recover plaintext for keys issued through the new
flow. A caller that loses its locally generated secret must rotate. The API
cannot prove entropy from a digest, so SDKs and callers must use a
cryptographically secure generator. Generic idempotency still needs a broader
route-scoping redesign; this change closes the plaintext persistence path.
