# ADR-187 · Protocol-aware private-network firewall rules

- **Status:** accepted
- **Date:** 2026-09-19

## Context

ADR-186 established a reusable network-level CIDR baseline, but a CIDR-only
allowlist cannot express the common case of exposing one service port while
keeping the rest of a private network closed. The policy must remain useful on
Gregale's multi-node topology without coupling the API to a cloud provider's
firewall object.

## Decision

Extend the Gregale private-network policy with ordered, provider-neutral allow
rules. Each rule has an ingress/egress direction, TCP/UDP/ICMP protocol, an
optional set of network-contained IPv4 CIDRs, and (for TCP/UDP) explicit ports
or inclusive port ranges. The API canonicalizes values and bounds rule/port
counts before persistence.

The control plane stores the rules with the network and carries them through
reconciliation, schedd, vmmd, and netns. A non-empty rule set is fail-closed:
matching traffic is accepted and unmatched traffic on the private side is
dropped. Rule CIDRs can narrow the network's `allowed_cidrs` baseline but can
never broaden it. An empty rule set preserves ADR-186's CIDR-only behavior.

The first implementation is IPv4-only; dual-stack rules require a follow-up
contract and renderer. No DigitalOcean (or other provider) firewall API is
called: Gregale owns and enforces the policy on every compute node.

## Consequences

Customers get reusable service-level private-network policy with deterministic
multi-node convergence and backward-compatible CIDR semantics. The policy
surface, generated SDKs, and audit payload now include protocol/port rules.
Operators must treat rule updates as a reconciliation event, and IPv6 remains
an explicit follow-up rather than an implicit partial implementation.

## Rejected alternatives

- Provider-native firewall objects: they would make behavior vary by backend
  and would not protect traffic on Gregale-owned bridges.
- Per-app-only rules: they duplicate network policy and do not provide a
  reusable baseline across workloads.
- A default-allow rule list: it would make a typo or omitted rule silently
  expose the private network, violating the fail-closed contract.
