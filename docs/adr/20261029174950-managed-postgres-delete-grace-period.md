# ADR: Managed PostgreSQL delete grace period

Date: 2026-10-29
Status: Proposed
Related: [ADR-155](155-provider-neutral-managed-postgres.md) (provider-neutral managed PostgreSQL), [ADR-20261024150500](20261024150500-account-deletion-includes-managed-postgres.md) (account deletion includes managed PostgreSQL), [ADR-20261025094915](20261025094915-managed-postgres-compute-caps-on-running-databases.md) (compute caps and the storage pool)

## Context

`DELETE /v1/postgres/databases/{id}` deletes the provider resource at once:
for Neon, the project and its whole point-in-time restore history. Nothing can
bring it back. The CLI now asks for the database's name first (#5856), but the
API, the SDKs and every script still delete immediately. Deleted apps, by
contrast, can be restored for 30 days.

The GA review lists this as a data-safety blocker. The owner decided on
2026-10-29:

- a deleted database can be undone for **7 days**;
- it is **not billed** during those 7 days.

## Decision

1. **A customer delete is scheduled, not immediate.** The database moves to
   `deleting` and records `purge_after = now + 7 days`. The lifecycle
   reconciler already resumes `deleting` databases once `retry_at` passes, so
   `retry_at = purge_after` makes it delete the provider resource when the
   window ends, through the existing path.
   - The state stays `deleting`, a value released SDKs already accept. A new
     state would break their closed enum.
   - The database's view gains `restorable_until` (`omitempty`), the time the
     window ends.
   - The preconditions do not change. A database with bindings, restore
     descendants or environment clone holds still cannot be deleted, so no
     application holds credentials to it when the window starts.
   - The API returns 200 with the database, as now.

2. **Nothing runs during the window.** Scheduling asks the provider to hold
   the database through an optional `DeletionHoldProvider`. For Neon that is
   the primary endpoint's `disabled` flag. Neon schedules a suspend and
   restricts connections, and no connection or console action can re-enable
   the endpoint. Neon's own availability checks still start it briefly now
   and then; that usage is waived with the rest (decision 6).
   - The hold is applied before the delete is accepted. If the provider
     cannot apply it, the request fails with 503 and the database stays
     ready.
   - A provider without the capability skips the hold. Bindings are already
     gone, so nothing connects, and a scale-to-zero compute suspends on its
     own.

3. **Undo.** `POST /v1/postgres/databases/{id}/undelete` (CLI
   `gregale postgres undelete <database>`) works while `restorable_until` is in
   the future.
   - It releases the provider hold, waits for the provider to report the
     database ready, and returns it to `ready` with its spec, data and restore
     history.
   - The plan check that applies to creates applies here too. A database
     whose spec the current plan no longer allows (after a downgrade) is
     refused with the usual 403.
   - After the window, the database is gone and undo returns 404.

4. **Purge now.** `DELETE …?purge=true` (CLI `--purge`) deletes at once, as
   today, on a database that is ready or already scheduled. The CLI asks for
   the name and says it cannot be undone.

5. **The database still counts toward the plan's database limit** until it is
   purged. Otherwise create-and-delete loops could hold several databases'
   storage for a week each on a one-database plan. The CLI's delete message
   says so and names `--purge`. The fleet database cap counts it as well,
   because the provider still stores it.

6. **Not billed.** Usage the collector records for a database while its
   delete is scheduled is marked waived.
   - Waived usage does not count toward the customer's usage view, compute
     cap, storage pool, admission ceilings, or any future invoice.
   - The fleet spend breaker still counts it, because the provider charges
     Gregale for it.
   - Undo stops the waiver from the moment the database is ready again.

7. **Internal deletes stay immediate.** Account erasure (ADR-20261024150500,
   which has its own 30-day grace), environment clone cleanup, database
   copies and dev databases call the immediate delete, as now. Only the
   customer's API delete schedules.

8. **Names stay taken during the window.** Undo needs the name, so a new
   database cannot reuse it until the old one is purged. The conflict error
   says the name belongs to a database scheduled for deletion and names
   undelete and `--purge`.

## Delivery

1. This record.
2. Schedule and undo:
   - the `purge_after` column (expand-only);
   - `Service.ScheduleDelete` and `Service.Undelete`, the Neon hold, the API
     routes and `?purge=true`;
   - `restorable_until` in OpenAPI and the SDKs;
   - routes under the `managed-postgres` capability.
3. The usage waiver (decision 6).
4. The CLI (`undelete`, `--purge`, the delete message), the dashboard and the
   docs.

Step 2 ships the window before step 3 waives its usage. Until step 3, a
scheduled database's storage keeps counting toward its subject's usage for up
to 7 days. Managed PostgreSQL has no invoicing, so nothing is charged either
way.

## Release compatibility

- `purge_after` is a new nullable column. An older apid ignores it and treats
  a scheduled database as an ordinary `deleting` one whose retry time is a
  week away: it neither deletes early nor undoes.
- An older apid's immediate delete still works on a scheduled database, as
  purge.
- `restorable_until` is `omitempty`, so older clients ignore it.

## Consequences

- A mistaken delete, from a script, the API or another tool, can be undone
  for a week. The point-in-time restore history survives the window.
- Gregale pays the provider's storage for scheduled databases for up to 7
  days.
- A one-database plan cannot replace a deleted database without undoing it or
  purging it first.

## Rejected alternatives

- **A new `pending_deletion` state.** Released SDKs validate `state` against a
  closed set.
- **Do not count scheduled databases toward the plan limit.** Create-and-delete
  loops would hold unbilled storage.
- **Suspend through the compute quota.** Undo would have to restore a quota
  that may never have been set, and the cap sweep rewrites quotas. The
  endpoint's `disabled` flag is reversible exactly.
