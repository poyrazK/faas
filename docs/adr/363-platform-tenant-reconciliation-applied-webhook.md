# ADR-363 · Platform-tenant reconciliation applied webhook

- **Status:** accepted
- **Date:** 2026-09-28
- **Decision:** Add the opt-in `platform_tenant.reconciliation.applied` tenant webhook event. Insert its durable delivery from the receipt row in the same database transaction as the reconciliation. Include the tenant identity, receipt ID, plan hash, applied timestamp, and change count; consumers fetch full applied changes from the receipt API.
- **Why:** A successful synchronous response can be lost, and polling every platform tenant for new receipts is wasteful. A signed, retryable notification lets a platform react promptly while the durable receipt remains the source of truth.
- **Consequences:** Delivery is transactional, at-least-once, and deduplicable by its stable delivery ID. The event is opt-in and is never backfilled. It reports successful commits, including no-op applies, and excludes desired bundles, resource-change details, DNS challenge tokens, and credentials.
- **Rejected alternatives:** Making every webhook receiver subscribe by default adds unsolicited traffic. Including full changes duplicates the receipt, increases payload sensitivity and size, and creates two representations that can drift. Polling alone delays automation and adds recurring API load.
