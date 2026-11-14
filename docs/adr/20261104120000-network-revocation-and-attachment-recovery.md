# ADR-20261104120000 · Revoke private flows and recover unpublished attachments

- **Status:** accepted
- **Date:** 2026-11-04
- **Amends:** ADR-009 (identical inner network), ADR-186 (CIDR policy), ADR-187 (firewall), ADR-20261104000101 (replica addresses), ADR-20261103194727 (policy replacement)

## Context

The regular established/related accept retains authorization after a private
policy tightens. Failed live side-link setup can allocate durable member and
service claims without publishing attachment metadata. A promoted service VIP
also needs to update neighbors that remember its previous owner's MAC.
Public TCP inventory must withdraw removed or renamed manifest declarations,
not only declarations explicitly marked internal.

## Decision

Reject guest IPv4 sources other than the fixed ADR-009 address in raw
prerouting, before conntrack and NAT. Recheck private flows using their original
conntrack source, destination, protocol and published destination port in an
IPv4 forward base chain at priority -5, before the regular established accept.
Apply the current policy in both packet directions. Ingress additionally
requires DNAT to a currently owned member/service address. Returns continue
through the regular forwarding gates; terminal drops revoke stale flows.
Platform routes retain their existing gates. Explicit operator lateral-movement
exemptions retain their deliberate egress override.

A namespace-local interval set remembers private destination prefixes for the
namespace's lifetime, across table replacement and daemon restarts. It only
classifies flows; membership grants no access. Atomic replacement adds both
old and new destinations while rebuilding current authorization. Preserve this
table during regular policy resets; namespace teardown removes it. Use complete
atomic replacement for legacy CIDR updates as well as owned side-links.

After failed live attachment setup, release the proposed member claim only
when side-link removal is confirmed. Return cleanup errors and retain claims
when removal is uncertain. Detach probes/removes the lease's owned private link
even if cached attachment metadata is empty. Confirmed full teardown always
looks up durable claims by runtime instance identity. Publish confirmed old-link
removal before attempting a replacement. Preserve the ownership/generation
fences used by native network helpers.

Enable per-peer `arp_notify` before initial link-up, with both member and VIP
addresses already bound. Linux's NETDEV_UP notifier announces both addresses.
After binding a promoted VIP on an already-up peer, assign a fresh locally
administered peer MAC: NETDEV_CHANGEADDR sends gratuitous ARP for all its IPv4
addresses. Keep the journaled host-side veth MAC unchanged. This uses existing
network-admin authority and preserves IPs, routes and conntrack. Publish cached
promotion only after both commands succeed. Failures retain durable ownership
and retry on service reconciliation. If eligibility is revoked after a partial
bind, the durable member projection returns a withdrawal-only service identity;
remove/probe that VIP before releasing its claim even when cached metadata is
empty. No new tool or service privilege is needed.

Require the exact current public name/protocol/port tuple for public TCP
inventory and admission, including after cold wake. Preserve unnamed-port
fallbacks and let internal reservations win over malformed duplicate entries.

Existing namespaces keep their installed rules during daemon adoption. Recycle
instances or rebuild their policy during rollout to install the source guard;
private policy changes install the new guards atomically when reconciled.

## Validation

Regression tests cover manifest withdrawal, original-tuple policy rendering,
source validation, atomic history retention, failed live setup/policy rollback,
hidden-claim detach/teardown recovery, and announcement failure/retry. Linux
kernel acceptance remains opt-in and requires native network namespace and
nftables capabilities. Existing VM lifecycle acceptance still requires KVM.
