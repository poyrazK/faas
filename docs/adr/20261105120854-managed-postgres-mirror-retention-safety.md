# ADR-20261105120854: Mirror holds protect PostgreSQL recovery data

- **Status:** accepted
- **Date:** 2026-11-05
- **Amends:** [ADR-1027](1027-native-managed-postgres-on-firecracker-database-nodes.md), off-site archive retention and restore fallback

A primary bucket can successfully return an empty or incomplete listing after
data loss. Previously its missing holds authorized pruning local recovery
files and journaling their deletion from the independent mirror. The mirror
could retain a snapshot hold after deleting the data required to restore it.
An empty primary backup catalog also hid complete mirror backups.

Pruning now verifies the union of primary and mirror holds before deleting
local or primary recovery files. The earliest hold bounds retention. Failure
to list or read either configured hold inventory defers deletion, including a
listed hold whose body has disappeared. A released primary hold can retain
data until its release reaches the mirror. Hold creation, lookup and release
continue using the primary as their authority.

This deliberately amends the promise that pruning never waits for the mirror.
Mirror hold reads happen outside the archive maintenance lock, so primary WAL
and backup uploads continue during a slow or unavailable mirror. Deferred
pruning can increase archive storage; existing prune error and off-site pending
metrics expose failed checks and delayed journal cleanup. Explicit database
drops remain authorized to remove the whole archive, including holds.

Mirror replay also checks its own recovery anchor before applying queued
backup or WAL deletions, including entries written by an older release. It
keeps the newest complete backup consistent before the earliest mirrored hold,
all later backups and WAL from that anchor onward, plus timeline history.
Without a proven anchor it preserves all recovery files. Protected entries
remain pending while later entries can run, so a hold release behind them
cannot deadlock cleanup. A later pass retries the deferred deletes. Checks are
cached per database within a replay pass and invalidated by hold changes or a
drop. There is no new journal operation, object format, RPC or migration;
older nodes still need upgrading because their pruners lack these checks.

Restore discovery consults the mirror when a primary catalog is empty or has
no backup consistent at the requested time. Candidates still require paired
manifest and data objects and a valid stop time. A usable primary restore
does not require a mirror catalog read. Closing a remote backup cancels its
request and closes the input pipe before waiting for decoder shutdown.

Validation: race-enabled archive tests exercise lost primary metadata through
local, bucket and fleet pruning; queued destructive replay, delayed releases,
corrupt pins and missing recovery anchors; uploads during stalled mirror checks;
protocol restores from incomplete primary catalogs; and partial-stream cleanup.
