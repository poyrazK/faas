# ADR-188 · Provider-neutral private-network peering

- **Status:** accepted foundation
- **Date:** 2026-09-19

## Context

Gregale private networks now have durable address allocation, node-local
bridges, regional VXLAN transport, and protocol-aware firewall policy. Each
network remains an isolated route domain, however, so connecting two customer
networks would otherwise require provider-specific route mutations.

## Decision

Define a provider-neutral peering contract in `pkg/privatenetwork`. A peering
is account- and region-scoped, connects two distinct Gregale networks, rejects
overlapping IPv4 CIDRs, and produces a canonical two-way desired route set.
Route plans are sorted and safe to replay; duplicate peerings or duplicate
network-to-network routes are rejected before a host mutation is attempted.

The planner does not call DigitalOcean or execute `ip route`. A subsequent
state/API slice will persist peering intent, and a node adapter will apply the
desired routes through the existing fabric while retaining each network's
firewall policy and fail-closed readiness semantics.

## Consequences

Peering behavior is stable across local and cloud-backed deployments, and
control-plane retries cannot accumulate stale one-way routes. The current
slice is intentionally a foundation: there is no customer-facing endpoint or
provider connector until durable lifecycle and node convergence are added.

## Rejected alternatives

- Provider-native VPC peering APIs, which would make network behavior vary by
  backend and violate Gregale-owned enforcement.
- Allowing overlapping CIDRs, which makes route selection ambiguous.
- Emitting imperative host commands from the API layer, which would bypass
  scheduler convergence and rollback safety.
