# ADR-294: Tenant-scoped self-service customer offboarding

**Status:** accepted
**Date:** 2026-09-27

## Context

After self-service customer creation, routine customer removal still requires
an owner-operated API call. Deleting or disabling customer access must also
invalidate every app-local credential without allowing a downstream tenant to
target another tenant's identity or leave a partially revoked batch.

## Decision

Add `POST /v1/platform-tenant-self/consumers/revoke`, authorized by the existing
tenant-bound `platform_tenant:consumers:manage` capability. The tenant is
derived from the bearer. A request contains 1-100 unique consumer IDs selected
from that tenant's inventory; no account or tenant selector is accepted.

The state operation validates ownership of every requested identity before
mutating anything, then revokes the identities and their active keys together
in one transaction. Foreign, unlinked, and unknown IDs share one not-found
response to avoid exposing ownership. The endpoint remains available when
new-customer provisioning is disabled and while the tenant is suspended, so
policy shutdown cannot prevent cleanup. An exact retry returns the revoked
identities and zero newly revoked keys, without emitting a duplicate audit
event.

## Consequences

- Downstream platforms can remove customer access in their own offboarding
  workflows, including across multiple apps.
- Identity status and active credentials cannot diverge due to a partial batch.
- Provisioning caps and policy affect creation only; cleanup remains possible.
- The operation is bounded, tenant-scoped, replay-safe, redacted, and audited.
- Historical usage and billing records remain intact; this revokes access but
  does not delete customer or financial history.
