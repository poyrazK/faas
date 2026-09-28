# ADR-300 · Tenant-scoped self-service hostname onboarding

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Add the narrow `platform_tenant:hostnames:manage` capability to downstream tenant access tokens. It can add a DNS hostname only to a pre-linked surface owned by the credential's tenant, subject to the owner-configured suffix policy, tenant-wide delegated cap, existing per-surface plan quota, active tenant status, and the existing DNS TXT ownership challenge. Replaying an existing same-surface hostname returns the original pending challenge; the response is no-store and does not use the generic idempotency cache.
- **Why:** A downstream customer should be able to onboard its own custom domain without the platform proxying an account-wide credential, while platform owners retain control over DNS zones, quotas, and eligible surfaces.
- **Consequences:** The tenant cannot create surfaces, alter delegation policy, delete hostnames, choose another tenant, or see upstream app IDs or raw diagnostics. The challenge token is exposed only to the tenant managing the hostname and omitted after verification. DNS and certificate readiness remain asynchronous and are reported separately through activation and lifecycle webhooks.
- **Rejected alternatives:** Reuse deploy:write or account-admin scopes, which would grant unrelated control-plane authority; accept arbitrary DNS names, which removes platform-owner policy; or return the generic owner activation payload, which exposes app IDs and challenge metadata beyond the target hostname.
