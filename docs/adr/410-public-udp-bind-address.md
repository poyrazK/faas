# ADR-410: Public UDP bind address validation

## Status

Proposed; public wiring review and rollout remain pending.

## Context

The UDP socket owner uses udp4. Allowing hostnames delegates bind configuration to DNS resolution during reconciliation, outside the bounded durable-state read context, and can delay configuration failures until dependencies are open.

## Decision

FAAS_UDPD_BIND_HOST must be an IPv4 address literal. Keep 0.0.0.0 as its default, trim whitespace and canonicalize the value before dialing scheduler or VMMD dependencies. Reject hostnames, IPv6 including mapped addresses, ports and CIDRs. The source-CIDR allowlist remains independently required and validated.

## Consequences

Operators supply a concrete local IPv4 address or the wildcard. No DNS lookup is needed to interpret the production bind setting. Portable configuration tests establish accepted/rejected values; deployed binding, firewall policy, failure recovery and native KVM qualification remain pending. Linux/amd64 and stateless guest storage remain the scope.

Deployment validates the enabled bind literal and each source CIDR before rendering the UDP environment. Strict decimal IPv4 octets and prefix lengths 0..32 prevent malformed or injected nftables source entries. UDP environment values are JSON-quoted so whitespace, quotes and newlines cannot introduce extra assignments. This does not replace runtime source-policy validation.

Both the gateway-public and nftables roles include the shared `deploy/ansible/tasks/validate_udp_policy.yml` assertions. The nftables role validates before any host changes, so independent firewall runs cannot bypass source-policy validation.

The firewall opt-in uses the Ansible bool filter, matching environment rendering and preflight checks. Supported false string values must not expose listener ports merely because a nonempty string is truthy in Jinja.
