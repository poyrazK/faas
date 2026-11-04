# ADR-20261103091657: Managed PostgreSQL work on existing databases outlives approvals, capacity changes and restore copies

- **Status:** accepted (owner, 2026-11-03: "lets solve those issues in the next pr")
- **Date:** 2026-11-03
- **Owner:** apid (managed PostgreSQL) maintainers
- **Amends:**
  - [ADR-20261028103115](20261028103115-managed-postgres-production-provisioning.md): the production approval gates new databases only;
  - [ADR-20261023181931](20261023181931-rollbacks-that-restore-the-database.md): deleting the source after a switch is the person's call, and now it can be made;
  - the backend fingerprint in `docs/managed-postgres.md`: a capacity change can be declared instead of fencing every database.

## Context

A review of the managed PostgreSQL code path on 2026-11-03 found three ways an existing customer loses working operations.

1. **A lapsed approval closes recovery.**
   - In production one gate, `NewProvisioningGate`, decided every operation that reaches the provider for a customer.
   - It holds only while the operator's approval window is open (at most 90 days) and the approved fingerprint matches the configured backend.
   - When it closed, these were refused with `managed_postgres_provisioning_not_enabled`:
     - point-in-time restore;
     - credential rotation;
     - resize;
     - new bindings, so any deploy that attaches a database;
     - rollbacks that restore the database;
     - finishing a database already admitted and reserved.
   - ADR-20261028103115 meant the approval to close new provisioning "without touching existing databases". Rotation in particular is how a customer answers a leaked credential.
2. **A restore copy pins its source forever.**
   - A Neon restore is a child branch inside the source's project. Deleting the source would delete the project, and every copy with it.
   - So a source with an undeleted copy refused its delete: `ClaimDelete`, `ClaimScheduledDelete`, and the `managed_postgres_restore_sources_guard` trigger.
   - After a rollback that restores the database, the app runs on the copy. The source has no bindings left, but it can never be deleted while the copy lives. It keeps one of the plan's databases and its storage.
   - On Pro (3 databases) one or two such rollbacks use up the plan. The refusal was a bare `409 managed_postgres_conflict`.
3. **A capacity change fences every database.**
   - Each database row stores its backend's fingerprint, and `Registry.Resolve` refuses a row whose fingerprint differs from the configured backend's.
   - The fingerprint covers every non-secret setting, the capacity bounds `max_storage_bytes` and `max_restore_window_seconds` among them.
   - Raising the storage ceiling therefore made every existing database `backend_unavailable`: no bindings, deletes, resizes, restores or health checks, with no way back but the old value.

## Decision

1. **Two gates.**
   - `NewProvisioningGate` keeps deciding **new databases**: `Create`, and the snapshot copy paths of environment clones.
   - The new `NewOperationsGate` decides **work on databases that exist**:
     - restores (`RestoreWithResult`, `AdmitRestoreReservation`) and database switches;
     - resize;
     - finishing a reserved database (`Reconcile`, and the reconcilers' provisioning rows);
     - every binding operation, cutovers included.
   - In production the operations gate is the operator's `provisioning_enabled` switch alone. That switch stays the full stop.
   - The account release (the `managed-postgres` capability flag) still applies to both gates.
   - Spend stays bounded where it already was: a restore still passes usage admission, the fleet ceilings and the plan's database count.
   - Outside production both gates are the staging qualification gate, as before.
   - `ServiceOptions.OperationsEnabled` carries the gate and defaults to `ProvisioningEnabled`.
2. **A source with restore copies can be deleted.**
   - Copies no longer refuse a delete claim. Bindings and environment-clone holds still do, and the refusal now says which.
   - The customer's delete keeps its undo window as usual.
   - When the purge runs and an undeleted copy still exists, the source is kept:
     - its provider resource is not touched;
     - it is held (endpoint disabled) if it was not already;
     - it waits in `deleting` with `last_error_code = restore_copies_remain`, retrying hourly;
     - once the last copy is deleted, the next retry deletes it at the provider and the row becomes `deleted`.
   - The trigger still refuses `deleted` while copies remain.
   - Nested copies wait the same way: a copy with its own copy is a source too.
   - Its usage keeps being metered: the usage collector meters every database with a provider resource, whatever its state, and the copies' usage is recorded against the source.
3. **A delete that can no longer be undone frees the database.**
   - The plan's database count and the restore slot's room no longer count a database in `deleting` with no undo window (`purge_after IS NULL`). This covers a kept source, and a database whose provider delete is still in flight.
   - A database still in its undo window keeps counting, because the owner can take it back.
   - `Database.CountsTowardLimit` is the rule. `CountManagedPostgresLifecycleDatabases` and `CountManagedPostgresOrgDatabases` implement it in SQL.
   - A kept source's name stays taken until it is deleted.
4. **Superseded capacity settings.**
   - A backend may list `previous_settings`, the settings it had before a capacity change. `Resolve` accepts a row whose fingerprint is the backend's current one or one of these.
   - An entry may differ from `settings` only in `max_storage_bytes` and `max_restore_window_seconds`. Any other difference is placement (organization, region, database name) and fails startup, so a backend can never be silently repointed.
   - At most 16 entries.
   - `Resolve` hands back the current backend, so the current capabilities apply: lowering a bound still refuses the work of a database whose spec exceeds it.
   - The current fingerprint still changes with a capacity change. A production approval and the qualification artifacts pin it, so new databases wait for a fresh approval while existing ones keep working (decision 1).

## Consequences

- An approval that lapses, or awaits renewal after a backend change, stops new databases only. The `FaasManagedPostgresProductionApproval*` alerts still fire.
- After a rollback that restores the database, the owner can delete the old database at once. It stops counting when its undo window ends, and disappears when the copy does.
- A kept source holds storage at the provider until its copies are deleted. That storage stays in the owner's metered pool, as it was before.
- An operator raising a capacity bound adds the old settings to `previous_settings` in the same change, then renews the production approval for the new fingerprint.

## Delivery

One PR: the gates, the delete path and counts, `previous_settings`, tests (memory store, PostgreSQL store, registry), and docs (`docs/managed-postgres.md`, `docs/postgres.md`, `docs/STATUS.md`).
