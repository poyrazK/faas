# ADR-317 · Tenant-scoped self-service consumer credentials

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Add separately scoped tenant-self endpoints to list linked consumers and redacted credential metadata, then atomically create, rotate, or revoke consumer keys for those linked consumers. The authenticated bearer supplies tenant identity. New keys must satisfy the account owner's delegated scope allowlist and per-consumer active-key ceiling inside the credential transaction.
- **Why:** Platforms need downstream customers to rotate application credentials without routing every rotation through the platform operator or granting customers account-wide API access.
- **Consequences:** `platform_tenant:credentials:read` exposes only consumer IDs, external references, names, statuses, and key metadata for the token's tenant. `platform_tenant:credentials:manage` accepts client-generated key prefixes and SHA-256 hashes; plaintext and hashes are never returned. The owner must explicitly enable and bound issuance. Revocation remains available when issuance is disabled. Account API keys cannot receive these special tenant scopes.
- **Rejected alternatives:** Sending plaintext to the control plane would create a recoverable secret store. Letting tenants choose app or tenant IDs would widen the authorization boundary. Reusing hostname-management scope would grant unrelated capabilities. Requiring owner involvement in every rotation would prevent delegated self-service.
