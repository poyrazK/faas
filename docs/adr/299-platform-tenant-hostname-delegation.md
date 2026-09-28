# ADR-299 · Owner-controlled platform-tenant hostname delegation

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Store an explicit, per-platform-tenant hostname delegation policy configured by the account owner. The policy is an allowlist of DNS suffixes plus a tenant-wide cap on attached hostnames. An absent or empty policy disables downstream hostname self-service. A tenant may use a suffix apex or a label-boundary subdomain only; existing DNS ownership verification, feature gates, and plan quotas still apply.
- **Why:** Platforms can currently provision customer hostnames only through an account-scoped onboarding flow. A downstream tenant needs a narrower capability that does not expose upstream app/account credentials or allow naming outside the platform's delegated DNS zones.
- **Consequences:** Policy is persisted separately from platform-tenant identity, is read and replaced only by account-owner APIs, and defaults to deny. Setting or reducing a cap never deletes existing hostnames. Tenant-scoped mutation APIs must enforce this policy transactionally and continue to require TXT ownership proof.
- **Rejected alternatives:** Let each downstream credential choose any hostname, which creates an unrestricted control-plane capability; infer allowed zones from current hostnames, which makes delegation implicit and hard to revoke; or delete existing hostnames when lowering a cap, which would cause an unexpected customer outage.
