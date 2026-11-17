# ADR-20261115180000: Shared-store object deletion moves to the control plane

- Status: Accepted (slices 1 and 2 implemented, off by default)
- Date: 2026-11-15
- Amends: ADR-054 (compute nodes write and delete the shared artifact store),
  ADR-733 (crash capture GC runs on compute)
- Related: ADR-20261110210000 (customer secrets open only on the control
  plane; slice 3d-3 seals snapshots), the 2026-11-15 compute keys audit
  (row 1: object-store credentials on every compute node)

## Context

Every compute node holds credentials for the shared artifact store. Today
those credentials can delete objects, and on GCS they can also overwrite
them, because overwriting needs delete permission. A rooted node can then
wipe or replace any tenant's snapshots and layers.

Slice 3d-3 of ADR-20261110210000 encrypts snapshots, which handles reading
and undetected tampering. It does not stop deletion. Cold boot keeps a wiped
snapshot recoverable (ADR-005). A wiped layer or rootfs is not: it is what
cold boot needs.

All object deletion runs on compute today. imaged is a compute-only daemon
and runs:

- snapshot-pair GC,
- rollback-window and retention GC,
- app and deployment cleanup,
- layer artifact retention,
- job artifact cleanup,
- crash capture expiry.

vmmd also deletes the objects of failed captures.

## Decision

Deletion decisions stay where they are. Executing a delete moves to the
control plane.

1. **Compute requests, it does not delete.**
   - With `FAAS_STORAGE_DELETE_MODE=defer`, imaged and vmmd call
     `storage.DeferDeletes`. Every shared remote backend then records a
     delete in `storage_object_deletions` instead of performing it. Today
     that means GCS and OCI, reached through the read-through cache and the
     local-prefix router.
   - The cache still evicts its local copy, and node-local prefix routes
     still delete their files.
   - A prefix route that leads to the shared store is refused, because it
     strips its prefix and the recorded key would be wrong.
   - Defer mode refuses to start without a shared store or a database.
2. **The control plane decides.**
   - `FAAS_SCHEDD_OBJECT_GC=1` on a control-plane schedd runs `pkg/objectgc`.
   - A request comes due 10 minutes after it was made. That grace lets a
     cleanup that deletes files before rows finish deleting its rows.
   - When a request comes due, the worker classifies the key:
     - **snapshot** (`snap/...`, including `.age` twins): kept while a live
       row names the key, its capture's memory key, or any object of a v2
       capture directory. Live rows come from `snapshots` (not
       delete-pending), `crash_captures` (not expired), environment branch
       captures, Application Standard captures, ready runtime snapshots, and
       migration leases or admissions.
     - **layer** (`apps/`, `layers/`, `jobs/`): kept while
       `LayerArtifactHasReferences` or operation job retention holds it.
     - **platform** (`base/`, `kernel/`) and **unclassified** keys: never
       deleted by request.
   - A free key is deleted with the control plane's delete-capable
     credentials. A referenced key is retried, with backoff from 10 minutes
     doubling to a day, and refused after 7 days. Platform and unclassified
     keys are refused at once.
   - Outcomes apply only to the request that was checked. A newer request
     for the same key stays requested.
   - `faas_object_gc_requests_total{class,outcome}` counts outcomes.
3. **The outbox is tamper-resistant against per-node logins.**
   - A trigger refuses deletes of request rows by a mapped compute login.
   - It rewrites any write such a login makes into a fresh request stamped
     with the node's name. A node cannot record an outcome or bring its own
     request forward.
4. **Exclusive artifacts retire by generation (slice 2).**
   - Native capture cohorts retire each object conditioned on the
     generation its publication acknowledged
     (`storage.RetireExclusiveArtifact`), so a replacement written under
     the same key survives.
   - In defer mode the node checks the receipt as before, then records
     `(key, generation)` in `storage_object_deletions.generation` instead
     of deleting. A deferrer that cannot record a generation makes the
     retirement fail; it never becomes an unconditional request.
   - The worker applies the same reference checks. A due row with a
     generation goes through `storage.DeleteGeneration`: the matching
     generation is deleted, a missing object counts as deleted, and a key
     that now holds another generation is refused. A generation row never
     falls back to deleting the key.
   - The node's publication journal marks the retirement complete once it
     is recorded. Crash recovery records it again, which only resets the
     request.
5. **Then compute loses the right (operations).**
   - Once every compute node runs in defer mode and the worker keeps up,
     compute credentials drop delete.
   - On GCS, `objectCreator` plus `objectViewer` also blocks overwrites.
   - On S3, deny `DeleteObject` and enable versioning to cover overwrites.

## Consequences

- A rooted compute node can still ask for deletions, but the control plane
  deletes only objects nothing references, and those requests are recorded
  with the node's name.
- Storage is reclaimed up to the grace period plus one worker tick later
  than today.
- The control-plane schedd must address the same store as compute: the same
  `FAAS_STORAGE_*`, with credentials that can delete.
- With slice 2, no compute code path needs delete in defer mode, so the
  IAM step (slice 3) can proceed once every node runs it.
- Not covered:
  - A node that can still rewrite database rows (shared logins, slice 1c of
    ADR-20261110210000) can make a live object look unreferenced.

## Slices

| slice | content |
|---|---|
| 1 | Outbox table and trigger, `storage.DeferDeletes`, defer mode in imaged and vmmd, control-plane worker with reference checks (this change; both switches off) |
| 2 | Exclusive-artifact retirement through the outbox: `generation` column, `GenerationRetireDeferrer`, `storage.DeleteGeneration` on the worker (implemented; rides `FAAS_STORAGE_DELETE_MODE=defer`) |
| 3 | Per-backend IAM: compute credentials without delete; runbook check that a compute delete is refused |
