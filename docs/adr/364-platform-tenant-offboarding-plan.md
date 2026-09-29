# ADR-364 · Platform-tenant offboarding plan

- **Status:** accepted
- **Date:** 2026-09-28
- **Decision:** Provide a read-only, repeatable-read preview for owner-initiated platform-tenant offboarding. Return a bounded summary and a stable hash over the tenant status, linked resource ownership and lifecycle, active credential identities, and delegated policies. The intended confirmed apply suspends the tenant, revokes active linked consumer keys and tenant-bound tokens, disables delegated provisioning, detaches only platform-managed consumers and surfaces, and removes only platform-managed hostnames.
- **Why:** Ending a platform customer's relationship crosses app-local credentials, tenant-owned access tokens, delegated policies, surfaces, and hostnames. Operators need an impact review and a stale-plan guard before those changes are applied.
- **Consequences:** Previewing never writes, revokes, detaches, deletes, or suspends anything. Unmanaged resources are explicitly counted for retention. Usage, billing statements, reconciliation receipts, and webhook subscriptions remain available for audit and final notifications. Credential material and customer names are not copied into the response; only a digest leaves the store.
- **Rejected alternatives:** Hard-deleting the tenant cascades away identity and billing context needed for support and audit. Reusing the generic desired-state reconciliation plan alone does not include linked credential revocation, tenant-bound tokens, or delegated-policy shutdown.
