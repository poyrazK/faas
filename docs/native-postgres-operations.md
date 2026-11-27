# Native PostgreSQL operations

This preview extends [Managed PostgreSQL](postgres.md) for accounts enabled for native PostgreSQL operations.

## Replica-only bindings

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

The default replay backlog limit is 16 MiB. Database status shows whether a
standby is available, its WAL replay backlog in bytes when measured, and the
age of the sample.

## Set timeouts for a pooled app binding

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
switch ID. Use `gregale release rollback status orders-api "$VERIFICATION_ID" --migration --wait`
to follow the same request. The receipt must still be fresh when the bindings change. Gregale halts replication and
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

## Check restore coverage

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

## Run a restore drill

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

## Recover in another region

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
must verify the requested point before reserving the copy. If the destination
has yet to verify a point after a major upgrade, status and `--latest` can use
the newest verified point on a retained older major. When Gregale cannot prove
that the archive covers a point, it reports the archive as unverified. Regional
recovery needs the Gregale control plane and the configured destination.

Disable the preference with `gregale postgres recovery-region orders --off`.

## Masked branches

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
