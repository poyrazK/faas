# ADR-286 · Tenant-bound self-service activation snapshot

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Add the special `platform_tenant:activation:read` scope and `GET /v1/platform-tenant-self/activation`. Resolve tenant identity only from the tenant-bound bearer. Return current surface, hostname-verification, certificate-state, expiry, and readiness metadata without app IDs, DNS challenge material, or raw DNS/certificate errors.
- **Why:** A downstream tenant's portal needs an authoritative current-state view during custom-domain onboarding. Lifecycle webhooks are useful for push updates, but they are at-least-once and a receiver can miss deliveries; the account owner's activation endpoint is not suitable for delegating because it includes operational diagnostics.
- **Consequences:** The new capability is read-only and cannot be minted as an ordinary account API-key scope. It reuses the existing activation computation and all-linked-surfaces readiness semantics. The response is non-cacheable and contains only resources explicitly linked to the caller's tenant. Owners retain the diagnostic activation endpoint; no tenant selector, write operation, DNS challenge token, app ID, or provider error is exposed to downstream bearers.
- **Rejected alternatives:** Let the downstream caller supply a tenant ID, which creates an IDOR/impersonation surface; reuse the owner response, which includes errors and DNS challenge material; or rely only on webhooks, which cannot serve as a current-state reconciliation source.
