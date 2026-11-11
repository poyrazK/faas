# ADR-20261106103654: Managed PostgreSQL branches: restore from latest, with an expiry

- **Status:** accepted
- **Date:** 2026-11-06
- **Owner:** apid (managed PostgreSQL) maintainers
- **Amends:** [ADR-20261031123259](20261031123259-managed-postgres-restore-slot.md) (restore slot: a branch is a restore and follows its quota rules unchanged)
- **Context:**
  - Customers testing migrations or destructive jobs want a throwaway copy of a live database: create it in seconds, run the suite, drop it. Today this needs `gregale postgres restore` with a hand-picked RFC3339 timestamp, and nothing removes the copy if CI never reaches its cleanup step.
  - On Neon a restore is already a copy-on-write branch in the source's project (ADR-20261031123259, context). It shares the source's pages until written to, Neon bills only the data written to it, and it has its own scale-to-zero endpoint. The provider path, lineage (`restore_source_database_id`), quota reservation, binding revocation and purge all exist.
  - What is missing is a "latest" point (no timestamp to compute, no client clock in the request) and an end date for copies made by automation.
  - Connection pooling needs no change: `read_write`, `read_only` and `data_api` URLs already go through the transaction pooler, and `migration` URLs connect directly (`docs/postgres.md`, "Connect an app").
- **Decision:**
  1. **Restore from latest.** `POST /v1/postgres/databases/{id}/restore` (and the organization route) accepts a request without `point_in_time`.
     - apid picks the point: its own clock minus `managedpostgres.LatestRestoreLag` (2 seconds). The lag keeps the point in the past on the provider's clock, so a branch may miss the last two seconds of writes before the request.
     - The point is recorded as `restore_point_in_time`, like any restore, and every restore-window and creation-time check applies to it.
     - Repeating a latest restore under the same name adopts the existing copy when it came from the same source (and the same TTL), whatever point it was taken at. An explicit `point_in_time` keeps today's exact-match rule.
  2. **An expiry.** The request may carry `ttl_seconds`, from 300 (5 minutes) to 2,592,000 (30 days).
     - The database records `expires_at = created_at + ttl` in a new nullable column, and the view shows `expires_at` (`omitempty`).
     - At `expires_at` the lifecycle reconciler's branch sweep revokes the copy's bindings and purges it, as `gregale postgres delete --purge` does. There is no undo window: the copy was asked to end, and a scheduled delete would keep it on the plan's database count for 7 more days.
     - The source is never touched. A copy that has copies of its own is held in `deleting` until they are gone, as for any delete.
     - A copy whose delete the customer already scheduled is purged at its expiry too. A copy already being purged is left to that purge.
     - Each purge emits `managed_postgres.database.expired`.
     - Only restores take a TTL. It cannot be changed after creation.
  3. **Quota is unchanged** (superseded for short-lived branches by amendment 1). A branch is a restore: it counts toward the plan's database limit and may take the one restore slot (ADR-20261031123259). A branch holding the slot ends at the earlier of `restore_slot_until` and `expires_at`.
  4. **CLI.** `gregale postgres branch` is the customer's word for this:
     - `gregale postgres branch [create] <database> <name> [--from live|RFC3339] [--ttl DURATION]`: a restore, from latest unless `--from` gives a time.
     - `gregale postgres branch list <database>`: the undeleted databases restored from it.
     - `gregale postgres branch delete <database> <name> [--yes]`: purges the copy, after checking it was restored from `<database>`. It never deletes a database that is not a copy of `<database>`.
     - `create` is optional; a database named `list`, `delete` or `create` needs it.
     - The branch's name is its database name, unique in the account like every database name.
- **Why:**
  - It reuses the restore path end to end, with its lineage, quota, custody and receipt checks. No new provider capability is needed.
  - "Latest" chosen by apid avoids the client-clock failures a CLI-computed timestamp would have (a point in the future, or one before a new database existed).
  - The expiry keeps CI that fails before its cleanup from leaving copies that hold the plan's database count.
- **Consequences:**
  - One expand migration: nullable `expires_at timestamptz` on `managed_postgres_databases`, with a partial index for the sweep. An older apid ignores the column: it never expires a copy, and a newer apid expires it later.
  - Branches work only where the backend advertises point-in-time restore (Neon today). The native Firecracker backend (ADR-1027) gets them when it gains restore.
  - The owner decided the branch allowance on 2026-11-11 (amendment 1).
  - PR preview databases (ADR-20261102123523) landed separately; they keep their own lifecycle and do not use `expires_at`.

## Amendment 1 (2026-11-11): short-lived branches have their own allowance

- **Status:** accepted (owner decision, 2026-11-11). Supersedes decision 3 for short-lived branches.
- **Context:**
  - Decision 3 counted every branch toward the plan's database limit. A Pro account at 3 of 3 databases could therefore hold one branch at a time, through the restore slot, which defeats a branch per pull request or test run.
  - On Neon's Scale plan a branch lives in its source's project, and each project includes 25 branches; extra ones cost $1.50 a branch-month. A Gregale branch therefore costs us about nothing in branch fees. It costs its own compute ($0.222 per CU-hour while awake) and the data written to it ($0.35 per GB-month, plus restore history), and both are already metered against the account like any database.
- **Decision:**
  1. A branch whose TTL is at most `api.ManagedPostgresAllowanceBranchTTLMax` (7 days) sits in the plan's **branch allowance** instead of its database limit: `api.ManagedPostgresBranchesMax` is Free 0, Hobby 2, Pro 5, Scale 20 at once. Free includes no managed PostgreSQL, so its 0 changes nothing.
  2. Such a branch never takes the restore slot, and plan-excess ranking (ADR-20261102125208) leaves it out: it ends on its own within 7 days.
  3. Past the allowance the restore is refused with 403 `managed_postgres_branch_limit`, naming the allowance. An unknown plan, or apid without the allowance hook, has none: the allowance fails closed.
  4. A branch with a longer TTL, or none, is an ordinary restore under decision 3.
  5. The rule is a property of the row (`restore_source_database_id`, `created_at`, `expires_at`), read the same way by the Go stores and the SQL counts. The count runs under the owner's reservation lock, as the database count does. No migration is needed.
  6. A restore that must fit inside the database limit (a database switch's copy) cannot carry a TTL.
- **Not done:** a per-branch storage cap. A Neon branch shares its project's storage limit, so it cannot be capped apart from its source; copy previews (ADR-20261102123523) have the same property. The account's storage pool and the 7-day TTL bound a branch's storage instead.

