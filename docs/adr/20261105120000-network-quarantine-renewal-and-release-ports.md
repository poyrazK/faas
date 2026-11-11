# ADR-20261105120000 · Enforce network quarantine, timed grants and release ports

- **Status:** accepted
- **Date:** 2026-11-05
- **Amends:** ADR-373, ADR-576, ADR-20261026150000, ADR-20261104170000 (raw ingress)

## Context

Security rescans persist a deployment quarantine before requesting teardown.
A running TCP/UDP target can survive that request. Raw routing and public
listener inventories previously ignored the marker.

On the Ubuntu 24.04 kernel baseline (Linux 6.8), adding an existing nft set
element acknowledges the request without renewing its timeout. DNS/database
refreshes consequently advanced the userspace deadline beyond the kernel's
permission. Instance caches also retained expired entries indefinitely.

Private TCP validated ports before resolving the caller's project release pin.
A retained release with zero ordinary traffic could have a different serving
port from the current release, making a valid pinned connection unreachable.

## Decision

Refuse new raw sessions when any traffic-bearing live deployment has the
security_scan_regressed marker. Read this policy without the ordinary deployment
cache and recheck the selected deployment after instance selection or wake.
A selected deployment marked during selection is refused even if its traffic
weight also becomes zero. Exclude quarantined apps from public listener lookup
and inventories in both stores. Preserve saved listener intent so a clean
replacement deployment restores exposure through normal reconciliation.

Renew timed DNS/database elements with ensure-add, delete, add in one atomic
nft transaction. This establishes the requested timeout on Linux 6.8 without
an intermediate forwarding gap. If expiration races deletion, or the result
is uncertain, retry once with an atomic replacement of the affected timed sets,
retaining all unexpired cached grants at their original absolute deadlines.
A failed application never advances the permission cache. Use the same renewal
path for app seeds, and never shorten a newer instance grant to an older seed.

Prune expired instance grants on grant/seed processing. Bound valid entries
per address family by the existing kernel capacities: 65,535 resolved addresses
and 1,024 database address/port pairs, centralized in pkg/api/limits.go. Reject
new grants beyond capacity before nft; renew existing entries at capacity
and keep IPv4/IPv6 budgets independent. Best-effort seeding fills available
slots without evicting valid grants. These bounds preserve existing kernel
limits and do not change plan quotas.

Resolve the caller's release pin before TCP port eligibility. For a pin, load
the exact live deployment's frozen runtime settings and expose only its declared
TCP listeners and effective serving port. Retained zero-traffic deployments are
eligible; foreign, non-live or quarantined deployments are refused. Unpinned
calls continue using traffic-bearing deployments. All port checks precede
runtime admission, wake and forwarding.

## Validation and rollout

Regression tests exercise warm/cold raw paths, stale caches, quarantine during
selection/wake, listener withdrawal and clean replacement recovery in both
stores. A Linux 6.8 timed-set model covers dual-stack renewal, uncertain writes,
failed reconciliation, preserved deadlines and cache capacity. Opt-in isolated
kernel acceptance exercises the actual nft transaction and recovered deadlines.
Private TCP tests cover retained releases with changed serving ports, scoped
wake, and refusal to borrow a sibling release's port.

Deploy through the ordinary PR/release workflow. No schema, RPC or journal
migration is required. Existing sessions retain their established transport
lifetime; durable quarantine prevents admission of new sessions independently
of asynchronous VM teardown.
