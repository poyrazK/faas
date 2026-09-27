# ADR-334: Owner-controlled downstream customer provisioning

**Status:** accepted
**Date:** 2026-09-27

## Context

Platform tenants can inspect their linked consumers and manage keys when the
owner enables credential delegation. Customer identity creation still requires
the platform owner to call account-level APIs, which forces downstream
platforms to route routine onboarding through an owner-operated control plane.

## Decision

The platform owner may enable downstream customer provisioning independently
for each platform tenant and set a hard maximum number of active customer
identities. A missing policy is disabled. Disabling requires a zero cap; enabling
requires a positive cap. The cap is enforced transactionally by the subsequent
self-service API, not as an advisory count in the handler.

The owner separately grants the tenant-bound
`platform_tenant:consumers:manage` scope. This scope cannot be minted as an
account-wide API key and does not grant account-owner or cross-tenant access.
The eventual self-service operation must select a surface already linked to
that tenant, derive tenant identity from the bearer, and create/link the
consumer atomically. It must never accept an upstream app ID.

## Consequences

- Provisioning is opt-in and bounded before downstream self-service is exposed.
- Tenant authentication and the owner's policy remain independent gates.
- Customer identities remain attached to existing apps and linked tenant
  surfaces; this policy does not grant surface creation or unlinking.
- The API and SDK expose a durable owner-managed policy for observability and
  later changes without requiring token replacement.
