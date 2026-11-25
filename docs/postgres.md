# Managed PostgreSQL

Managed PostgreSQL gives an app its own PostgreSQL database. Gregale creates
the database, delivers its connection URL to your app as a secret, keeps a
point-in-time restore window, and stops the compute while the database is idle.

Every plan includes managed PostgreSQL. To see what your plan and a region
support, run:

```sh
gregale postgres capabilities --region eu-central-1
```

Operators: see the [operator guide](managed-postgres.md).

## Plans

| Plan | Databases | Pooled storage | Compute per month | Included restore history | Restore window | Classes |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| Free | — | — | — | — | — | — |
| Hobby | 1 | 1 GiB | 25 CU-hours | 1 GiB | 7 days | development |
| Pro | 3 | 5 GiB | 75 CU-hours | 5 GiB | 7 days | development, burstable |
| Scale | 10 | 20 GiB | 200 CU-hours | 20 GiB | 7 days | development, burstable, production |

- The Free plan includes no managed PostgreSQL: create, `gregale dev
  --postgres`, a Compose `postgres` service and restores answer
  `403 managed_postgres_not_in_plan` there. Upgrade to Hobby or above for a
  database.
- Storage is pooled across your databases: one database may use the whole
  pool.
- Compute suspends when a database is idle, by default after 2 minutes
  without activity. Every wake bills at least that long, so a shorter time
  costs less and a longer one avoids more cold starts. Choose it per database
  from 1 minute to 1 hour:

  ```sh
  gregale postgres suspend-timeout set orders-db 5m
  gregale postgres suspend-timeout set orders-db default
  ```

  A suspended database takes the new time at once; a running one takes it
  the next time it suspends, without dropping connections. `gregale postgres
  get` shows the current value. Always-on compute is not part of these plans.
- Restore history (what point-in-time restore keeps) and snapshots are
  included up to the size of the storage pool, averaged over the month.
- Without overage, storage is a limit and nothing past the plan is billed.
  Compute, storage, restore history and egress past the plan are billed only
  when you turn on overage; see [Usage and limits](#usage-and-limits).
- A database runs in a single zone. Gregale makes no high-availability
  promise for it; point-in-time restore and the 7-day undo of a delete are the
  recovery paths.
- A plan downgrade waits until your databases fit the new plan: the refusal
  lists the databases to delete and the ones to move to a class the plan
  includes. A database whose delete is scheduled no longer counts.
- A plan can also drop without that check, for example when a subscription
  ends. Databases the new plan no longer includes are then suspended:
  - your oldest databases are kept, up to the plan's database limit;
  - a newer one shows `plan_excess: database_limit`;
  - one in a class the plan does not include shows
    `plan_excess: service_class`.

  `get`, `list` and the dashboard show it, and you get an email. Its data,
  credentials and app bindings are kept, but it does not start until you
  upgrade, delete a database, or resize it to an included class. The next
  usage sweep, within minutes, resumes it.
  - A database still suspended 30 days later is deleted. The email names the
    date, and a reminder follows a week before it. Its app bindings are
    removed, and `gregale postgres recover` brings it back for 7 days after
    that.
- A subscription that ends drops the account to Free, which includes no
  databases, so every database it holds is suspended this way.

## Create a database

```sh
gregale postgres create orders --region eu-central-1
gregale postgres get orders
```

The database starts in `provisioning` and moves to `ready`. Options:

- `--class development|burstable|production`: the compute class your plan
  allows. The default is `development`.
- `--postgres-major N`: the PostgreSQL major version, 15 to 18. The default is
  16. PostgreSQL 14 reached its end of life in November 2026 and is not
  supported.
- `--storage-bytes N` and `--restore-window-seconds N`: default to your plan's
  allowance.

To create a database and attach it to an app in one step:

```sh
gregale add postgres --app my-api --region eu-central-1
gregale add postgres --database orders --app my-api --env production
```

`add postgres` reuses a matching database when there is one. Otherwise it
creates one named `<app>-postgres`, waits until it is ready, and binds it to
the app as `DATABASE_URL`. `--database` attaches an existing database by name
or ID. `--region` is needed only when a database is created.

## Connect an app

To connect an app to Postgres, attach the database to it. Gregale delivers
the connection URL as an app secret:

```sh
gregale postgres attach orders my-api --env DATABASE_URL
```

- `--scope` picks the environment. It defaults to the linked project's
  environment.
- The app receives the secret once the binding is `ready`. Each binding has
  its own login, so detaching one app never affects another.
- A bound app can reach its database on every plan. Other destinations on port
  5432 stay closed unless the app declares that egress port (Pro and Scale).

> **Schema changes need a second binding.** The default `read_write` access
> can read and write rows in `public` but cannot `CREATE`, `ALTER` or `DROP`
> anything, so a migration run at app startup fails with `permission denied`.
> Attach the database again with `--access migration --env MIGRATION_DATABASE_URL`
> and run your migration tool as a release task pointed at that URL. Serving
> instances never receive it. The access levels are listed under
> [Connect from your laptop](#connect-from-your-laptop).

### Declare the database in gregale.yaml

Instead of `gregale postgres attach`, you can declare the database in
`gregale.yaml`, and `gregale deploy` binds it before it starts the new
compute:

```yaml
databases:
  - database: orders       # name or ID
    scope: production      # defaults to default
    env: DATABASE_URL      # defaults to DATABASE_URL
    access: read_write
```

- A binding that is still provisioning blocks the deploy with an error you can
  retry.
- In a project with several apps, give each entry an `app`.
- All of one deployment's database entries use the same scope.

Add `create` to have the deploy create the database when none with that name
exists, so a new project needs no `gregale postgres create` first:

```yaml
databases:
  - database: chronolake-db
    app: gateway
    create:                # or `create: true` for the defaults
      postgres_major: 17
      class: development   # development (default), burstable or production
      scale_to_zero: true  # default
      region: us-east-1    # defaults to your default region
  - database: chronolake-db
    app: gateway
    env: MIGRATION_DATABASE_URL
    access: migration
```

- `create` applies only when the database is missing: an existing database
  keeps its settings, and the deploy never changes or deletes one.
- Declare `create` once per database; other entries for the same name bind it.
- A project deploy lists the database under **Managed PostgreSQL (provided by
  this deploy)** before you apply. The database counts against your plan's
  limit, and the plan stops before creating anything when it would not fit.
- `gregale deploy` of a single app waits up to three minutes for the new
  database. A project deploy reserves the bindings right away and delivers each
  key once the database is ready, usually before the first build ends.
- Creating a database needs a credential with `postgres:manage`. Deploy
  tokens and `deploy:write` keys can bind existing databases but not create
  them.

## Connect from your laptop

`gregale postgres connect` opens `psql` on your machine against a managed
database, through an app that is bound to it:

```sh
gregale postgres connect orders
gregale postgres connect orders --access read_only -- -c 'select count(*) from segments'
gregale postgres connect orders --proxy --port 5433   # for a GUI client
```

It starts a task VM of a bound app to read that binding and relays a local
port to the database through
[`gregale app port-forward`](apps.md). TLS still verifies the database's own
host name, and the password reaches `psql` in its environment, so it is never
printed or put on the command line. With `--proxy` the tunnel stays open for
another client; take the password from the app's binding.

- It picks a `ready` binding, preferring `read_write`, then `migration`, then
  `session`, `read_only` and `replica_only`. Choose one with `--app`, `--env` or `--access`.
- It needs [app tasks](apps.md) on your plan and `psql` on your machine (or
  `--proxy`). The app's image needs `printenv`, which Debian, Ubuntu and
  Alpine images include.

Choose the credential with `--access`:

| Access | Use it for |
| --- | --- |
| `read_write` (default) | The app's queries: SELECT, INSERT, UPDATE and DELETE in the `public` schema, with row-level security enforced. |
| `read_only` | Read-only queries, for example `--env READ_DATABASE_URL`. |
| `replica_only` | Reporting queries that must stay on a native production standby. |
| `migration` | Schema changes. Only the release task receives it, never the serving instances. |
| `data_api` | The [Data API](data-api.md). |
| `session` | The same privileges as `read_write` over a direct connection, for serving code that needs session state. |

`read_write` and `data_api` URLs, and `read_only` ones unless they reach a
replica, go through a connection pooler in transaction mode. Session state does not carry from one transaction to the
next, so these don't work through it:
- `SET` outside a transaction (use `SET LOCAL` inside one);
- `LISTEN` and `NOTIFY`;
- session-level advisory locks (use transaction-level ones);
- SQL-level `PREPARE` (driver prepared statements work);
- temporary tables kept across transactions.

A `migration` binding connects directly and supports all of them, but only the
release task receives it. When the serving app itself needs one of them, bind
a `session` credential instead:

```sh
gregale postgres attach orders my-api --access session --env SESSION_DATABASE_URL
```

Each serving instance then holds its own server connections, so keep its pool
small: a `session` login allows at most 42 connections across every instance
of the app (2 per instance at Scale concurrency), and further connections are
refused with "too many connections for role". Keep ordinary query traffic on a
pooled `read_write` binding. `session` is offered only where the database
backend has qualified it (`gregale postgres capabilities` lists it).

An app holds at most 8 managed PostgreSQL bindings on Free, 25 on Hobby, 50 on
Pro and 100 on Scale. A binding's environment key must start with a letter and
may not be `PORT` or start with `GREGALE_` or `FAAS_`, which the platform sets.

Run migrations as a release task, with a Procfile `release: npm run migrate` or
`release.command` in `gregale.yaml`, and point the migration tool at a
`migration` binding:

```sh
gregale postgres attach orders my-api --access migration --env MIGRATION_DATABASE_URL
```

A failed release command leaves the previous deployment serving. A rollback
never undoes a migration; see
[Releases that change the database](releases.md#releases-that-change-the-database).

For reporting workloads that must stay on a standby, attach a native production
database with replica-only access.

```sh
gregale postgres attach orders reporting-api --env REPORTING_DATABASE_URL --access replica_only
gregale postgres status orders
```

The replica-only URL uses transaction pooling and has read-only SQL privileges.
Each new transaction checks standby availability and WAL replay backlog before
it borrows a server connection. An unavailable standby, unknown replay state,
or backlog above the threshold returns the retryable PostgreSQL error
`57P03`. The client can reconnect and retry. Replica-only connections require an
already-running database. A transaction already on the standby may finish
while it is still a standby. The regular `read_only` mode
continues to prefer the standby and fall back to the primary.

The default replay backlog limit is 16 MiB. Operators can set
`replica_max_replay_lag_bytes` in pgproxyd configuration. Zero requires a
sample with zero backlog. Database status shows whether a standby is available,
its WAL replay backlog in bytes when measured, and the age of the sample.

To rotate a credential, run `gregale postgres bindings list orders`, then
`gregale postgres bindings rotate BINDING_ID --wait`. Rotation returns once
the new credential is active. `--wait` polls until `rotation_pending` clears,
for up to five minutes (`--wait-timeout`).

Rotation recovery queues the runtime refresh even if the original request
fails or the worker restarts. The previous serving credential stays valid
until that refresh is confirmed; retrying rotation reuses its generation.

The `rest-api-postgres` and `rest-api-postgres-python` starters
(`gregale init --template rest-api-postgres`) already use this layout. They
adopt rotated credentials and reconnect after a restore. See the
[pool runtime guide](postgres-pool-runtime.md).

### Set timeouts for a pooled app binding

Set timeout defaults independently for each native `read_write` or `read_only` binding.

```sh
gregale postgres bindings timeouts BINDING_ID --statement 5s --lock 1s --idle-in-transaction 30s
gregale postgres bindings timeouts BINDING_ID
```

Supply the settings you want to change in whole milliseconds or larger units.
Use `0` to disable a timeout. The statement timeout limits a statement's
runtime, the lock timeout limits each lock wait, and the idle transaction
timeout disconnects a session that stays idle inside a transaction.

A change is saved as `pending`. Applying it can wake a parked database.
`refreshing` means PostgreSQL accepted the defaults and the pool is refreshing
its cached revision. Once `applied`, new transactions use the updated defaults.
An in-flight transaction can finish on its existing connection, which retires
when returned to the pool. Applications can override these defaults for one
transaction with `SET LOCAL`. Use a separate `migration` binding for schema changes
and backfills with different timeout needs. Rotation carries these defaults
forward to the replacement credential.

## Run SQL from the CLI

`gregale postgres query` (alias `postgres sql`) runs a query against a managed
database, through the same bound-app tunnel as above, and prints the rows. It needs no `psql`, so scripts and coding agents
can use it:

```sh
gregale postgres sql orders "select status, count(*) from orders group by 1"
gregale postgres sql orders --file audit.sql --json
gregale postgres sql orders --write "update orders set status = 'void' where id = 42"
```

- It is read-only unless you pass `--write`. A read runs as the only statement
  of a `READ ONLY` transaction that is always rolled back, on a `read_only`
  binding when the database has one. A second statement, or one that writes,
  fails with a hint. Without a `read_only` binding it uses the app's
  credential, so functions with side effects that a read-only transaction
  still allows (`pg_terminate_backend`, advisory locks) run with that role's
  rights; attach a `read_only` binding for a hard boundary.
- `--write` runs the text as a script on a `read_write`, `migration` or
  `session` binding and commits it. To try a destructive change first, run it
  on a [branch](#branches): `gregale postgres branch orders pr-42 --ttl 2h`,
  attach the branch to an app, then `gregale postgres sql pr-42 --write …`.
- `--max-rows` (default 500) caps the rows kept per result, and `--timeout`
  (default 30 s) bounds each run: the statement timeout for a read, a cancel
  of the whole script for `--write`.
- With `--json`, numbers, booleans and `json`/`jsonb` stay typed, `NULL` is
  `null`, and every other value is its PostgreSQL text form. Each result also
  lists its column names and types and the command tag (`SELECT 1204`, which
  counts every row, kept or not).

## Import an existing PostgreSQL database

Create a fresh native database to receive the copy. Set
`SOURCE_DATABASE_URL` to a direct PostgreSQL connection URL for the source.
The source must be reachable at a public endpoint with a trusted TLS
certificate. Use a login that can read the data and schema you want to copy.

```sh
gregale postgres import orders --check
gregale postgres import orders
gregale postgres import-status orders OPERATION_ID
```

`--check` tests the source connection and major version, checks for unavailable
extensions and user objects on the target, and estimates the storage needed
for the dump and restore. It can wake a parked target. The copy rechecks
compatibility and commits in a single transaction. Use `--source-env NAME`
for a different environment variable and repeat `--skip-extension NAME` to
omit optional source extensions. Database, login and extension names must
use lowercase letters, digits and underscores, starting with a letter or
underscore. Use a direct source endpoint that accepts standard password
authentication over verified TLS. Provider-specific connection URL options,
including required channel binding, are outside this import flow.

Save the operation ID from the start response. Use `--request-id OPERATION_ID`
to reuse it after a failed or interrupted request. A finished copy under
that ID is reported as complete, including after a target restart.
`import-status` leaves a parked target asleep and reports `unverified` if
the guest cannot report the requested operation. Use `--json` for the state
and any compatibility details. The CLI exits with status 1 when the check
or copy reports `failed`.

The copy includes the source's data and schema at the time of the dump.
Source-specific role grants need to be recreated for Gregale credentials.
App bindings stay in place. Pause source writes before the final import
when moving a production workload, then verify the copy before switching
your app.

### Restore a Gregale backup file

Keep the exported custom archive and its companion manifest together and
restore them into a fresh native database.

```sh
gregale postgres create orders-recovery --region eu-central-1
gregale postgres import orders-recovery --from-file orders.dump
gregale postgres import-status orders-recovery OPERATION_ID
```

Wait until the target is ready before uploading. Keep the manifest at
`orders.dump.manifest.json`. File restore requires an export manifest
with the Gregale restore receipt. Earlier dumps can still be used with
standard `pg_restore`.

Gregale checks the archive checksum and receipt, PostgreSQL major,
extension versions, estimated space, and whether the target is empty.
The restore runs in one transaction using the target's import role. The
target briefly disconnects app sessions when it begins applying the
archive. Destination roles and grants remain managed by Gregale. Use
the operation ID printed by the CLI with `--request-id` when retrying an
interrupted upload. PostgreSQL 16 through 18 can restore managed pgvector
when the source uses the `public` schema.

### Check live migration prerequisites

Use the same source URL environment variable and an empty native target.

```sh
gregale postgres migrate check orders
```

The check reports PostgreSQL version, source permissions, available slots
and WAL senders, the WAL retention cap, table compatibility, sequence count,
and storage headroom. It checks for a writable PostgreSQL 16 source with
logical WAL and a positive slot retention cap up to 1 GiB. The first
compatibility rules cover logged public tables with a valid primary key
and default replica identity. The report flags unsupported tables and
large objects separately from sequences that would need synchronization
before cutover.

The target may wake for inspection. The command inspects catalogs and
storage and creates no publication, slot, subscription, or copy.
`compatible` means the reported prerequisites passed. Use `--json` for
the structured report and reason codes.

### Start and monitor live migration

Use a dedicated, empty single-zone native target and keep writing to the
source during catch-up. Store the source URL in `SOURCE_DATABASE_URL` and use
the same operation UUID for every retry.

```sh
MIGRATION_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
gregale postgres migrate check orders
gregale postgres migrate start orders --request-id "$MIGRATION_ID"
gregale postgres migrate status orders
```

The subscriber creates replication tables for up to 64 supported public
tables, copies the initial rows, then applies inserts, updates, deletes, and
truncates from the source. Status reports tables copied, observed apply lag,
and WAL retained by the main source slot. Keep the source schema stable
throughout the migration. The target holds the table columns and primary
keys required for replication. Prepare and verify the migration before
switching the app's bindings.

The migration fences all application connections to the target. `live`
means all initial table copies are complete and no apply lag was observed
in the latest sample. The fence still applies. If the target VM reboots or
moves, run `start` again with the original operation UUID and source URL.
The source password is kept in the guest's temporary runtime storage, so a
fresh guest needs it again. The same migration can reconnect after a
subscriber process or host relay restart.

If the source identity, publication, or slot can no longer be verified, the
migration reports `resync_required` and stops applying. Use a new empty target
for a full resync. To abandon a migration, stop it before deleting the target.

```sh
gregale postgres migrate stop orders --request-id "$MIGRATION_ID"
```

Stopping disables the subscriber and removes verified source replication
resources. The target remains fenced. If the source is unreachable or its
resources cannot be verified, status reports `source_cleanup_unverified`.
Retry with the original operation and source, or have the source administrator
verify and remove the migration's publication and replication slots.

### Prepare a live migration for cutover

Run preparation after the initial table copy finishes, using the original
migration UUID and source URL. Use migration status to follow the job.

```sh
gregale postgres migrate prepare orders --request-id "$MIGRATION_ID"
gregale postgres migrate status orders
```

The target builds supported secondary indexes concurrently while the
subscriber keeps applying changes. Preparation accepts sources with up to
128 secondary indexes and builds ordinary non-unique B-tree indexes on plain
columns with default ordering, collations and operator classes. It reports
source objects that need reconciliation, such as column defaults, foreign
keys and unsupported indexes, and counts sequences that need synchronization.

Preparation runs behind the target's application connection fence. A
`prepared` report covers the inspected schema and supported index builds.
Run replay verification after pausing source writes. Keep the source schema stable through
cutover. After a VM restart, resume the subscriber with `migrate start`
and retry `migrate prepare` with the same UUID. Completed matching indexes
are reused.

### Verify the final writes reached the target

Wait for a `prepared` report, then pause source application writes and
drain in-flight write transactions. Keep application writers paused while
verifying and through activation. Allow the migration login to create and
write its marker table. Resolve prepared transactions before verification.

```sh
VERIFICATION_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
gregale postgres migrate verify orders --request-id "$MIGRATION_ID" \
  --verification-id "$VERIFICATION_ID" --source-writes-paused
gregale postgres migrate status orders
```

Verification writes a marker into the migration's existing publication and
checks that the target durably replayed it. It rechecks the prepared schema
and synchronizes up to 64 standalone public sequences. The migration login
needs `SELECT` on those sequences. Source column defaults and owned or
identity sequences require reconciliation before verification can finish.

The receipt records the observed durable replay position, sequence count,
and verification time. It reflects one operator-declared write pause.
Verification runs behind the target's application connection fence.

Reuse the verification UUID when retrying during the same continuous
pause. If application writers resume, start a new verification with a fresh
UUID after pausing and draining them again. After a full target VM restart,
resume the subscriber with the original migration UUID and source URL.

### Activate a verified migration

Activation currently supports a Gregale-managed Neon source and a dedicated,
single-zone native target. Start replication from the source's direct primary
endpoint. All managed source bindings must belong to the same app environment,
and the target must have no existing app bindings. The environment needs one
live release.

Keep source writers paused and drained. Within five minutes of successful
verification, run:

```sh
gregale postgres migrate activate orders-native --source orders-neon --app orders-api \
  --request-id "$MIGRATION_ID" --verification-id "$VERIFICATION_ID" \
  --source-writes-paused
```

Pass `--env` for a named environment. The verification UUID is also the
switch ID and can be reused to follow the same request. The receipt must
still be fresh when the bindings change. Gregale halts replication and
confirms the subscriber is still at the verified position. The app pauses
while Gregale drains its instances, switches its bindings, retires the old
managed credentials, and opens the target. The app resumes on the same release. If the switch fails before changing
bindings, check migration status. Resume the subscriber if required and
verify again with a fresh UUID before retrying the switch.

Keep independent integrations paused until they are pointed at the native
target. Once the switch completes, new writes go to the native database.
Run `gregale postgres migrate stop orders-native --request-id "$MIGRATION_ID"`
with the original replication credentials to remove the verified source
publication, slot, and marker. The target remains available during cleanup.

## Export a portable backup

Start a background export of a native database and download the resulting
PostgreSQL custom archive and checksum manifest.

```sh
gregale postgres export orders --output orders.dump
```

The manifest records the database snapshot time, PostgreSQL major, installed
extension versions, source database size, archive size and SHA-256 checksum.
New manifests also include the receipt for managed file restore. The CLI verifies the
checksum before saving `orders.dump` and `orders.dump.manifest.json`. Reuse the
printed export ID with `--request-id UUID` to resume after disconnecting.

Exports may wake the source and use its running compute while the dump is
created. Each dump can be up to 5 GiB. One export can run per database, with up
to four queued, running or retained exports per account or organization.
Completed exports are encrypted in private storage and available for one hour
to members with PostgreSQL management access.

For an independent restore, provision a PostgreSQL server with the extensions
listed in the manifest available, then restore into an empty database.

```sh
pg_restore --exit-on-error --no-owner --no-privileges --dbname target orders.dump
```

Use the same PostgreSQL major or a compatible newer version and an account
that can install the required extensions. The archive covers the customer
database's schema, rows and indexes. Configure destination roles, app
credentials, tablespaces, and logical replication separately.

### Schedule daily backups to a private bucket

Use a private Gregale Object Storage bucket in the database's account or
organization. Specify the app that owns the bucket and the daily UTC time.

```sh
gregale postgres backup-schedule orders --daily-at 03:00 --bucket-app archive --bucket database-backups
gregale postgres backup-schedule orders
gregale postgres backup-schedule orders --off
```

A scheduled run uses the same portable export as a manual run and can wake
a parked database. The destination's configured single-upload limit applies.
The status shows the next run, the last successful backup and its object key,
and any error from the most recent run.

The worker uploads the `.dump` first. The adjacent `.dump.manifest.json`
marks a complete backup and contains the SHA-256 digest and restore metadata.
Download both files and verify the archive against the manifest before
restoring. Delivery retries use the same object keys and verify existing
files by checksum.

The bucket's lifecycle rules control how long the files stay in storage.
Disabling the schedule leaves completed backups in the bucket. If a run
is actively delivering a backup, retry the change after the worker releases
its lease.

## Restore to a point in time

A restore creates a new database at a point in time inside the restore window. The
source database and its apps are not changed.

```sh
gregale postgres restore orders --name orders-before-import --point-in-time 2026-10-30T09:00:00Z
gregale postgres attach orders-before-import my-api --env RESTORED_DATABASE_URL
```

`gregale postgres get` shows `restore_range`, the bounds Gregale accepts for
a restore request. The earliest is the later of the database's creation and
the start of its configured restore window.

Use `gregale postgres status DATABASE` to review a native database's
configured retention, last recorded base backup, and latest restore drill.
A successful drill verifies recovery at its specific target. Status shows
the last replayed commit when PostgreSQL reported it and keeps the last
success visible alongside a newer attempt. It shows `unverified` when the
database has no recorded successful drill. A parked database stays asleep
while Gregale reads these records. The JSON response includes the evidence
under `backup`.

Check archive coverage for a specific native restore target before creating
a copy.

```sh
gregale postgres restore orders --check --point-in-time 2026-10-30T09:00:00Z
```

The check can confirm coverage, report a missing base backup or WAL segment,
or report that coverage is unverified. It reads the archive through the same
storage tiers as a restore and leaves a parked source asleep. It confirms
coverage when it finds a suitable base backup and a continuous sequence of
archived WAL through a replay stopping point. After a standby promotion,
the check uses PostgreSQL's timeline history to follow the archived WAL
on both sides of the promotion. It reports the selected timeline and the
first missing timeline history file or WAL segment when available. A
restore checks the archive on its selected node and pins a verified
promoted timeline before starting the new database. The bounded scan
reports unverified when the archive
has competing timelines or the scan reaches its inspection limit. Use
`--json` for the available evidence.

Attach the restored database under its own environment key when you want an
app to read it. A source you delete while a database restored from it exists
is kept until that copy is gone; see
[Delete, recover and purge](#delete-recover-and-purge).

The restored database counts toward your plan's database limit. When the
account is at its limit, one restore may still go one database past it, on
every plan:

- The copy shows `restore_slot_until`, 7 days out, in `get`, `list` and the
  API.
- If you purge another database or upgrade before then, the copy becomes an
  ordinary database.
- Otherwise its delete is scheduled at `restore_slot_until`, with the usual
  7-day undo. Its bindings are removed first. The source is never touched.
- The account that ran the restore gets an email 48 hours before
  `restore_slot_until` while there is still no room, and another when the
  delete is scheduled. Recording the expiry email and scheduling deletion
  happen together, so a worker restart cannot lose the notice.
- A second restore past the limit while a copy holds the slot returns
  `403 managed_postgres_restore_slot_in_use`. Purge the copy to restore again.
- A copy can replace its source on a one-database plan: point your app at
  the copy, then delete the source. A normal delete is enough; you do not
  have to purge. Once the source's delete is scheduled, the copy becomes an
  ordinary database and keeps its bindings past `restore_slot_until`. The
  source keeps its 7-day undo, but recovering it then needs room under your
  plan's limit (delete or purge the copy, or upgrade). A purged source is
  kept, with its name and storage, until the copy is deleted.
- A [rollback that restores the database](releases.md#rollbacks-that-restore-the-database)
  never uses the slot: its copy becomes the app's database, so the account
  needs room for it inside the limit.

### Run a restore drill

```sh
gregale postgres restore-drill orders
gregale postgres restore-drill orders --drill-id DRILL_UUID
```

Gregale queues a drill, writes an internal marker to the source, and restores
the archive into a temporary database. A drill passes after Gregale finds the
marker in the restored copy and cleans up the temporary database. Starting a
drill can wake a parked source. Reading its progress keeps the source asleep.

The start command returns a drill ID and a request ID. Reuse the same
`--request-id` to retry a start. Gregale admits one requested drill at a
time on a source or node. `gregale postgres status orders` shows the latest
attempt alongside the last verified restore, with target, elapsed time, and
failure reason where available.

### Recover in another region

For a native database with a configured recovery destination, opt in and
check the archive that is available in that region.

```sh
gregale postgres recovery-region orders --region eu-west-1
gregale postgres status orders
gregale postgres restore orders --region eu-west-1 --name orders-recovery --latest
gregale postgres attach orders-recovery my-api --env RECOVERED_DATABASE_URL
```

`status` shows the latest time verified from the recovery region's copy of
the base backup and write-ahead logs (WAL). Replication is asynchronous.
The age of the verified point shows how far back the restore may need to
go after an outage. If a WAL segment is missing, Gregale reports the gap
and can offer an earlier point that it can still verify.

The restore creates a new database in the recovery region using only its
archive. You can choose an earlier point with `--point-in-time`. Gregale
must verify the requested point before reserving the copy. The first
version verifies a bounded range of WAL on the initial timeline of the
current PostgreSQL major. When it cannot prove that the archive covers a
point, it reports that evidence as unverified. Regional recovery needs
the Gregale control plane and the configured destination to be available.

Disable the preference with `gregale postgres recovery-region orders --off`.

### Restore and switch an app

`--switch` restores the database and moves an app to the copy in one step,
without changing the app's code. Use it when bad data, not bad code, is the
problem: a mistaken `DELETE`, a broken import, a job that ran twice.

```sh
gregale postgres restore orders --name orders-0900 --point-in-time 2026-10-30T09:00:00Z --switch my-api
```

```text
orders is restored to 2026-10-30T09:00:00Z as a new database, orders-0900, and my-api moves to it; its code does not change.
Writes to orders after that moment stay only in orders, which keeps every write and is no longer bound to my-api.
my-api stops serving while it switches (usually under a minute, then a cold start).
Type "my-api" to confirm: my-api
restoring orders to 2026-10-30T09:00:00Z as orders-0900
staging and verifying credentials for orders-0900
fencing my-api; it stops serving until the switch ends
draining my-api: 2 retired, 1 parked, 0 pending
switching my-api to orders-0900
resuming my-api on v42
✓ Switched my-api to orders-0900. orders keeps every write and is no longer bound to my-api; delete it when you no longer need it.
```

It runs the same steps as a
[rollback that restores the database](releases.md#rollbacks-that-restore-the-database),
except that the release stays the same: restore, stage and verify credentials,
fence and drain the app, move every binding the app has to the database in
the scope (`--env`, default `default`) in one transaction, then resume. Until
the bindings move, a failure or `gregale release rollback status my-api <id>
--cancel` unwinds it, and the app keeps serving on `orders`.

- **Data:** the copy holds exactly what `orders` held at the restore point.
  Writes after it are not copied; `orders` keeps them unchanged, so you can
  compare or copy rows by hand. Nothing is lost by switching.
- **Downtime:** the app does not serve from the fence until the first request
  after the switch, usually under a minute plus a cold start. Instances and
  tasks must settle within 2 minutes of the fence and the drain must finish
  within 10, otherwise the switch fails and the app resumes on `orders`.
- **Room:** the copy becomes the app's database, so it must fit inside your
  plan's database limit; it cannot use the restore slot.
- **One app per switch.** Other apps bound to `orders` stay on it.
- `--yes` skips the prompt (scripts and `--json` need it); `--no-wait`
  returns once the switch starts. `gregale rollback switches my-api` lists
  switches. The API is `POST /v1/apps/{slug}/database-switches` with
  `database_id`, `name`, `point_in_time` and an optional `scope`; it needs the
  `postgres:manage` scope and a verified email.

### Recovery objectives

What a recovery gives back, and how long it takes:

| Situation | Recovery | Data | Time to serve again |
| --- | --- | --- | --- |
| Bad data or a mistaken write | `restore --switch` | everything up to the point you pick | minutes: the restore, then under a minute of downtime and a cold start |
| A bad migration | `gregale rollback --restore-databases` | everything up to the start of the release | the same |
| A database deleted by mistake | `gregale postgres recover`, within 7 days | everything | minutes |
| A provider zone outage | none from Gregale | committed writes are kept by the provider | until the provider recovers; no high-availability promise |

The restore point can be any moment inside the restore window (7 days). The
switch's own steps are bounded: the restore 1 hour, settling 2 minutes,
draining 10 minutes; a step that runs over fails the switch and the app keeps
serving.

## Branches

A branch is a restore from the database's latest point: a new database that
shares its source's storage until it is written to. Use one to test a
migration or a destructive job against real data, then throw it away.

```sh
gregale postgres branch orders pr-42 --ttl 2h
gregale postgres attach pr-42 my-api-pr-42 --env DATABASE_URL
# run migrations and tests against pr-42
gregale postgres branch delete orders pr-42 --yes
```

- `--from live` (the default) takes the latest point, a couple of seconds
  before the request. `--from 2026-11-06T09:00:00Z` branches from any time in
  the source's `restore_range`.
- `--ttl` (5m to 30d, for example `90m`, `2h` or `7d`) purges the branch that
  long after it is created. `get` and `list` show it as `expires_at`. At that
  time its bindings are removed and it is purged: there is no undo window. A
  branch that has branches of its own is kept until they are gone.
- `gregale postgres branch list orders` lists the branches of `orders`.
  `branch delete` purges a branch at once, and refuses a database that is not
  a branch of the one you name.
- The source and its apps are never changed. A source you delete while it has
  branches is kept until they are gone.
- A branch's name is its database name, so it is unique in your account like
  any database name. With several databases, include the source in it, for
  example `orders-pr-42`.
- A branch with a TTL of 7 days or less does not count toward your plan's
  database limit. Each plan includes short-lived branches at once: Hobby 2,
  Pro 5, Scale 20. Past that, branch creation is refused with
  `managed_postgres_branch_limit`; delete a branch or wait for one to expire.
  The branch's compute and the data written to it count toward your plan's
  usage like any database.
- A branch with a longer TTL, or none, is an ordinary restore: it counts
  toward your plan's database limit and may take the one restore slot past
  it (see [Restore to a point in time](#restore-to-a-point-in-time)).
- In the API, a restore without `point_in_time` is a branch from the latest
  point, and `ttl_seconds` sets its expiry.

### Masked branches

Create a short-lived masked branch from a native database using a JSON policy
that explicitly selects tables and columns. Create the policy as a database
administrator and use `keep` only for values approved for the branch.

```json
{
  "tables": [
    {"name": "customers", "columns": [
      {"name": "id", "action": "keep"},
      {"name": "email", "action": "email"},
      {"name": "api_token", "action": "token"}
    ]},
    {"name": "orders", "columns": [
      {"name": "id", "action": "keep"},
      {"name": "customer_id", "action": "keep"}
    ]}
  ]
}
```

```sh
gregale postgres branch masked orders pr-42 --policy preview-policy.json --ttl 2h
gregale postgres status pr-42
gregale postgres attach pr-42 my-api-pr-42 --env DATABASE_URL
```

`email` generates stable `example.test` addresses within the branch. `token`
generates stable hexadecimal tokens, and `null` clears a nullable column.
Use text or sufficiently wide `varchar` columns for `email` and `token`.
Matching masked values stay equal within the branch, so related keys can join
when both sides use the same action. Columns and tables absent from the policy
are excluded from the copy.

Gregale reads a consistent source snapshot and writes only the projected data
to a fresh, scale-to-zero database with its own backup history. The target
stays in `provisioning` while the copy runs. Wait for `ready` before attaching
an app. A failed validation leaves the branch unavailable and reports an error
code in status. The TTL defaults to two hours and accepts 5 minutes to 7 days.

The manual flow supports ordinary tables in `public`, built-in scalar types,
standard serial and identity columns, and primary, unique, and foreign keys
on the selected columns. Limits are 32 tables, 256 columns, 50,000 total rows,
and 64 MiB of data processed. Unsupported selected definitions, including
custom triggers, row security and executable defaults, fail validation.

## Change the compute class

Resizing is in beta.

```sh
REQUEST_ID=$(uuidgen | tr A-Z a-z)
gregale postgres resize orders --class burstable --request-id "$REQUEST_ID"
gregale postgres resize status orders "$REQUEST_ID"
```

- Keep the request ID, a lowercase UUID: repeating it returns the same
  resize's progress.
- A confirmed rejection reports `failed` and `last_error_code`, keeps the
  original configuration and permits deletion or a corrected request with a
  new ID. An uncertain outcome stays `pending` until it can be confirmed.
- Clients may be disconnected during the change. They reconnect with the same
  credentials.
- Region, version, storage and the restore window stay as they are.

## Rehearse a major-version upgrade

Run an upgrade on a private, temporary restore of your native database.

```sh
gregale postgres upgrade-rehearsal orders --major 18
gregale postgres upgrade-rehearsal orders --rehearsal-id REHEARSAL_UUID
```

Gregale selects the latest point it can verify from the base backup and
archived WAL. The rehearsal leaves a parked source asleep and writes the
verification row only to the copy. It runs the regular extension checks and
major upgrade, then checks that the target PostgreSQL version can read
the row. Gregale deletes the copy and its archive before reporting a
terminal outcome.

The receipt reports the recovery point, phase, and elapsed time through
cleanup. A failure includes the phase and a reason code. Use the same
`--request-id` and target major when retrying a start. Rehearsals require
capacity for the copy on the source node and an archive whose recovery
point the verifier can prove. That requires a commit archived after the
selected base backup and the timeline history when the source has been
promoted.

## PostgreSQL extensions

List the installed extension versions and the managed versions bundled
with the native database's current PostgreSQL image.

```sh
gregale postgres extensions list orders
gregale postgres extensions enable orders pg_trgm
gregale postgres extensions enable orders btree_gist
```

Enablement installs the image's default version in `public`. Repeating the
request reports the version already installed, including installations made
through SQL. Listing and enabling may wake a parked database.

Before a major upgrade, check your installed versions against the target
image on the database node.

```sh
gregale postgres upgrade orders --major 18 --check
```

The check lists incompatible extension names and installed versions.
Gregale checks again before it stops a running database for an upgrade.
PostgreSQL still runs its full `pg_upgrade --check` before modifying the
cluster.

Native import checks compare source extension versions with the target
defaults. A matching version or a PostgreSQL update path qualifies the
extension for import. An incompatible version blocks the copy.

### pgvector on PostgreSQL 16, 17 and 18

The native PostgreSQL 16, 17 and 18 images include pgvector 0.8.7. Enable
the extension to use the `vector` type and indexes such as HNSW through an
ordinary app binding.

```sh
gregale postgres extensions enable orders vector
```

Native snapshot imports support pgvector installed in `public` on the
source. Gregale checks the source version and installs the managed
version before restoring the dump as the target schema owner through a
dedicated import role. If the data copy fails, its SQL transaction rolls
back. A retry can reuse the managed extension already installed in the target.

You can upgrade a PostgreSQL 16 database with managed pgvector to 17 or
18 using the existing `postgres upgrade` command. The upgrade preflight
checks the extension scripts and shared library in the target image.
Existing app bindings reconnect after the upgrade. Point-in-time restore
uses the PostgreSQL major and its pgvector library from the selected time.

## Diagnose database activity

```sh
gregale postgres diagnose orders
gregale --org commerce postgres diagnose orders --json
```

Diagnostics samples a running native primary. It reports client connections
against the engine's ordinary connection slots, active and idle transactions
with their oldest ages, and sessions blocked on locks. Up to 20 waiting
sessions appear with their PostgreSQL process IDs and known blockers. A zero
blocker PID identifies a prepared transaction.

Connection pressure covers the primary instance. Transactions and waiting
sessions cover the selected managed database. Use the sample timestamp to
relate the activity to an incident. A parked database stays asleep and
reports its parked state.

## Inspect sessions and cancel queries

```sh
gregale postgres sessions orders
gregale postgres cancel-query orders TARGET_UUID
```

The session list shows the primary's client backends, transaction age,
wait event, and application name when available. It leaves a parked database
asleep. Use `--json` for a bounded structured sample.

Active pooled queries can carry a cancel target. Use a target from a fresh
sample. The proxy verifies that the same execution still owns the backend
and that the command is for the observed primary. The result reports
whether the proxy sent the request, found a stale target, or could not
confirm delivery. A cancellation keeps the client connection open. An
application may need to roll back an explicit transaction before continuing.

Direct sessions and protocol batches without a single identifiable
execution appear in the inventory. Cancel targets are issued for individual
executions that the transaction pool can track. The view contains session
metadata and omits statement text.

## Inspect table health

```sh
gregale postgres tables orders
gregale postgres tables orders --sort dead-rows --limit 10
gregale --org commerce postgres tables orders --sort xid-age --json
```

Each row shows table and index storage, estimated dead rows and changes since
analyze, transaction ID age, and the latest recorded manual or automatic
vacuum and analyze. Table storage includes TOAST. Transaction ID age uses the
older of the table and its TOAST relation. PostgreSQL can omit vacuum and
analyze timestamps when its statistics have no record of either operation.

The default order is largest total storage first. Use `--sort dead-rows` to
find tables with the most estimated dead tuples, or `--sort xid-age` to inspect
the oldest transaction IDs first. The command returns 20 rows by default and
accepts up to 50. It samples stored user tables and individual leaf partitions
on the running native primary. A parked database stays asleep.

## Autovacuum profiles

```sh
gregale postgres autovacuum orders
gregale postgres autovacuum orders --profile high-churn
gregale postgres autovacuum orders --profile default
```

The native service offers two cluster-wide profiles. Each sets the fraction
of a table's estimated rows that PostgreSQL adds to its base vacuum and
analyze thresholds.

| Profile | Vacuum scale factor | Analyze scale factor |
|---|---:|---:|
| `default` | 0.20 | 0.10 |
| `high-churn` | 0.05 | 0.02 |

High-churn can start maintenance sooner on frequently updated or deleted
tables. Compare table health and I/O under the workload before and after
changing the profile. PostgreSQL still honors table-specific autovacuum
settings.

The command shows the saved profile and the scale factors reported by each
running instance. Gregale reloads a running primary and standby and retries
an unapplied change. A parked database stays asleep and picks up its saved
profile at the next wake. Use `--json` for the saved profile and sample state.

## Query Insights

Query Insights ranks statements by execution time for a single-zone native
PostgreSQL database. Enable collection for the database before inspecting
its history.

```sh
gregale postgres queries orders --enable
gregale postgres queries orders --since 1h
gregale postgres queries orders --disable
```

Gregale applies the collection setting on the next normal boot. An always-on
database with a maintenance window can apply it during that window. The
collector samples running primaries about every two minutes and keeps up to
24 hours of numeric query-ID history. Reading history leaves a parked
database asleep.

The report shows total and mean execution time, calls, and the amount of time
covered by complete samples. A restart, reset, or statement eviction starts
a new baseline. Disabling collection stops new samples and retains recent
history until it expires.

## Query error history

Enable error history for a native database and filter pooled errors by SQLSTATE
or time range.

```sh
gregale postgres errors orders --enable
gregale postgres errors orders --since 1h --sqlstate 40P01
gregale postgres errors orders --disable
```

The proxy records the time, severity, SQLSTATE, and app binding ID when it can
identify the binding. These are structured errors from pooled connections.
The history contains only those fields. Collection starts when each proxy
refreshes the saved setting.

Gregale retains up to 1,000 recent errors per database for up to 24 hours.
Use `--limit` to request up to 100 matching events or `--json` for structured
output. Collection uses a bounded queue, so high error volume or a reporting
outage can leave gaps. Reading history leaves a parked database asleep.
Disabling collection lets retained events expire on the normal schedule.

## Storage health

```sh
gregale postgres status orders
gregale --org commerce postgres status orders --json
```

For a native database, status shows the storage allocated per volume and
the latest free space, read-only state, and sample time for the primary
and any standby. The scheduler samples running databases every two
minutes. Status leaves parked databases asleep. It labels samples from
parked databases and samples older than five minutes historical. A
restarted or moved database needs a fresh sample before its storage can
be assessed.

A fresh low-space sample appears in the status and in the personal
database dashboard. Free space or grow the volume while the primary is
still writable. A low-space standby can lose replication if its volume
fills.

### Storage alerts

Gregale emails the verified owner of a personal or organization database
after two consecutive low-space samples from its primary. A sample that
shows the storage guard in read-only mode alerts immediately. The email
includes the sampled free space, write impact, and available growth details.

`gregale postgres status DATABASE` and the database dashboard show recent
alert history. The JSON response includes the latest 10 transitions under
`storage.alerts`, newest first. A fresh sample that confirms free space has
recovered and the primary is writable closes the incident. A parked database
stays asleep and its last alert remains visible until new observations arrive.

### Automatic storage growth

Single-node native databases can opt in to automatic storage growth with
a per-volume ceiling in bytes. Always-on databases also need a weekly
maintenance window.

```sh
gregale postgres auto-grow orders --max-bytes 10737418240
gregale postgres status orders
gregale postgres auto-grow orders --off
```

Gregale looks for sustained low space. A database that scales to zero
grows at a natural idle park. For an always-on database, Gregale reserves
storage and starts the resize during its maintenance window. If a base
refresh is also due, both changes use the same restart. A resize admitted
in the window can finish after it closes.

The ceiling, the plan's storage allowance, and available node capacity
bound each growth request. Status shows the proposed size when a fresh
sample shows low space, the upcoming window, and any blocker. If the
database becomes read-only before the window, you can request a manual
storage increase with `gregale postgres resize DATABASE --storage-bytes BYTES --request-id UUID`.

## Maintenance windows

Set a weekly UTC window for planned base and VM launch refreshes on an
always-on native PostgreSQL database.

```sh
gregale postgres maintenance orders --day sunday --start 02:00 --duration 1h
gregale postgres status orders
gregale postgres maintenance orders --off
```

When Gregale detects an outdated base or VM launch, status shows the pending
refresh, the current or next window, and the last outcome. Gregale can start
one refresh per database in each window. Choose a window long enough for a
scheduler sweep. A refresh can finish after the window closes. Removing the
window restores the default refresh policy.

On a single-node database, the refresh restarts PostgreSQL. On a production
database, Gregale refreshes the standby first when its image is outdated.
When the primary also needs a refresh, Gregale promotes the standby after
both nodes agree on the target image and the standby is synchronized. Clients reconnect using the same
connection strings. Status shows the standby refresh, switchover, and
restoration of redundancy. If standby validation fails before Gregale fences
the primary, the primary keeps serving and status reports the reason.

Synchronous writes may wait during the standby restart. After switchover,
replication can be degraded while the replacement standby is built.
Replica-only connections can receive retryable SQLSTATE `57P03` while there
is no eligible standby. If the outcome of a stop or promotion is uncertain,
Gregale keeps connection admission fenced while it verifies the handoff.

## Usage and limits

```sh
gregale postgres usage
```

`usage` shows this month's compute, storage, restore history and egress, and
how many databases you have against your plan's limit. It can trail the
databases by up to two hours.
- Compute: `compute_cap_state` is `warning` at 80% of the month's allowance. At the
  allowance the databases are suspended until the 1st of next month or an
  upgrade, unless compute overage is on. A suspended database keeps its data.
- Compute overage (`gregale postgres overage on|off`) keeps the databases
  running past the allowance, up to twice it, and bills the excess at $0.2331
  per CU-hour. It is off by default on Hobby and Pro and on by default on
  Scale; turning it off on Scale is kept.
- Storage: at the pooled storage limit, writes that would grow a database
  fail. Reads, deletes and `VACUUM` keep working, and nothing is suspended or
  deleted. With overage on, the limit is twice the pool, and storage past the
  pool, averaged over the month, is billed at $0.50 per GiB-month.
- Restore history and snapshots: `usage` shows `history_byte_seconds` (what
  point-in-time restore keeps), `snapshot_byte_seconds` and
  `included_history_bytes`, which equals your storage pool. Restore history
  uses the included amount first. It grows with how much your databases write,
  not with their size: rewriting 1 GiB a day keeps about 7 GiB with a 7-day
  restore window. With overage on, what passes the included amount, averaged
  over the month, is billed at $0.21 per GB-month for restore history and
  $0.0945 per GB-month for snapshots (10^9 bytes). A month that billed it
  keeps the charge if you turn overage off.
- With overage off, restore history is bounded instead of billed. Once this
  month's restore history and snapshots pass twice the included amount,
  `usage` shows `history_state: limited` and your databases keep only the
  last 24 hours of history until the 1st of next month, or until you turn
  overage on. Restoring to a point older than 24 hours is refused while
  limited, and history dropped while limited does not come back. Writes,
  reads and data are not affected.
- Egress: each database has its own 500 GB (10^9 bytes) monthly egress
  allowance, separate from your apps' plan allowance. `usage` shows
  `egress_allowance_bytes` (per database) and what all the databases sent
  (`egress_bytes`). `egress_state` follows the database that sent the most:
  `warning` at 80% of its limit and `held` once it reaches it. That database
  then refuses connections until the 1st of next month; your other databases
  and apps keep running, and the account is emailed once that month. Data a
  database sends your app counts as database egress; private network
  transfer (Private Link) does not. A copy of a database (a restore, a branch
  or a PR preview copy) shares its source's 500 GB: the source and its copies
  are held once together they sent it.
- With egress overage on (`gregale billing egress-overage`), each database's
  egress past its own 500 GB (shared with its copies) is billed at $0.105 per
  GB. `egress_state` is then `overage`, and a database is held only at twice
  its allowance.
- Database overage counts toward your [spend cap](billing.md#spend-cap) and
  is billed only up to it; at the cap your databases are held with
  `hold: compute_ceiling`.

When the platform stops a database that is still `ready`, `get`, `list` and
the API say why in `hold`: `compute_ceiling`, `egress`, `inactive` (the
account is suspended), `plan_excess` or `preview` (a PR preview database once
the account used 80% of its compute). `usage` shows `hold` when every database
of the account is held. A held database keeps its data.

## Delete, recover and purge

```sh
gregale postgres delete orders
gregale postgres recover orders
gregale postgres delete orders --purge
```

`delete` does not delete a ready database at once.
- The database moves to `deleting`. Its compute stops and it refuses
  connections.
- Its `restorable_until`, 7 days out, shows in `get`, `list` and the API.
- Until then, `recover` (API: `POST /v1/postgres/databases/{id}/undelete`)
  returns it to `ready` with its data and restore history.
- After that, it is deleted for good.
- A database that never became ready is deleted at once.

If a delete request fails while the provider is confirming that compute has
stopped, check the database with `gregale postgres get`. Its delete may already
be scheduled. Retrying delete finishes that step without extending the 7 days;
`recover` can still undo it.

`--purge` (API: `DELETE /v1/postgres/databases/{id}?purge=true`) deletes the
database and its restore history at once, even during the 7 days. It cannot be
undone through Gregale. The provider keeps the deleted data for up to 7 more
days before it erases it.

Until it is purged, a deleted database:
- still counts toward your plan's database limit;
- keeps its name, so creating another database with that name returns 409;
- uses nothing from your compute allowance or storage pool, unless it has a
  restored copy (below).

A database you restored a copy from, for example the original database after
a rollback that restored it, can be deleted while the copy lives. The copy is
stored inside the original, so:
- the 7 days to recover it run as usual, and its usage keeps counting, because
  it includes the copy's;
- after that, or at once with `--purge`, it stays in `deleting`, with
  `last_error_code` `restore_copies_remain`. It no longer counts toward your
  database limit, and can no longer be recovered;
- it is deleted for good once you delete its copies. Until then its name stays
  taken, and the storage it holds stays in your storage pool.

Undelete applies the same plan check as create. A database your current plan
no longer allows, for example after a downgrade, cannot be recovered.

`delete` asks you to type the database's name. Scripts pass `--yes` (or
`--quiet`); JSON and `--non-interactive` mode require it. A database with an
attached app cannot be deleted. Detach the app first with
`gregale postgres bindings delete BINDING_ID`.

## Dashboard

The signed-in dashboard's `/dashboard/postgres` page shows your databases,
their bindings and any scheduled delete. It is read-only: create, restore,
delete and bind with the CLI or the API.

## API and SDKs

Every command uses the public API under `/v1/postgres/*`, and the SDKs cover
the same operations. Company databases, in beta, use
`/v1/orgs/{organization}/postgres/databases`.
- API keys need `postgres:read` to read databases and usage.
- They need `postgres:manage` to create, restore, resize, delete, undelete or
  bind.
- Responses never contain passwords, connection URLs or provider IDs.
  Credentials reach your app only as app secrets.
- Every CLI command takes `--json` and prints the API's response.

## Deleting your account

An account with databases cannot be deleted. Purge each database first:

```sh
gregale postgres list
gregale postgres delete orders --purge
```

A database whose delete is only scheduled still blocks account deletion until
its 7 days end.
