# ADR-20261031123259: A restore may hold one database over the plan's limit

- **Status:** accepted (owner, 2026-10-31: a copy whose slot ends with no room has its delete scheduled; suspension rejected)
- **Date:** 2026-10-31
- **Owner:** apid (managed PostgreSQL) maintainers
- **Amends:** ADR-20261031091607 (managed PostgreSQL GA, PR #6126: open item 1 and `postgres-restore`'s plans), [ADR-20261025094915](20261025094915-managed-postgres-compute-caps-on-running-databases.md) decision 9 (the storage pool), [ADR-20261029174950](20261029174950-managed-postgres-delete-grace-period.md) (undelete)
- **Context:**
  - A point-in-time restore creates a new database, and that database counts toward the plan's database limit. Free and Hobby allow one database (ADR-1430), so they cannot restore at all. Pro at three of three cannot either.
  - Restore is how a customer recovers from their own mistake, such as a bad migration or a deleted table. Without it, the 7-day undo of a delete is their only recovery path, and it cannot help with data that is still there but wrong.
  - On Neon, a restore is a branch in the source's project, not a new project. Until it is written to, it shares its source's pages, and Neon bills only the data written to it. It has its own compute endpoint.
  - The source cannot be deleted while a restore of it exists. Neon cannot delete a branch that has children.
- **Decision:**
  1. **The restore slot.**
     - A restore may reserve one database past the plan's database limit. The new constant is `api.ManagedPostgresRestoreSlots = 1` in `pkg/api/limits.go`.
     - The reservation keeps today's count and lock (undeleted databases of the owner, under the owner's reservation lock). A restore is admitted while the count is below `limit + 1` and no other database of the owner holds the slot.
     - Creates, environment clones and snapshot copies never use the slot.
     - The rule is the same on every plan, and for organization databases it applies per organization.
     - A restore admitted inside the limit does not take the slot.
  2. **The slot lasts 7 days.**
     - The database that took it records `restore_slot_until = created_at + 7 days` (`api.ManagedPostgresRestoreSlotWindow`).
     - The view shows `restore_slot_until` (`omitempty`) while the slot is held.
     - `gregale postgres get` and `list` say the copy is temporary, until when, and that purging it or upgrading keeps the account inside its limit.
  3. **The slot ends early when the account has room.**
     - If another of the owner's databases is purged, or a plan upgrade raises the limit, the count without the slot database fits the limit. The database then becomes an ordinary one and `restore_slot_until` is cleared.
     - The slot is checked at every reservation, purge and plan change, and by the lifecycle reconciler.
     - A scheduled delete does not free room: a database in its undo window still counts (ADR-20261029174950).
  4. **When the slot ends with no room, the copy's delete is scheduled.**
     - At `restore_slot_until`, the reconciler schedules the database's delete exactly as a customer delete. The state becomes `deleting`, `restorable_until` is 7 days later, and the provider hold stops its compute.
     - The account gets a `managed_postgres.database.restore_slot_expired` audit event, and an email (Delivery 2). The account that ran the restore is also emailed 48 hours before the slot ends while it still has no room (`api.ManagedPostgresRestoreSlotNoticeLead`).
     - The source is never touched, so the only data the delete can lose is what was written to the copy after the restore.
  5. **Undo and a second restore.**
     - Recovering a copy whose slot expired needs room under the plan's limit, like any undelete. The slot is not granted again, so it cannot extend itself.
     - A second restore while a copy holds the slot, its delete scheduled or not, is refused with `403 managed_postgres_restore_slot_in_use`, naming the copy to purge.
  6. **The storage pool leaves the slot database out of the sum.**
     - Its logical size is mostly its source's pages. Counting it would let restoring a nearly full database block writes to the live one.
     - Neon's storage limit is per branch but set for the whole project, so the copy still cannot grow past the project's water level, which is at most its spec.
     - Compute counts as for any database, toward the pooled monthly allowance (the cap enforcer is unchanged). The fleet ceilings count it as they count every database.
  7. **Catalog.** `postgres-restore` lists every plan. The customer guide explains the slot and the way to keep a restored row:
     - copy it back into the source, then purge the copy; or
     - upgrade so both fit.
- **Delivery (one PR):**
  1. **Code and schema:**
     - `pkg/api/limits.go` constants;
     - an expand migration adding nullable `restore_slot_until timestamptz` to `managed_postgres_databases`, with its clone-registry entry;
     - reservation in `PostgresStore` and `MemoryStore` (the contract test covers both);
     - the view field in OpenAPI and the SDKs;
     - CLI output, the catalog plans and the docs.
  2. **Lifecycle:** the slot release and expiry in the lifecycle reconciler, through `ScheduleDelete` with a system actor; the recover rule; the audit event. The email followed in its own change: template `managed_postgres_restore_slot`, migration 20261101085949320 (notices table, template CHECK lists widened), behind `FAAS_INTERNAL_MAIL_MANAGED_POSTGRES_RESTORE_SLOT_OUTBOX_ENABLED` (off).
  3. **Storage pool:** the exclusion in the storage pool's sum.
  4. **Journey:** `TestManagedPostgres`'s restore step stops skipping on one-database plans and checks `restore_slot_until`.
- **Why:** Free and Hobby customers should be able to recover from their own mistakes without upgrading first. A short-lived copy gives them that, while the plan's database count stays what the financial model sells.
- **Consequences:**
  - An account holds at most `limit + 1` undeleted databases. The extra one is a restore for at most 7 days, then up to 7 more in its undo window.
  - Provider cost per slot is one scale-to-zero endpoint inside the pooled compute allowance, plus the data written to the copy.
  - Keeping the restored copy and dropping the source is not possible on Neon. That needs a restore into the source itself (Neon restores a branch from another branch). This is the follow-up "restore in place", and it would also let one-database plans skip the copy.
- **Rejected alternatives:**
  - **Raise the database limit for Free and Hobby.** That would be a permanent second database the financial model does not sell.
  - **Leave restored databases out of the count.** Unbounded restores would make unbounded databases.
  - **Suspend the copy at expiry instead of scheduling its delete.** That keeps the data forever, but the account would stay over its limit with no way back except an upgrade or a manual purge. It also needs a held-but-ready state that released SDKs' closed `state` enum cannot express. A scheduled delete reuses the undo window customers already know.
  - **Restore in place first.** It is the better product, but it touches bindings, credentials, cutover and usage accounting. The slot fixes the gap now and stays useful after in-place restore exists.
