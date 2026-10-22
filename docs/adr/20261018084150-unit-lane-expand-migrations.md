# ADR 20261018084150 · Expand-only migrations in per-unit releases

- **Status:** proposed
- **Date:** 2026-10-18
- **Related:** ADR-1650 (per-unit releases), ADR 202610171900 (release
  compatibility gates), ADR-124 (migration advisory lock), ADR-142
  (timestamp migration versions), ADR-1344 §6 (schema discipline)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A per-unit release (ADR-1650) ships the daemons that changed since the full
release the hosts run, without a tag, a drain or Ansible. Until now any
migration since that base closed both unit lanes: 18% of merges carry one.
Unit releases are cumulative, so one such merge closes the lane until the
next full release (55-75 minutes, with node drains). After each of the last
15 tags the lane stayed open for a median of 1 merge. ADR-1650 lists this as
its first open item: run expand-only migrations ahead of restarts, behind a
machine-checkable compatibility marker.

Before deciding where the lane applies migrations, we checked who applies
them today and whether an older binary tolerates a newer schema:

- **The full release.** cd-controlplane builds `bin/migrate` (cmd/migrate)
  from the tag as a controller-local tool. `deployctl deploy` runs it
  (`hostRuntime.Migrate`: `su - faas`, the host's `DATABASE_URL`) after
  preflight and before it installs units, publishes the release pointer or
  restarts anything (`pkg/deploycontroller.DeployOver`). cmd/migrate calls
  `db.MigrateUp`, which holds the session advisory lock `MigrationLockKey`
  (ADR-124) and lets a timestamp migration arrive after a higher one
  (ADR-142).
- **The unit lane already runs that step.** It calls `deployctl deploy
  --only` on the control plane, so it runs the overlay's `bin/migrate`. Until
  now that was a hard link to the base release's binary, so the step was a
  no-op.
- **Daemons migrate at startup.** apid, meterd, schedd, builderd and imaged
  call `db.MigrateUp` before they serve.
- **Nothing compares the live schema with the binary at startup.** The
  canonical schema binding (`schema.sql` and `schema.sql.migrations.sha256`,
  PR #4238) is read only by the operator's ledger-recovery commands
  (`migrate -prepare-ledger-recovery`, `-ledger-recovery-plan`,
  `-ledger-recovery-apply`). They refuse a schema that does not match the
  binary, which fails closed. Run them from the newest release.
- **An older binary's MigrateUp is a no-op on a newer ledger.** goose
  v3.27.2 (`gooseutil.UpVersions`) applies only versions it embeds and
  ignores ledger versions it does not. Our `historicalMigrationOption`
  finds no gap when the ledger is ahead of the binary. Two tests pin this:
  a reconcile table case, and a Postgres test that adds a ledger version the
  binary does not embed, then runs MigrateUp.
- **`migrate -wait-for-migrations`** waits until every embedded version is
  applied. A ledger that has more versions satisfies it.

## Decision

### 1. The compatibility class (the marker)

Every new migration declares its class once, anywhere in the file:

```sql
-- migration-compat: expand     -- the previous release runs correctly against the new schema
-- migration-compat: contract   -- only a full release may apply it
```

The migration linter (`scripts/ci/check_migration_expand_contract.py`,
light CI) requires exactly one declaration with one of the two values. It
also checks that a migration marked expand contains none of the following
contract changes, whether or not code references the object:

- a drop or rename of a table, view or column that existed before;
- a column type change;
- a waived `not-null`;
- `TRUNCATE`;
- a dropped function, procedure, type, domain, sequence, schema, extension
  or policy, unless the same migration creates the routine again;
- a dropped trigger on an existing table that the migration does not
  create again;
- a dropped unique index or unique, primary key or exclusion constraint:
  these are the previous release's `ON CONFLICT` targets;
- a dropped column default;
- a rename of a constraint, type, function, sequence or index.

A migration marked expand also may not waive `lock-timeout` or
`concurrently-in-transaction`. These findings belong to a new rule,
`compat-marker`, which cannot be waived.

The marker is opt-in by design: a migration without it fails the lint, and
a migration marked contract goes through a full release. The author and the
reviewer still answer for what no linter sees. A trigger function may
change a `pg_notify` payload that the previous release decodes. A new CHECK
or foreign key may reject rows that previous writers produce. A backfill may
change what a value means. A view may change its columns. Marking a
migration expand is the claim that none of these break the previous
release.

### 2. Lane eligibility

`check_migration_expand_contract.classify_range(base, head)` judges every
migration added in `(base, head]`. A migration is lane-eligible when all of
these hold:

1. It declares `expand`.
2. The linter, run against the base tag's `schema.sql`, leaves no finding
   after waivers. The catalogue also counts objects that the range's other
   migrations create, because an earlier unit release may already have
   applied them and run code that reads them.
3. It makes no contract change (section 1), waived or not.
4. It waives neither `lock-timeout` nor `concurrently-in-transaction`.

**Waivers.** A waived finding of the following kinds keeps a migration
lane-eligible: `index-build` and `table-rewrite`. Each records a measured,
bounded lock cost that the pull request's reviewer accepted. The migration
is still expand-only, and it runs the same way in either lane: through the
same runner, before any restart, while the old binaries serve. The verdict
records these waivers in the signed manifest. Every other waiver closes
the lane:

- A **contract waiver** (`drop-*`, `rename-*`, `column-type`, `not-null`)
  is a reviewer's reading of a grep, about binaries that keep running until
  the next full release. That judgment belongs on a full release, which is
  tagged by a person, gated by the mega tier and checked by acceptance.
- A **lock-timeout or concurrently-in-transaction waiver.** The lane
  applies migrations on every merge with nobody watching. `lock_timeout` is
  what makes a blocked `ALTER` fail within 10 seconds instead of queueing
  every query on the table. A `CONCURRENTLY` statement inside a transaction
  does not run at all.

A range is eligible when every migration added in it is eligible, no
issued migration was modified or deleted, and no non-migration file under
`migrations/` (the runner's Go code) changed. Otherwise the unit lanes stay
closed, as before.

### 3. Where and how the lane applies them

The lane reuses the full release's runner. It adds no migration runner of
its own.

1. `affected_units.py` adds a **data step** to its report. When the range's
   migrations are eligible, they stop blocking. The report carries each
   migration's verdict and a data version, `data-<newest commit that
   changed a migration>`.
2. `release-units.yml` builds `tools/migrate` at the release commit, with the
   flags the full lane uses. The tool goes into the signed tarball, and the
   verdicts go into `units-manifest.json` under `data`. That file is covered
   by the same signature.
3. `verify_unit_release.sh` refuses a release that carries migrations
   without an eligible verdict and the migrate tool.
4. The control-plane plan (`plan_unit_deploy.py`) runs on the control
   plane. When the host's recorded data version differs from the release's,
   the overlay replaces `bin/migrate` with `tools/migrate`. `deployctl deploy`
   then runs it before activation, pointer publication and every restart.
   The step runs under the host's deploy lock and the Postgres advisory
   lock, and each migration's own `SET LOCAL lock_timeout` bounds its lock
   waits.
5. **Failure.** If a migration fails, deployctl returns before activating
   anything. The activation step fails, nothing restarts, the SLO gate does
   not run, and the run fails. Migrations that committed before the failure
   stay applied. Each one is expand-only, so the binaries still running are
   unaffected, and a rerun continues from there (goose).
6. **SLO gate.** It adds `--unit migrations` (the postgres alert component)
   to the units it holds.
7. **Bookkeeping.** `unit-versions.json` records the applied
   `data_version`, and every later overlay carries it forward.
8. **Applied once.** Only the control-plane lane on the control plane
   replaces `bin/migrate`. On the control plane, the edge lane's overlay
   inherits the binary as a hard link, so its deploy step is a no-op.
   Compute hosts never migrate (`hostRuntime.Migrate` skips
   host-managed releases).

The next full release runs its own `bin/migrate`. goose skips every version
already applied. A migration merged later with an older timestamp still
goes in, through ADR-142's out-of-order rule.

### 4. Deploy order

The order stays data → control plane → edge/node → runtime, and rollback
goes in reverse.

- **Data before the control plane.** The data step runs inside the
  control-plane lane's `deployctl deploy`, before it restarts anything. That
  lane deploys even when no control-plane daemon changed. In practice one
  always does, because every daemon embeds the migrations through `pkg/db`.
- **Control plane before the edge.** In `affected_units.py`, anything that
  blocks the control-plane lane also blocks the edge lane. Before any
  gateway changes, `cd-edge-units.yml` plans the control-plane lane against
  the control plane's state. A plan that would still apply migrations
  counts as "not on the control plane yet". On the control plane, the
  planner also refuses the edge lane until the data version is recorded.
- **Runtime last.** Guest and image changes still wait for a full release.
- **Rollback in reverse.** Binaries roll back (`deployctl rollback
  --only`), and the schema stays. No down migration runs: it would discard
  data written since, and expand-only migrations make it unnecessary.

### 5. Why rollback stays safe

These binaries run against the new schema:

- the base release's binaries on compute (schedd, vmmd, realtimed,
  builderd and the gateways until the edge lane reaches them);
- earlier unit overlays;
- after a rollback, the previous release's control-plane daemons.

They stay correct for these reasons:

- **Expand-only.** Nothing they read is dropped, renamed or retyped. Nothing
  they write becomes newly required: no `NOT NULL` without a default, no
  dropped default, no dropped `ON CONFLICT` target. No rows are truncated.
- **Startup migration is a no-op.** A rolled-back apid or meterd runs
  `MigrateUp` at startup against a ledger that records versions it does not
  embed. That is a no-op (see Context; tested).
- **No startup schema check.** No daemon compares the live schema with its
  build. The ledger-recovery commands refuse a mismatch, which fails
  closed.
- **Lag is bounded.** The N-1 binaries are exactly the base release's
  binaries, which the linter's verdict was computed against. The N±1 lag
  floor (ADR-1650) keeps every host on that base or newer.

## Consequences

- Every new migration needs one more line. A branch that adds migrations
  fails the light-tier lint until each one declares its class; the message
  names the line to add.
- A range whose migrations are all expand keeps the unit lane open. On
  history, few migrations would need `contract`. Since rc.240, the
  machine-checked contract changes listed in section 1 appear in 20 of 394
  migrations, about 5%. The remaining misses before this change were
  missing `lock_timeout`s, which the linter has required of new migrations
  since PR #4875.
- **Simulated on the last 15 tags.** The simulation replayed each tag's
  merges by path and treated each migration as expand unless it makes a
  contract change. The lane stays open for a median of 3 merges (range
  0-10), up from 1 (range 0-5). Changes under `deploy/` now close it first
  in 12 of the 15 cases, so they are the next blocker.
- Migrations reach production between full releases. The next full
  release finds them applied. Through the life of a unit lane, compute
  daemons run at the base version against a newer schema, which N±1 already
  allows.
- The unit lane can now change the database. A bad expand migration can
  hurt the database even when the previous binaries cope with it, for
  example a long data backfill. The SLO gate watches the postgres alerts,
  but the schema does not roll back. A risky backfill belongs in its own
  migration marked `contract`, or in a job.
- **Evidence:**
  - Hermetic tests cover the classification, the range and ordering rules,
    the plan, the stage and the verifier.
  - A Postgres test runs MigrateUp against a newer ledger, on a scratch
    instance.
  - Not yet run on a real host. The first data step should run on fsn-4 or
    a staging control plane with the owner's OK.

## Rejected alternatives

- **A separate migration job in the unit workflow.** That would be a
  second runner, with its own credentials and its own failure handling,
  outside the deploy lock. `deployctl deploy` already runs the full lane's
  step at the right point.
- **Letting apid's startup MigrateUp apply them.** The migration would run
  after the restart instead of before it. A failure would crash the new
  apid instead of stopping the lane, and every migrating daemon would race
  for the lock.
- **Eligibility from the lint alone, with no marker.** The linter cannot
  see triggers, `pg_notify` payloads, new constraints or backfills. An
  opt-out marker has the same flaw: a forgotten line would send such a
  migration through an unattended lane.
- **Counting every waived finding as eligible.** A contract waiver is a
  claim about binaries the lane keeps running for hours. The lane would
  turn a reviewer's grep into an unattended deploy.
- **Down migrations on rollback.** They discard data written since the
  migration, and the expand-only rule makes them unnecessary.
