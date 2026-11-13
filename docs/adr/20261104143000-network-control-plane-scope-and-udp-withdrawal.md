# ADR-20261104143000 · Scope shared router policy and withdraw stale UDP listeners

- **Status:** accepted
- **Date:** 2026-11-04
- **Amends:** ADR-409 (UDP ingress), ADR-568 (native ownership), ADR-187 (firewall), ADR-20261104001001 (routed peering)

## Context

Shared router policy was dispatched through the native per-instance runner,
although a fabric RPC has no VM lease. UDP inventory retained removed or renamed
manifest ports, allowing already admitted peers to survive reconciliation.
Live firewall RPC validation rejected peered attachments because their destination
list contains more than the owning network's CIDR.

## Decision

Run account/region router firewall transactions through the same node runner that
owns fabric links, under the existing fabric mutation mutex. Validate every nft
command against the derived router namespace before executing one transaction.
Keep per-instance commands behind their original native lease/generation fence;
shared fabric reconciliation cannot supply or replace VM authority.

Require a current public name/protocol/guest-port tuple in both UDP route lookup
and enabled-listener inventory. Internal declarations take precedence over
conflicting public entries. Use the same rule for target admission. Withdrawal
removes the supervised socket and cancels/joins all admitted peer streams; it
preserves the durable port reservation. Restoring the declaration rebinds it.

For a live firewall update with member identity, choose the owning subnet by the
IPv4 member address, independent of destination ordering. Validate rules against
that subnet and retain all peering routes. Omitted rule CIDRs keep their owning
subnet scope. Reject invalid or out-of-range member addresses and reject ambiguous
multi-CIDR firewall updates without member identity. Legacy single-CIDR updates
retain their existing validation.

## Validation

Portable tests exercise native fabric/peering setup without VM leases, atomic
failure/retry, namespace isolation, and unchanged per-VM lease enforcement.
Real UDP sockets and protobuf gRPC streams verify withdrawal and restoration for
removed, renamed, internal, and changed protocol/port declarations. State tests
cover memory/PostgreSQL inventory parity. RPC tests cover both route orderings,
owning-subnet rules, omitted scope, and invalid identities. Privileged namespace
and KVM acceptance remain separate native-host gates.
