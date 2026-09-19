# ADR-186 · Reusable private-network firewall policy

- **Status:** accepted
- **Date:** 2026-09-19

## Context

Gregale-owned private networks already provide address allocation, node-local
bridges, optional VXLAN transport, and an app-level `allowed_cidrs` exception.
The app field is not reusable: every workload must repeat the same allowlist,
and the control plane has no network-level policy that can be reviewed or
updated independently.

## Decision

Add an optional `allowed_cidrs` list to the Gregale network definition and a
`PUT /v1/networks/{id}/policy` replacement endpoint. Policies are IPv4 CIDRs,
limited to 64 entries, and must be contained by the network CIDR. An empty
list means no additional restriction (the existing compatibility behavior).

During reconciliation the network policy is the upper bound. An attachment
may provide a narrower list, but a list outside the network policy is rejected
and the attachment remains `error`. The effective list is sent through the
existing provider-neutral VMMD route contract, which applies symmetric
private ingress/egress nftables rules and publishes ready only after the
update succeeds.

Policy is stored with the network row, so it applies consistently across
nodes and survives restarts. Updating it does not call DigitalOcean or any
other cloud API; schedd converges all live nodes asynchronously.

## Consequences

Customers get a reusable network firewall baseline and fail-closed updates
without introducing provider credentials. CIDR policy is intentionally the
first slice. Port/protocol rules, rule ordering, and per-rule audit metadata
are deferred until a versioned network-policy model is needed.
