# migrations/ — goose, timestamped, append-only (spec §5)

Never edit a merged migration. Schema authored in spec §5; sqlc generates typed
queries against it. Every state column carries a CHECK constraint.

## Creating a migration

Use the generator; do not choose a version manually:

```sh
make migration-new NAME=add_job_priority
```

It creates `YYYYMMDDHHMMSSmmm_name.sql` using UTC time with millisecond
precision. Timestamp IDs let independent PRs merge in either order. The
migration runner compares the complete embedded migration set with
`goose_db_version` and applies every missing post-cutover migration, including
one whose numeric ID is lower than a migration already deployed.

Versions `00001` through `00590` are the frozen legacy namespace. They remain
contiguous and strict. Never add another five-digit migration or a
`reserve_slot` file; ADR-142 supersedes ADR-041 for all new work.

## Authoring contract

- Prefer additive expand migrations. Backfill separately, deploy compatible
  code, and contract only after every old binary is gone.
- New migrations must be replay-safe; CI removes their ledger rows and runs
  them again against the already-expanded schema.
- If migration B depends on migration A, stack the PRs or merge A first.
  Timestamp IDs remove coordination for independent migrations; they do not
  turn incompatible DDL into compatible DDL.
- Regenerate `schema.sql` and sqlc output when the schema shape changes.

See ADR-142 for the cutover and runtime safety rules.
