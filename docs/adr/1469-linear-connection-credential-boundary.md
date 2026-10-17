# ADR-1469: Verified Linear credential boundary

Date: 2026-10-14

Status: Accepted; provider verification and sealing primitives implemented.

## Context

Issues supports explicit Linear ticket creation and lifecycle synchronization
through a local CLI API key. Native GitHub operations use installation identity;
Linear has no equivalent server connection yet. Persisting a token without
provider identity or connection scope would allow accidental credential reuse
across apps, accounts, or teams.

## Decision

Before sealing a Linear personal API key, query the fixed Linear GraphQL origin
for organization, viewer and the explicitly selected team. Require nonzero UUID
identity, exact team selection, confirmed unarchived team, and no GraphQL errors,
including partial responses. Never retain provider diagnostics in domain errors.

Seal a versioned envelope using the existing age secretbox facilities. Authenticate
account ID, app ID, organization ID, team ID and viewer ID inside the ciphertext.
Opening the credential requires an exact match to all expected identity fields
and the versioned namespace. Support current/previous sealing identities for
rotation. Validate credential bounds using the existing central tracker limit.

These primitives do not authorize users, persist connections, grant provider
write access, or expose a new endpoint. Future connection handlers must enforce
app ownership, entitlement and MFA, maintain revisioned connection records, and
reverify provider identity before native dispatch. OAuth needs its own bearer
authentication, callback-state and refresh/revocation contract; personal API keys
must not be described as OAuth connections.

## Validation

Mocked provider tests cover identity verification, partial GraphQL errors,
archived/missing team evidence, mismatched teams and fixed origin/authentication.
Credential tests cover scope substitution across every identity field, sealing
key rotation, wrong keys/namespaces, absence of plaintext in ciphertext, invalid
local scope before dispatch and sanitized verification errors.

## Next increment

Add durable owner-scoped connection records and explicit connect/disconnect API
and CLI commands, then integrate native status and ticket creation. Automatic
Linear reconciliation also requires explicit team workflow-state mapping.
