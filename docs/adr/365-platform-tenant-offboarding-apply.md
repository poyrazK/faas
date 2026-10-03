# ADR-365 · Confirmed platform-tenant offboarding apply

- **Status:** accepted
- **Date:** 2026-09-28
- **Decision:** Apply offboarding only when a caller supplies the current plan hash and required idempotency key. Recompute the ownership-aware snapshot while locking the account, tenant, and affected rows; in one transaction suspend the tenant, revoke active consumer keys and tenant-bound tokens, disable delegated policies, detach platform-managed consumers and surfaces, remove platform-managed hostnames, and persist a secret-free receipt.
- **Why:** A preview without a safe commit path cannot end a platform customer's relationship. Hash revalidation prevents applying an impact review to a changed inventory; idempotent response replay and a durable receipt handle retries and lost responses.
- **Consequences:** A stale hash returns conflict without partial changes. Unmanaged resources, usage, billing statements, reconciliation history, and webhook subscriptions remain intact. Receipts expose the resulting action counts, never credential material or customer names.
- **Rejected alternatives:** Hard-delete the tenant or all linked resources, which would erase app-local identities and audit context; or apply from a caller-supplied action list, which could target resources absent from the preview.
