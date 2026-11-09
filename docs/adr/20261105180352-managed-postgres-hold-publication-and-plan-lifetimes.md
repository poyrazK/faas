# ADR-20261105180352: Preserve PostgreSQL hold ownership and fresh plan lifetimes

- **Status:** accepted
- **Date:** 2026-11-05
- **Amends:** ADR-1027 and ADR-20261105123632

An off-site hold copy could replay before its primary PUT completed. Reading a
missing source acknowledged and removed the journal entry, leaving no mirror pin
and zero pending work after a successful placement. A subsequent loss of primary
hold metadata allowed pruning to remove the snapshot backup from both tiers.

New hold-copy journal entries carry the complete hold payload before the primary
PUT. Replay publishes that pin directly, independently of primary visibility.
An idempotent placement also journals the existing pin, repairing work lost by
older writers without changing the hold's creation time. A failed primary PUT
may leave a conservative mirror pin; deleting the snapshot releases it through
the ordinary ordered journal. Legacy entries without a payload still copy from
the primary. Completed archive objects and remote hold formats are unchanged;
the optional journal field is readable by older binaries, whose primary-copy
behavior remains their pre-upgrade behavior.

Mirror file uploads isolate failures by database. A failed file defers the rest
of that database for the pass, preserving WAL and backup-data-before-manifest
ordering, while other databases continue uploading. The pass reports all upload
errors and pending work remains visible. Ordered journal replay is unchanged.

Named prepared statements need the same fresh-Parse semantics as unnamed ones.
Sharing a backend name solely by SQL text leaves a stale result plan after DDL,
even after a protocol Close or a client reconnect. Every new Parse now receives
a unique backend identity. The client's statement retains that identity when it
borrows another backend. No failed SQL is replayed automatically. Existing
backend statement-count limits bound retained plans; closing a client statement
remains tentative until the batch succeeds, so skipped changes can be undone.

A compute-cap sweep can read a ready database immediately before deletion holds
it. Releasing the observed provider hold from that stale snapshot re-enables a
database still marked deleting. Cap provider operations now claim the existing
database lifecycle lease while preserving state, retry time and diagnostics.
The claim checks state, generation, backend and dataset pins after locking the
row. PostgreSQL computes lease expiry from its own clock after any lock wait.
Deletion and other lifecycle workers honor the same lease. A stale or busy claim
is skipped until a later sweep; stores without this capability cannot mutate
provider holds. Shadow mode remains observational.

Provider operations have the normal 30-second deadline within the two-minute
lifecycle lease. Confirmed operations release only their own unexpired lease.
An uncertain provider outcome leaves the lease to expire before another owner
acts. A crash is recovered by the same lease expiry. No catalog migration,
provider interface change, quota change or credential change is required.

Validation includes PostgreSQL 16 direct/pooled controls after DDL, Close and
reconnect; concurrent journal replay with primary metadata loss and pruning;
mirror retry and cross-database upload progress; both cap/deletion orderings;
and memory/PostgreSQL lease contention, replacement and lock-wait expiry checks.
These checks do not replace native Firecracker qualification.
