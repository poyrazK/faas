# ADR-631 · Reclaim unowned layer clones and retry failed teardowns

- **Status:** accepted
- **Date:** 2026-10-07
- **Amends:** ADR-472 (restart resource quarantine) and ADR-477 (journal survivors)

## Context

production-us hunt #4 (2026-10-06, rc.243) found writable layer clones
(`.faas-layer-<instance>-*`, up to 2 GiB logical each) left in `/srv/fc/base`.
fsn-2 held six, about 2 GB allocated; fsn-3 held three, about 600 MB. The oldest
dated from 2026-10-01. None of their instances was live. Two gaps left them
there.

1. **Nothing swept that directory.** A clone is created beside its source
   (`reflinkCloneTemp`), so a host-path drive in `/srv/fc/base` leaves its clone
   there. `ReapOrphanedLayerClones` only scanned the storage cache's two-hex
   buckets. It also ran only at startup, and only when journal-backed recovery
   was off. Production runs with `native_process_recovery`, so it never ran.
2. **A failed teardown was never retried.** `cleanup` retains the lease, slot
   and clone "so Destroy can retry", but nothing calls `Destroy` again for a
   parked instance. During `app restart`, the old instance's teardown failed with
   "bind source mode restoration waits for unknown owner", because its
   replacement briefly shared the bind source. That instance stayed retained
   until vmmd restarted.

ADR-472 and ADR-477 forbid a replacement vmmd from deleting **journal
survivors** by name. That rule protects resources whose ownership the resource
journal still records. These clones had no such record.

## Decision

vmmd reclaims a writable layer clone only when **nothing owns its instance**:

- not this Manager (`HasInstanceOwnership`: live, waking, or a retained teardown);
- not a resource-journal record (`ResourceJournal.Owns`): journal-owned
  resources stay with verified recovery, exactly as ADR-477 requires;
- not durable state (`state.IsLive`). An unknown durable state never authorises
  removal.

The clone must also be older than `DefaultReapMinAge`.

The sweep covers the cache buckets and the flat directories that hold host-path
drives (`FlatDirs`, derived from the legacy kernel path, `/srv/fc/base`). It
runs at startup and every 10 minutes, in both recovery modes. Clones from older
binaries that carry no instance id are still ignored.

vmmd also calls `Manager.RetryPendingCleanups` every 2 minutes. `cleanup` is
idempotent and serialised per instance, so a retry racing a `Destroy` is safe. A
retry that still fails keeps the instance retained, as before.

Restart quarantine is unchanged. Quarantined slots, processes, namespaces and
jails keep the ADR-472 boundary: none of them is released by this sweep.

## Consequences

- Leaked clones are reclaimed within about 10 minutes of the last owner going
  away, instead of never.
- A teardown blocked by a transient condition, such as a shared bind source,
  finishes on its own once the condition clears. Its lease, slot and clone are
  returned without a vmmd restart.
- A deliberately retained teardown (an unconfirmed exit) is retried every
  2 minutes and logged each time it is still pending.

## Validation

- `TestReapOrphanedLayerClones_ScansFlatDirs` checks that a flat-directory clone
  is reaped only when dead and aged; live, young and non-clone files survive.
- `TestRetryPendingCleanupsFinishesFailedTeardown`: a blocked retry stays
  pending with its lease; once unblocked it completes and releases lease and
  ownership.
- `TestLayerCloneOwnershipGate` covers the cases where an instance counts as
  owned: the Manager owns it, durable state is live, or durable state is unknown.
- Native acceptance (`make test-metal`, `make leakcheck`) remains the release
  requirement for this lifecycle change.
