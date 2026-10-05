# ADR-609: Global directory organization owners

Date: 2026-10-04
Status: Accepted; regional organization grant integration remains pending.

## Context

ADR-602 registers identities for personal accounts. Organization apps need a
stable tenant identity shared by members and independent of their creator.
Regional deployment authorization and billing currently remain creator scoped;
allowing those paths by changing only the directory role gate would authorize
operations against the wrong regional ownership and quota boundary.

## Decision

A directory owner root references exactly one account or shared organization.
Personal roots retain the existing account UUID; shared roots use a new UUID.
Existing apps, bindings, release journals, delegation metadata and recovery work
use this root as `owner_id`. The authenticated actor remains an account and is
used for audit attribution. Existing sealed delegation payloads preserve their
`AccountID` JSON member so migration cannot invalidate personal consent.

Shared organizations may register and read global identities. A bound API key
selects its own organization and cannot switch tenant via a header or query.
Sessions select an organization with `X-Active-Org` or `org`; the global app slug
is never an organization selector. Existing LoadOrg membership resolution and
the closed organization action matrix authorize reads and creation. Removed
members are denied, as are inactive or pending-deletion organizations. Owner,
admin and developer roles may create; all current member roles may read.

Directory registration uses the organization's app-count limit from the plan
limits table. PostgreSQL checks the live actor, organization and membership
under locks, fences a changed plan, and serializes quota allocation on the owner
root. A retry with the same tenant and slug returns the same app. Shared roots
survive creator departure or account deletion; organization deletion cascades
its directory roots and apps. Existing retained revocation work has no root FK.

Shared regional attachment and image releases fail closed at the API and SQL
binding boundary until the regional organization grant, app authorization and
billing protocol is implemented. This phase creates directory identities, not
runtime capacity, billing entitlement, regional resources or ingress routes.
Global runtime quota admission remains separate from directory app-count limits.

The migration is append-only, replaces affected PL/pgSQL function bodies after
column renames, and backfills personal roots without changing app UUIDs. Rollback
restores personal state and refuses any retained shared root. Regional databases
reject root writes and a populated root directory cannot become a regional cell.

## Validation

Cover member sharing and isolation, organization-bound key selection, current
membership and role enforcement, organization plan limits, concurrent allocation,
role/plan/status changes while registration waits for locks, creator deletion,
organization deletion, immutable roots, shared binding bypass attempts, legacy
ciphertext decoding, personal populated migration round trips and shared-root
downgrade refusal. Native KVM runtime acceptance remains a separate required gate.
