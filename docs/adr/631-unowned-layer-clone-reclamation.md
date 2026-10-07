# ADR-631 · Reclaim unowned layer clones and tenant cgroups, and retry failed teardowns

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

vmmd also calls `Manager.RetryPendingCleanups` every 2 minutes. It retries
only entries whose last cleanup attempt failed and that no teardown flight
currently owns. `DestroyWithExport` retains its instance before it waits for
the guest and exports the drive. On rc.244 the first version of this retry
treated those in-flight entries as failed: it killed running builder VMs and
unmounted their drive under the export, and every build failed with `EBADMSG`. `cleanup` is
idempotent and serialised per instance, so a retry racing a `Destroy` is safe. A
retry that still fails keeps the instance retained, as before.

The same sweep removes **empty tenant cgroup scopes** (H4-61).

- **Scope:** it looks at every plan slice, current and legacy. A scope is
  removed only when all of these hold:
  - its name is an instance id;
  - it is older than `DefaultReapMinAge`;
  - the ownership gate above says nothing owns it;
  - `cgroup.events` reports `populated 0`.
- **Removal:** it calls `rmdir`, deepest child first. The kernel refuses
  `rmdir` on a populated cgroup, so a process that joins after the check
  makes the removal fail; it is never stranded.
- **Why:** on production-us, 19 such scopes were left across two nodes in a
  week (memory.max reset to `max`, no processes, no memory). Every one
  belonged to a VM that was running when vmmd restarted for a rollout.
  Destroy removes the scope, but the next daemon's recovery path did not.

Restart quarantine is unchanged. Quarantined slots, processes, namespaces and
jails keep the ADR-472 boundary: none of them is released by this sweep.

## Consequences

- Leaked clones are reclaimed within about 10 minutes of the last owner going
  away, instead of never.
- A teardown blocked by a transient condition, such as a shared bind source,
  finishes on its own once the condition clears. Its lease, slot and clone are
  returned without a vmmd restart.
- Each vmmd restart no longer leaves one cgroup scope per running VM behind
  for good.
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
- `TestReapOrphanedTenantCgroups` checks the cgroup sweep's gates. It removes
  an unowned, aged, empty scope and its empty child. It leaves alone a scope
  that is live, populated, young, or whose state is unknown, and any name
  that is not an instance id.
- `TestMetalReapOrphanedTenantCgroups` passed on real cgroup v2
  (gregale-internal-test-1, kernel 7.0). It created an empty scope with an
  empty child and a scope holding a `sleep` process. The first was removed by
  `rmdir`; the populated one survived.
- Native acceptance (`make test-metal`, `make leakcheck`) remains the release
  requirement for this lifecycle change.
