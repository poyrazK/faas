# ADR-410: Public UDP bind address validation

## Status

Proposed; public wiring review and rollout remain pending.

## Context

The UDP socket owner uses udp4. Allowing hostnames delegates bind configuration to DNS resolution during reconciliation, outside the bounded durable-state read context, and can delay configuration failures until dependencies are open.

## Decision

FAAS_UDPD_BIND_HOST must be an IPv4 address literal. Keep 0.0.0.0 as its default, trim whitespace and canonicalize the value before dialing scheduler or VMMD dependencies. Reject hostnames, IPv6 including mapped addresses, ports and CIDRs. The source-CIDR allowlist remains independently required and validated.

## Consequences

Operators supply a concrete local IPv4 address or the wildcard. No DNS lookup is needed to interpret the production bind setting. Portable configuration tests establish accepted/rejected values; deployed binding, firewall policy, failure recovery and native KVM qualification remain pending. Linux/amd64 and stateless guest storage remain the scope.
