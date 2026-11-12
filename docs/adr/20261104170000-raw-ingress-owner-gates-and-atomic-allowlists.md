# ADR-20261104170000 · Enforce raw ingress ownership and recover allowlist writes

- **Status:** accepted
- **Date:** 2026-11-04
- **Amends:** ADR-031 (egress), ADR-20261026150000 (database egress), ADR-20261104143000 (public listener withdrawal)

## Context

Raw TCP and UDP target selection can reuse a running VM without asking the
scheduler for admission. A suspended or abuse-held resource owner can therefore
receive new sessions. Enabled-listener inventories also retain its public sockets.

The allowlist handle reader can mistake the later database tuple exception for
the public CIDR exception. A narrowing update then changes the database rule and
leaves the broader public rule installed. Separate IPv4 and IPv6 writes can also
leave a changed family with an obsolete cached handle after partial failure.
Deferring cache publication across replicas loses earlier completed work when
a later replica fails.

## Decision

Public TCP and UDP inventories and public-port lookups require an active
resource owner without an abuse hold. Target selection checks the same current
ownership policy before routing, after selecting a running instance and after
waking one. Account and organization reads bypass the TCP projection cache.
Past-due owners retain runtime access. Adopted company apps use company policy;
their creator's personal suspension or hold does not revoke company resources.
Legacy company apps retain both company and creator restrictions. Management
reads preserve saved listeners so access can recover when a hold is released.

Identify the public allowlist rule by its guest-interface and destination match
plus the explicit SMTP port-25 exclusion. Database tuple and private destination
accepts cannot supply its handle. Duplicate matching rules leave the identity
unknown and force a full policy reconstruction.

Commit both address-family rule swaps in one nft transaction, preserving their
positions before terminal drops. Serialize with DNS, circuit and teardown writes
using the existing locks and instance identity checks. Publish each completed
replica's prefixes and new handles immediately. Any uncertain write or handle
capture clears cached handles so the same revision can reconstruct the policy
on retry. Equal cached prefixes alone do not prove convergence when a required
handle is unknown. Full replacements likewise discard handles after uncertain
acknowledgements.

Reconstruction retains private-network history, current circuit state and active
DNS/database exceptions using their original expiries. Entries with less than a
whole second remaining are omitted rather than extended to the renderer minimum.

## Compatibility and rollout

No schema or protocol change is required. Upgrade public gateways to enforce
fresh owner admission and listener withdrawal; upgrade vmmds for atomic allowlist
recovery. Older components retain their previous behavior until upgraded.

## Validation

Regression tests cover personal and company account policy in both state stores,
cached routes, running and cold targets, holds during selection or wake, recovery
after hold release, database-rule handle exclusion, both nft runner forms,
dual-family failure, replica progress, lost acknowledgements, capture failures,
same-prefix recovery and original timed-exception expiry. Native nft and VM
lifecycle acceptance require the dedicated Linux KVM hosts.
