# ADR-293: Tenant-scoped self-service customer creation

**Status:** accepted
**Date:** 2026-09-27

## Context

Downstream platforms can manage credentials for customer identities that the
platform owner already linked, but every new customer still requires an
owner-operated control-plane call. That prevents a platform from completing
routine tenant onboarding in its own workflow.

## Decision

Add `POST /v1/platform-tenant-self/consumers`, authorized only by the
tenant-bound `platform_tenant:consumers:manage` capability. The request selects
an active surface already linked to the authenticated tenant and provides only
the app-local external reference and display name. Tenant and app IDs are
derived from the bearer and selected surface; callers cannot create surfaces,
select arbitrary apps, or adopt an existing unlinked consumer.

Consumer creation, tenant linkage, owner-policy validation, and active-customer
cap enforcement happen in one transaction. The tenant row serializes concurrent
creates; the policy row is locked while the active count and insert are checked.
An exact retry returns the same active same-tenant identity without consuming a
second slot, even if the owner later disables new provisioning. Conflicting,
revoked, unlinked, or differently named identities are never silently adopted.

## Consequences

- A platform can onboard customers without routing routine creation through the
  upstream owner.
- The owner retains a default-off policy, tenant status control, and an explicit
  upper bound on active customer identities.
- Creation is constrained to the tenant's already-linked active surfaces, so
  this capability does not expand app, surface, billing, or account access.
- The response contains only consumer ID, external reference, name, and status;
  app IDs and account metadata remain owner-side details.
