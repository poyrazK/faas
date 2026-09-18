# ADR-185 · Reserved public IP route reconciliation

- **Status:** accepted control-plane follow-up to ADR-184
- **Date:** 2026-09-19
- **Decision:** reconcile the complete desired set of assigned reserved-IP routes per region through a provider-neutral connector. A route carries the lease generation as its fencing token; the lease becomes `assigned` only after the connector accepts the desired set.
- **Why:** inventory and tenant claims are durable, but multi-node public addresses are not usable until a failed or drained node can be replaced without publishing stale ownership. Full-set replacement also withdraws routes for released leases.
- **Connector contract:** `Apply(desired routes)` is idempotent and replaces the connector's scoped route set. The first implementation is deliberately an interface plus fake coverage; netlink, BGP/VRRP, and other fabric adapters remain deployment-specific.
- **Safety:** active nodes are selected deterministically within the lease region. State transitions use generation compare-and-swap, stale sweeps lose with `ErrConflict`, and route activation failures leave the lease in `error` rather than claiming readiness.
- **Out of scope:** DigitalOcean APIs, customer-facing route controls, quotas/billing, and physical network provisioning.
