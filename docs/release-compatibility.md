# Release compatibility: N±1, deploy order and the gates

Gregale releases each unit on its own: one daemon, the schema, or the guest
runtime. Releases are meant to land in about 30 minutes with no downtime.
That means two versions run side by side all the time: a release-N schedd
calls a release-N-1 vmmd, and a release-N host writes `app.json` for a
guest-init that a snapshot restored from an older release. This page sets the
rules that keep mixed versions working and describes the checks that enforce
them. The decision record is
[ADR 202610171900](adr/202610171900-release-compatibility-gates.md); it
implements the schema discipline, version skew and release safety parts of
ADR-1344 §6.

## The N±1 rule

**Every unit must work with units one release older or one release newer
than itself, in both directions.** Release N has to talk to N-1 peers,
read data that N-1 wrote, and write data that N-1 can read. The reverse also
holds, because a rollback puts N-1 back next to N.

Three consequences:

- **Removal takes two releases.** First, release N stops using the thing
  (the RPC, field, column, or JSON key) but keeps it. Then release N+1
  deletes it. The deletion is safe because the only other version still
  running is N, and N no longer uses it.
- **No unit may lag more than one release.** The CI checks compare against
  the previous release tag; they cannot see older binaries. Before release N
  rolls out anywhere, every unit has to be at N-1 or newer, including units
  that did not change. Enforcing this floor is the release pipeline's job.
- **The guest protocol is stricter: never remove a key.** A parked app's
  snapshot contains the guest-init that captured it, and a snapshot can be
  much older than N-1. Snapshots are only invalidated when the Firecracker
  version changes (ADR-005). Only retire a guest key after confirming that
  no live snapshot carries a guest-init that reads it. Record that check in
  the allowlist entry (below).

## Deploy order

Roll a release out in this order, and roll back in the reverse order:

1. **Data.** Schema migrations, expand step only. The new schema must serve
   the N-1 code that is still running everywhere, which is why migrations
   are expand/contract (below).
2. **Control plane.** apid, schedd, meterd, imaged, builderd, githubd,
   realtimed, outboundd. These units write the intent and instance rows the
   edge reads, and they must answer N-1 edge and node peers.
3. **Edge and node.** gatewayd-public, gatewayd-internal, vmmd and compute
   schedd, one node or cell at a time. These units serve traffic and call
   the control plane that was upgraded in step 2.
4. **Runtime.** guest-init, runner images and the base rootfs go last. A new
   guest may depend on a key the N host now writes. Older guests in
   snapshots keep working because of the additive-only guest rule.

A contract migration (a drop, or tightening a constraint) is the data step of
a *later* release, after every unit that read the old shape has moved past it.

## Gate 1: version skew (CI, every pull request)

`make version-skew-check` runs `scripts/ci/versionskew`, a Go tool. CI runs
it in the `light: codegen, policy, migrations` job.

It compares two contracts against the **previous release**, which is the
newest `v*` tag that is an ancestor of the pull request's base:

- **Protos** (`api/proto`, every daemon-to-daemon gRPC service). The tool
  compiles the protos at the release tag and at head with protoc, then
  compares the descriptor sets.
- **Guest protocol** (`pkg/api` `AppManifest` → `/etc/faas/app.json`,
  `BuildManifest` → `build.json`, `BuildDone` → `build-done.json`). The tool
  reads the Go structs' JSON shape at both refs without building either one.

A finding that the base already has is reported as pre-existing and does not
fail the check, so a single bad merge cannot turn later pull requests red.

| Contract | Fails on | Allowed |
|---|---|---|
| Proto service or RPC | removal, unless it was `option deprecated = true` in the previous release; a change to its request type, response type or streaming mode | new services and RPCs |
| Proto field | removal, unless it was `[deprecated = true]` in the previous release and its number and name are now `reserved`; a rename, `json_name` change, type change, singular/repeated change or oneof move; reuse of a number reserved in the previous release | new fields; adding or dropping proto3 `optional` |
| Proto enum value | removal without deprecation plus `reserved`; a rename (an `allow_alias` alias keeps the old name valid) | new values |
| Guest JSON key | removal or rename (a changed tag counts as both); a change of JSON shape (string, integer, object, array, map, base64 bytes, a type gaining a custom marshaler, the `,string` option) | a new key tagged `omitempty` or `omitzero` under an existing object; any key inside a new object; Go field or type renames that keep the JSON; pointer versus value; integer width |

**To remove an RPC or field:** in release N, mark it deprecated. The
generated Go code then carries `Deprecated:`, and staticcheck's SA1019 (in
`make lint`) flags every remaining use, so remove those uses. In release
N+1, delete it and add `reserved <number>; reserved "<name>";`.

**Exceptions** go in `scripts/ci/versionskew/allow.txt`, one finding key
per line followed by `# reason`. A typical case is a proto whose client and
server always ship in the same unit. An entry stops matching once a release
that contains the change is tagged, and the check then asks for the entry to
be deleted.

## Gate 2: migration expand/contract lint (CI, every pull request)

`make migration-lint` runs `scripts/ci/check_migration_expand_contract.py`
on every migration the change adds. It checks only the `-- +goose Up`
section. A table, view or index that does not exist in the base's
`schema.sql` is exempt: nothing old reads it and nothing waits on its lock.

| Rule | Fails on | Do instead |
|---|---|---|
| `drop-table`, `drop-column` | dropping a relation or column that the previous release tag (or this change) still mentions in non-test Go or SQL | stop reading it in one release, drop it in the next |
| `rename-table`, `rename-column` | renaming something the previous release still mentions | add the new name, write both, backfill, move readers, then drop the old name in a later release |
| `column-type` | `ALTER COLUMN … TYPE` | add a new column, backfill, move readers |
| `not-null` | `ADD COLUMN … NOT NULL` without a `DEFAULT`; `SET NOT NULL` without a `SET DEFAULT` in the same migration | add the column with a constant default, or nullable and backfilled; constrain it in a later release |
| `table-rewrite` | `ADD COLUMN` with a volatile default (`gen_random_uuid()`, `clock_timestamp()`, …) or a `STORED` generated column | add it without a default, backfill in batches, then set the default |
| `lock-timeout` | an `ALTER TABLE`, a non-concurrent `CREATE`/`DROP INDEX`, `CREATE`/`DROP TRIGGER`, `CREATE OR REPLACE VIEW`, `LOCK`, `REINDEX` or similar on an existing table, with no `lock_timeout` set before it; a `lock_timeout` of 0 or more than 10 s | start the Up section with `SET LOCAL lock_timeout = '5s';` |
| `index-build` | `CREATE INDEX`, `ADD PRIMARY KEY` or `ADD UNIQUE` on an existing table | `CREATE INDEX CONCURRENTLY` in a `-- +goose NO TRANSACTION` migration, then `ADD CONSTRAINT … USING INDEX` |
| `concurrently-in-transaction` | `CONCURRENTLY` without `-- +goose NO TRANSACTION` | add the directive |
| `compat-marker` | no `-- migration-compat:` class, or more than one; a migration marked `expand` that makes a contract change or waives `lock-timeout` or `concurrently-in-transaction` (cannot be waived) | declare the class; mark a contract change `contract` |

**Compatibility class.** Every new migration says, once, which kind it is
([ADR 20261018084150](adr/20261018084150-unit-lane-expand-migrations.md)):

```sql
-- migration-compat: expand
```

`expand` claims that the previous release runs correctly against the
schema the migration produces. A per-unit release may then apply it before
the next full release. `contract` means only a full release applies it.
The linter rejects an `expand` migration that does any of these:

- drops or renames anything that existed before, whether or not code
  references it;
- changes a column type;
- waives `not-null`;
- truncates a table;
- drops a function, type, sequence, trigger or unique constraint that it
  does not create again;
- drops a column default.

What no linter sees is the author's claim to make: a trigger function's
`pg_notify` payload, a new CHECK or foreign key, a backfill that changes a
value's meaning.

Why `lock_timeout` matters: without it, an `ALTER` queues behind one
long-running transaction, and every later query on that table queues behind
the `ALTER`. With a timeout, the migration fails fast instead. The daemon
that runs it (`pkg/db.MigrateUp`) fails to start, the unit's deploy fails,
and the old version keeps serving.

**Waivers** are a comment anywhere in the migration. Name the rule,
optionally the object, and give a reason; a waiver without a reason does not
count:

```sql
-- migration-lint: allow index-build events: 1 s write pause measured on 394k rows
-- migration-lint: allow drop-column apps.legacy_tier: only the rc.250 backfill wrote it; nothing reads it
```

The reference check is a word grep. It skips `migrations/`, every
`schema.sql`, sqlc `models.go`, tests, `testdata`, `cmd/e2e`, `scripts/` and
`docs/`. It cannot see SQL that is assembled at runtime, and a name that
appears only in a comment still counts. Waive that case and say so.

## Gate 3: post-deploy SLO gate (deploy workflows)

`scripts/ci/slo_gate.py` watches Prometheus for a set time after a unit
deploy. Its exit status tells the workflow whether to promote the unit or
roll it back. It needs only the Python standard library, so it runs on the
control-plane runner, where Prometheus listens on loopback.

```bash
python3 scripts/ci/slo_gate.py --unit schedd --deployed-at "$DEPLOYED_AT" --duration 10m --interval 15s
```

| Exit | Meaning | Workflow action |
|---|---|---|
| 0 | every selected rule held for `--duration` | promote |
| 1 | a new alert instance stayed true for its hold time, or the unit stopped being scraped | roll the unit back |
| 2 | usage or configuration error | fail the deploy |
| 3 | inconclusive: Prometheus did not answer, or a platform rule was missing, in error or stale for four rounds | treat as 1: no evidence, no promotion |

How it works:

- **The alert rules are the thresholds.** The gate reads
  `/api/v1/rules` from Prometheus. That response includes every rule's
  expression, its `for:`, its health and its active alerts with `activeAt`.
  The gate stores no copy of any threshold. It does not re-evaluate PromQL
  either, apart from one `up{job=…}` scrape check, so watching a deploy
  costs one request per interval on a saturated control plane.
- **Selected rules.** For every unit: the platform SLOs (5xx availability,
  wake latency p95, the 350 ms platform wake SLO, cold-boot fallback as the
  restore-failure signal, daemon down, not ready, restart loop, database
  pool starvation). In addition, every `page` or `warn` rule whose
  `component` label belongs to the unit. To see a unit's selection, run
  `--list`.
- **Breach.** An alert instance breaches the gate when it became active at
  or after `--deployed-at` and stays true for the rule's `for:`, capped by
  `--max-hold` (default 2 minutes), so a rule with a 10-minute `for:` can
  still stop a 10-minute gate. An instance that was already active before
  the deploy is logged and ignored, because rolling back would not fix it.
  `--deployed-at` is required: record `date +%s` right before the unit
  restarts. Prometheus's `activeAt` and the runner's clock come from the
  same host.
- **Units** are listed in `UNITS` in the script, each with its scrape jobs
  and alert components: every daemon, `migrations` (postgres alerts) and
  `runtime` (guest images, judged by the platform wake and restore SLOs). A
  unit not in the table needs `--job` and `--component`. A unit test checks
  that every named alert, component and job exists in
  `deploy/ansible/roles/prometheus`.

The per-unit release lanes
([ADR-1650](adr/1650-per-unit-control-plane-releases.md)) call this gate after
every host's deploy: once for the control-plane lane, then once per host in
the edge lane before the next host starts. Each lane's first host, its
canary, gets the full gate. Later hosts get a short follower gate. Both are
set in `scripts/ci/release_units.json` under `gates`. They run one gate per restarted
unit, in parallel on the control plane (Prometheus at `127.0.0.1:9095`),
through `scripts/ci/unit_deploy.sh gate`. Exit 1 or 3 from any of them rolls
the host's units back to the release that was active before
(`deployctl rollback --only`) and stops the run.
`scripts/ci/observe_rollout_availability.sh` keeps doing the black-box
probing during the control plane's activation itself.

## Deploy order and skew in the unit lanes

The unit lanes enforce the rules above in their workflows:

- **N±1.** A unit release is refused unless it is built over the newest
  full release tag before its commit, which is the release CI compared each
  merge against. Units at that base and units at the unit release's commit
  are therefore at most one release apart.
- **Order.** The data step rides the control-plane lane. A range whose
  migrations are all lane-eligible counts as eligible. Lane-eligible means:
  - each migration is marked `expand`;
  - it makes no contract change;
  - it has no finding left against the base release's schema;
  - it waives neither `lock-timeout` nor `concurrently-in-transaction`.

  The control-plane lane's `deployctl deploy` applies those migrations
  with the release commit's `bin/migrate`, under the advisory lock, before
  it restarts anything. A failed migration stops the lane before any
  restart and any SLO gate. Any other migration still blocks both lanes,
  so a contract migration ships in a full release.

  Control-plane host roles ride the same lane
  (docs/adr/20261018133100-host-configuration-lane.md). Before the data
  step, the lane converges the roles whose content hash changed since the
  host's record. It then restarts only the daemons whose start inputs
  changed, one at a time, and holds the SLO gate. A role change must
  work with the binaries the host runs, as an expand migration does: add
  a variable an older binary ignores, and never remove one the running
  binaries read in the same change.

  `release-units.yml` deploys the control-plane lane first, and the edge
  lane (`cd-edge-units.yml`) only after it. When the edge lane runs alone,
  it first checks that the control plane already runs that unit release's
  control-plane units and has applied its migrations. The runtime step
  (guest and images) always waits for the next full release. A rollback
  restores binaries, never the schema. The previous binaries run against an
  expand-only schema, and their startup migration ignores ledger versions
  they do not embed.

## Not covered yet

- guest-init decodes `job.json`, `app-task.json` and `execution.json` with
  `DisallowUnknownFields` (in `guest/init`), so an older guest rejects any
  key added on the host. Those types are not in the guest check yet, and
  they cannot accept additive changes until the guest tolerates unknown
  keys.
- ADR-1344 also asks for a CI job that runs the previous release's binaries
  against the new schema, and for capability negotiation between daemons.
  Neither is built.
- The full release pipeline still has to enforce that no unit lags more than
  one release. The unit lanes do enforce it.
