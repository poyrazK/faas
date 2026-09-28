# ADR-301 · Owner-controlled delegated platform-tenant credential policy

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Keep platform-tenant credential self-service disabled until the account owner explicitly sets a non-empty consumer-key scope allowlist and a positive active-key ceiling per linked consumer. Tenant-bound operations must enforce that policy in the same transaction that applies credential changes.
- **Why:** Platforms need to let downstream customers rotate credentials without granting them account-level API keys. The owner must remain the authority over the maximum privilege and key count the downstream tenant can issue.
- **Consequences:** The account API exposes a tenant-scoped policy read/replace endpoint protected by account scopes and MFA for writes. Policy changes are audited. Only the existing `read`, `write`, and `admin` consumer-key scopes can be delegated, and the per-consumer cap cannot exceed 100. Empty scopes plus a zero cap disables delegation. The later tenant-self management surface can expose metadata and accept client-generated key hashes without returning plaintext; it must derive tenant identity from its access token and never widen this policy.
- **Rejected alternatives:** Defaulting to `read` would make a new tenant credential capability active without explicit owner consent. Reusing hostname policy would couple unrelated controls. Sending key plaintext to Gregale would turn the control plane into a secret-recovery store and make retries unsafe.
