# ADR-171 · Provider-neutral reserved public IP leases

- **Status:** accepted control-plane foundation
- **Date:** 2026-09-18
- **Decision:** model a reserved public address as an account-owned lease with
  an explicit lifecycle (`available`, `pending`, `assigned`, `error`), one
  workload assignment, an optional active node, and a monotonically increasing
  generation. The lease contract is provider-neutral and does not call a cloud
  API or move an address on a host.

## Why

DigitalOcean Reserved IPs can be reassigned between resources in one region,
which is the missing multi-node evolution of Gregale's current single-node
static-egress-IP feature. Gregale already has the network fabric and node
identity primitives; the next safe slice is the durable ownership and
failover contract that a provider/route connector can consume.

## Scope

This slice adds the state model, Postgres schema, in-memory parity, transition
guards, and deterministic region-scoped failover selection. It intentionally
does not claim that an address is routed: `pending` remains fail-closed until
a future connector marks the assignment `assigned`.

The following remain follow-up work:

- provider/operator IP inventory and address advertisement;
- host alias/BGP/VRRP or equivalent route movement;
- customer-facing API/CLI and plan quotas;
- ingress binding and outbound SNAT integration.

The separation keeps a control-plane lease from becoming a false promise of
physical reachability.
