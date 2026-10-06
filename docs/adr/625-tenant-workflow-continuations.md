# ADR-625: Authenticated tenant workflow continuations

- **Status:** accepted
- **Date:** 2026-10-06
- **Decision:** Allow tenant-bound durable workflow runs to wait for external
  events and callbacks. Expose tenant-self continuation routes that derive the
  tenant from the access token and verify the active tenant-to-app link in the
  same transaction as each durable continuation write.
- **Why:** Tenant-bound runs already preserve a verified tenant identity for
  step dispatch, but rejecting waits prevented customers from building useful
  approval, response, and event-driven workflows without account-owner
  intervention.
- **Consequences:** Tenant tokens with `platform_tenant:invocations:read` may
  list their run's callback handles; tokens with
  `platform_tenant:invocations:manage` may complete callbacks and inject
  events. Foreign, inactive-link, and missing runs share the same 404. Callback
  IDs remain identifiers, never credentials. Callback duplicate and conflict
  behavior matches account-scoped workflows. Tenant schedule starts are
  specified separately in ADR-626.
- **Rejected alternatives:** Reuse account-authorized continuation routes,
  which would require an account credential in customer code; trust the run ID
  or callback ID as a secret, which would make leaked identifiers authority;
  validate the tenant link only in HTTP middleware, which leaves a revocation
  race between authorization and persistence.

The write path checks tenant ownership, tenant status, and an active API
consumer or tenant-surface link. MemStore performs the check under its shared
mutex. PostgreSQL locks the run and tenant rows plus active link rows for the
duration of the continuation transaction, so concurrent revocation cannot
authorize a write after the link has become inactive. Event injection retains
its run-state guard and request idempotency behavior; callbacks retain stable
deterministic IDs and payload-aware duplicate handling.

The public API is available under
`/v1/platform-tenant-self/workflows/runs/{id}/callbacks` and
`/v1/platform-tenant-self/workflows/runs/{id}/events`. The account-owned tenant
run-start route and generic account continuation routes keep their existing
authorization model. No schema migration is needed. OpenAPI, the Go SDK,
generated Node/Python SDKs, and the customer-platform starter documentation
describe the new surface.

Qualification covers both MemStore and PostgreSQL continuation writes, active
and revoked links, cross-tenant isolation, event replay, callback replay and
payload conflicts, and tenant-token API behavior. Scheduled workflow admission
is a separate capability covered by ADR-626.
