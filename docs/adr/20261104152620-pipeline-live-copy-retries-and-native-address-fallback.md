# ADR-20261104152620: Pipeline live copy retries and native address fallback

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-04
- **Amends:** ADR-1591, ADR-20261103220000

## Context

A live-table repair could report a stalled native copy or missing relation
tracking, while both its table-recovery and schema-retry controls rejected another
attempt. Other tables continued syncing, but repairing the affected table could
require a full resync. The original live-repair approval required a mismatch
proof and a ready native relation, neither of which describes a failed fresh copy.

Source inspection tried multiple qualified DNS addresses. Native connection
generation instead pinned only one sorted address. A verified control connection
could succeed over IPv4 while the native subscriber selected unreachable IPv6.

## Decision

Allow a fresh table-recovery preview for the quarantined live table when its copy
is blocked at the copying stage and its current copy evidence reports a stall or
missing tracking. Preserve the source baseline, generation, selected table,
publication/subscription identities, read gate and shared replication stream.
Other-table tracking loss remains a shared failure.

Add an optional `CopyRetry` flag to the recovery approval and persist it in the
private receipt. It distinguishes replacement after a copy failure from repair
after a row mismatch. Include the flag in the approval hash; false is omitted to
preserve existing approval identities. Copy retries do not require the old
mismatch checkpoint or a ready native relation. They retain the ordinary exact
preview, revision/freshness checks, qualified capacity and ten-attempt budget.

No rows are cleared during preview. A newly approved copy retry uses the existing
withdrawal, native retirement, transactional clear, republish and copy stages.
The table stays quarantined through scoped verification and API refresh. If its
previous copy finishes while confirmation is pending, the fresh approval still
authorizes replacement of that quarantined, unverified scope; it cannot select
another table. Completion and lost acknowledgements retain their original
ownership and lease fences.

Observe copy progress using the durable private recovery stage. A lost catalog
acknowledgement of copy registration must not conceal missing tracking or prevent
another exact retry. Expand existing private receipts with a false default before
reading the new field, including during completion replay. An older worker that
does not understand a true flag rejects its approval hash rather than replacing
rows under different consent.

For native connections, retain every freshly resolved, qualified address. Unmap,
deduplicate and sort addresses, excluding unspecified, multicast, link-local and
unqualified destinations. Pass libpq matching host/hostaddr lists: each host is
the original verified hostname, while each hostaddr is one qualified IP. Native
PostgreSQL can try subsequent addresses without performing an unguarded DNS
lookup. Preserve the original database, user, password, port, verified TLS mode,
system trust. Bound each native connection attempt to five seconds, preserving
an explicitly shorter timeout. This also bounds fallback when an address drops
connection attempts. A single-address connection keeps its existing host
representation; an empty qualified set fails closed.

## Validation and rollout

Native acceptance retries a stalled live repair, then retries missing tracking
after losing a copy-registration acknowledgement. It checks distinct approval
modes and attempts, no clear during preview, exactly one clear per approval,
continued unrelated CDC, unchanged healthy storage and shared subscription,
scoped proof, gated reads and lost completion acknowledgement recovery. Unit
checks retain freshness, scope, readiness and retry-budget restrictions, and
preserve a legacy private receipt's approval after expansion.

Address tests cover filtering, mapped-address deduplication, original TLS identity
and immutable source URLs. A native PostgreSQL libpq connection tries an
unreachable IPv6 address before succeeding with verified TLS over IPv4.

No new catalog migration is needed; the control-plane flag is additive JSON and
the owned remote receipt acquires its column lazily. The combined branch still
requires its existing setup-ownership migrations. Production admission remains
disabled pending deployed qualification.
