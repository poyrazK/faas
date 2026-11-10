# ADR-20261110120000: Every host's firewall admits its peers by exact private address

- Status: Accepted
- Date: 2026-11-10
- Related: ADR-20261109100000 (bare-metal private card), ADR-20261109170000
  (control-plane artifact signer), ADR-110 (split-box manifest)

## Context

Compute nodes run untrusted tenant code. If a guest escapes to root on one
compute node, the next thing it reaches is every other host's private
services. The host ruleset admits peers by source address, built by
`gregalectl manifest ansible` from each host's manifest `address`.

A host whose `address` is an IP gets a `/32`. A host whose `address` is a
hostname (production: `fsn-1.gregale.dev:9091`) cannot be resolved at render
time, so `appendHostCIDR` falls back to the whole `overlay.cidr`. With one
hostname in the fleet the compute allow list becomes the overlay subnet, and a
compromised compute node can reach every other compute node's vmmd (50051),
gatewayd (8080) and metrics ports, plus the control plane's private services
as if it were any host.

## Decision

1. Each `fleet.hosts[]` entry may carry `private_ipv4`, the host's address on
   the private network. `Host.PrivateAddress()` returns it, else the
   `address` host when that is an IPv4 literal, else nothing.
2. Every allow list the renderer builds (`faas_control_plane_allowed_cidrs`,
   `faas_compute_allowed_cidrs`, `faas_postgres_allowed_cidrs`, the database
   node lists) uses the private address as a `/32`. Without one it keeps
   today's behavior, so existing manifests render unchanged.
3. Validation: `private_ipv4` is IPv4, inside `overlay.cidr`, equal to
   `address` when that is an IP, and unique. It is required on every host
   once `overlay.interface` names a private card (the bare-metal marker from
   ADR-20261109100000), so an OVH fleet cannot fall back to the subnet.

Follow-ups, separate changes:

- Listeners bind the private address instead of `0.0.0.0` (vmmd, gatewayd
  egress and private control, apid app-errors, githubd, realtimed health,
  cloudflared metrics, livequeryd), after checking each one's loopback
  clients.
- Per-node object-store credentials, after the S3 artifact store (#6724).

## Consequences

- Production (GCP, hostnames, no `overlay.interface`) is unchanged until an
  operator adds `private_ipv4`; then the next `make bootstrap-*` narrows the
  rules. Adding a host means adding its address, and every peer's ruleset is
  re-rendered, which the manifest flow already does.
- A wrong `private_ipv4` cuts that host off from its peers; validation
  catches the common mistakes (outside the subnet, duplicate, disagrees with
  `address`).
