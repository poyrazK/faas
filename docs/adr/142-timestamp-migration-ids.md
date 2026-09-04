# ADR-142 · Timestamp migration IDs and out-of-order application

- **Status:** accepted
- **Date:** 2026-09-04
- **Decision:** freeze legacy versions 1–590 and assign all new migrations a
  UTC `YYYYMMDDHHMMSSmmm` ID; apply missing post-cutover migrations by ledger
  membership rather than global sequence position.
- **Why:** the contiguous five-digit namespace serialized unrelated pull
  requests. At cutover, 331 of 590 SQL files were no-op slot reservations,
  and every merged migration forced sibling PRs to renumber and rebase.

## Decision

`00001` through `00590` remain immutable, contiguous legacy history. Migration
`20260904000000000_timestamp_ids_cutover.sql` starts the timestamp namespace.
Authors create migrations with:

```sh
make migration-new NAME=add_job_priority
```

The generator uses UTC time with millisecond precision and creates the file
with exclusive-create semantics. Static CI rejects new five-digit migrations,
IDs in the gap between 590 and the timestamp cutover, malformed timestamps,
and duplicate IDs.

`db.MigrateUp` opts into Goose `WithAllowMissing()` only when every historical
gap is either a legacy no-op reservation or a post-cutover timestamp migration.
A missing real migration in 1–590 remains a hard failure. Fresh databases still
apply the complete migration set in numeric order.

Migration readiness and `migrate -status` compare the complete embedded ID set
with applied rows in `goose_db_version`. `MAX(version_id)` remains diagnostic
only: a high timestamp cannot prove that an earlier timestamp, merged later,
has been applied.

The cross-PR slot scanner and `FAAS_MIGRATION_ALLOWED_GAPS` CI exception are
retired. New migrations remain append-only and replay-safe. Independent expand
migrations may merge in either order; dependent migrations must use stacked PRs
or merge their dependency first.

## Consequences

- Independent schema PRs no longer rebase merely to rename migrations.
- Git conflicts remain only for genuinely overlapping SQL, `schema.sql`, or
  generated code changes.
- Production migration order can differ from fresh-install numeric order, so
  expand/backfill/contract compatibility is mandatory.
- Existing reservation files stay embedded forever as legacy history, but no
  new reservation files are created.
- ADR-041 is superseded for migrations after version 590.

## Rejected alternatives

- **Keep sequential slots and allocate them with GitHub automation:** still
  serializes authors and makes the merge service a schema bottleneck.
- **Reserve larger per-PR ranges:** preserves gaps and produces even more no-op
  migrations.
- **Enable out-of-order application for all history:** old migrations were not
  authored under the replay-safe contract; the legacy boundary keeps them
  fail-closed.
- **Renumber at merge time:** hides the work in a bot but still mutates every
  migration-bearing PR immediately before merge.
