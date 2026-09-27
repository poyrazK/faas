# ADR-247: Downstream platform-tenant self-service access

**Status:** Accepted

**Date:** 2026-09-25

## Context

Platform customers can already inspect their end customers' cross-app usage and create immutable, finalized billing statements inside Gregale. Those account-scoped views are for the platform operator; a downstream customer has no safe way to read its own records directly without receiving an account-wide Gregale API key or asking the platform to proxy them.

## Decision

- Allow an account owner to mint a distinct `fp_tenant_` bearer bound to one immutable platform-tenant ID, with one or both of the narrow usage-read and finalized-statement-read scopes.
- Store only the SHA-256 hash, a display prefix, scoped metadata, expiry, last-use time, and revocation time. Return plaintext once, never cache it in generic idempotency storage, cap lifetime at 365 days, and allow revocation. Limit each tenant to ten active credentials with unique active names.
- Accept this bearer only on a small, explicit GET-only `/v1/platform-tenant-self/*` allowlist. The tenant ID comes from the authenticated token and cannot be chosen by request parameters or paths. The special scopes are excluded from account API-key minting and never imply account admin.
- Expose the tenant's raw usage and finalized immutable statement revisions. Draft, superseded, and foreign-tenant statements appear as not found; statement listing is bounded, paginated, and returns summaries so line items are fetched only for a single statement.
- Keep this capability read-only. Platform owners continue to create, price, finalize, and hand off statements. Gregale does not collect payment or expose account configuration through a tenant bearer.

## Consequences

Downstream customers can reconcile usage and billing facts directly, while platforms avoid building a credential proxy. The platform remains the trust root for tenant identity and access revocation. A bearer leak is limited to one tenant and the scopes selected at issuance; rotation is revoke-and-reissue. Usage remains event-time attributed and diagnostic/debugger activity is not part of this self-service surface.
