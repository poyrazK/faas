# Managed Postgres data pipelines

Pipelines copy selected tables from an external PostgreSQL 16 database into a
dedicated Gregale managed database and keep them synchronized. You can expose
the mirror through a read-only [Data API](data-api.md). This feature is available
only to operator-approved staging accounts on Hobby, Pro, or Scale.

## Connect and read

Start in **Dashboard → Data connections**, or inspect a source with the CLI:

```sh
gregale pipeline connection add orders-source --url-file ./source-url.txt
gregale pipeline connection check CONNECTION_ID
gregale pipeline connection tables CONNECTION_ID
```

The private worker checks verified TLS/network access, PostgreSQL version,
logical replication, retention, publication and replication permissions, and
available slots/senders. Discovery lists public tables, columns, primary keys,
and specific compatibility blockers. Unsupported tables remain visible with
remediation; they cannot be selected. No customer rows are read by discovery.

Use `connection check CONNECTION_ID --database DATABASE_ID` to also inspect an
existing empty mirror's native-subscription permission and replication worker
configuration. Checks default to waiting for a result; `--wait=false` queues
the job and returns immediately. `connection tables` reads the latest check,
so run `connection check` again when it expires or reports stale credentials.
Active requests and requests within 15 seconds of the previous request may
reuse that job. Rotate a credential before checking a replacement password.

The dashboard provides table selection, explicit reader subjects, saved setup,
and sync health. After a passing source check, choose **Start saved setup** to
provision a new dedicated mirror in the default or requested region. To use an
existing empty mirror, select it when checking the connection. The saved workflow
checks the source, prepares the mirror, checks both ends, starts replication and
waits for fresh live status. Its progress continues after you close the page.
Automatic refresh pauses while you edit a form. Previously discovered table
selections can be saved after their check expires; the worker obtains fresh
approval before provisioning or starting replication.

Saved setup can also deploy an optional Data API: supply an API name and select
an owned, deployed Auth app. The workflow prepares a read-only binding, submits
the normal template build, and waits for the intended deployment to be live.
Failed builds can be retried while preserving the mirror and app. Existing
mirrors without the setup service remain supported by direct pipeline creation.

Prepare a private file containing a PostgreSQL URL with an explicit nonempty
username/password and `sslmode=verify-full`,
then restrict it to your user (`chmod 600 source-url.txt`). Supply the file path,
not the credential itself, to the CLI:

```sh
gregale pipeline create orders \
  --url-file ./source-url.txt \
  --tables orders,customers \
  --read-subjects customer-alice,customer-bob \
  --api orders-data --auth customer-auth \
  --wait --timeout 30m
```

The command registers `orders-source` and checks its selected tables **before
provisioning** `orders-mirror`. It then checks both ends, requests the copy,
waits for fresh live status, and configures and deploys `orders-data` with
a managed SELECT-only binding. The Auth app must already exist. Both listed JWT
subjects may read every row in both selected tables. Choose subjects belonging
to trusted readers of the whole dataset. Other subjects see no rows. Writes are
rejected by both the HTTP runtime and database privileges.

To use an existing source, run `gregale pipeline connection add NAME --url-file
./source-url.txt`, then pass `--connection CONNECTION_ID` to create. Use
`--database DATABASE` for an existing empty, unbound managed database, or
`--region REGION` to choose placement for a new one. Omit `--api` to operate the
mirror without deploying an HTTP app. Provisioned resources remain available
for inspection if a later step fails; retry with their source and database IDs.
Source registration uses a credential-derived idempotency key; rotate an existing
source when its credential changes instead of registering its name again.

Every new pipeline requires a passing connection check less than 90 seconds old
for the current source credential and each selected table. Checks for a specific
mirror apply only to that mirror. Missing, expired, rotated, blocked or unsupported
selections return `data_connection_check_required` (HTTP 409). The control catalog
locks the checked credential/result while reserving intent. Retrying an already
accepted, identical pipeline still recovers its existing intent after evidence
expires. The replication engine rechecks source compatibility before creating
native resources.

Generate the read contract with `gregale data-api types orders-data --output
./database.types.ts`. Use `.schema('public')` when selecting mirrored tables with
`createDataClient` from `@gregale/data`, and authenticate using the selected Auth
app. See [Data API onboarding](data-api-onboarding.md) for token and client setup.

## Saved dashboard setup and recovery

Setup submission records immutable table/reader selections, a frozen mirror plan
and a stable database ID before any provider operation. New mirror names include
a setup-specific suffix. Provider reconciliation runs in the private apid worker,
using the existing managed database service and its provisioning/account gates.
Repeated submissions with the same setup name and selections return the existing
operation. Different selections under that name conflict; use a new name after
stopping the old workflow when changing its intent.

The optional **API app name** and **Auth app** fields deploy a read-only Data API
inside this saved workflow. Choose an existing, deployed Auth app owned by your
account; its issuer is pinned when setup is saved. The worker waits for a fresh
live mirror, reserves a dedicated app, creates a SELECT-only `DATABASE_URL`
binding through the normal managed-binding service, and submits the built-in
Data API template to the existing builder queue. Builds still run in builder
microVMs. The API exposes `public`, requires an Auth JWT and rejects writes.
App/compute billing, app quotas, deployment rate admission and connection-capacity
admission still apply. The installed worker can write the shared build spool and
artifact cache; custom spool/cache locations also need matching systemd write
paths. Read-only API setup does not enable browser CORS origins.

Progress includes configuration, build submission and deployment readiness.
The endpoint and a bounded authenticated query example appear after the intended
deployment is live. An accepted build with a lost response is recovered using its
saved intent identity. A terminal deployment failure requires **Retry saved setup**:
that creates a new build/deployment attempt linked in the saved metadata and
annotation, while retaining the same app, binding, pipeline and mirror. API failure
does not stop replication. Review deployment logs before retrying. A changed Auth
issuer or a conflicting resource blocks setup instead of silently adopting it.

Leave both optional fields empty for a mirror-only setup. You can also deploy
its read-only API later using the CLI:

```sh
gregale data-api create orders-data --database MIRROR_ID \
  --read-only --schema public --auth customer-auth
```

Use an Auth app whose subjects match the trusted readers selected during setup.
The setup name is reserved for its matching pipeline; existing pipeline names are
refused before mirror provisioning.

The source check is refreshed before provisioning if its metadata or shorter
capacity proof has expired. Mirror creation rechecks the current account's plan
against the saved definition. Mirror checks and atomic pipeline admission remain
required; a newly provisioned mirror can pause at **needs attention** until its
exact allocation is operator-qualified. Creating a mirror does not grant it
capacity qualification automatically.

**Retry saved setup** resumes its saved stage using the same resource IDs. Worker
leases and intent revisions fence stale completion, including after cancellation
or lease takeover. A lost provider or pipeline acknowledgement is recovered from
the existing reservation instead of creating another resource. Failed/blocked
setups remain visible with their selected tables and mirror reference.

**Stop setup** stops workflow advancement and retains requested databases,
pipelines, API apps, bindings and builds, including requests already in flight. It does not roll back a database
or disconnect a pipeline that has already started. Queued builds and deployments
continue under their existing owners; stopping setup does not cancel them. Use **Disconnect pipeline**
with confirmation to clean up native replication while retaining imported rows,
then remove the managed database separately if desired. Source credential rotation
and source removal are available in the dashboard. Active/blocked setups prevent
source/account deletion and mirror deletion or resize until stopped; active
pipelines continue to enforce their own cleanup guards afterward.

An account can have at most three active/blocked saved setups and one such setup
per source connection, because discovery uses one current result per source.
Unstarted setups reserve slots from the account's existing three-pipeline limit
before requesting a billable mirror. Direct pipeline creation observes those
reservations. Up to 100 setup records are retained; cancellation during the first
source-check stage can be pruned, while records that may own resources remain
until their source connection is explicitly removed. No credentials or customer
rows are stored in setup records.

## Source and destination requirements

- PostgreSQL 16 with `wal_level=logical`, available replication slots/senders,
  verified TLS using a publicly trusted server certificate, and
  `max_slot_wal_keep_size` set to a positive value no greater than `1GB`.
- A source role with REPLICATION, database CREATE, SELECT, and ownership rights
  on each selected table, so it can manage its own publication and logical slot.
- Between 1 and 16 ordinary public tables, each with a primary key and at most
  128 columns. Names must be lowercase SQL identifiers beginning with a letter.
  Partitioned tables, source row security, generated columns, arrays, enums,
  domains, and custom types are currently unsupported.
- Supported builtin types: integer, floating point, numeric, boolean, text,
  varchar/char, UUID, timestamp/date/time/interval, JSON/JSONB, bytea, inet/cidr,
  and macaddr/macaddr8. This mirror copies stored values, not source indexes,
  defaults, foreign keys, application policies, or triggers.
- An empty managed destination without existing app bindings. Active mirrors
  permit only read-only app bindings and reserve the database lifecycle until
  the pipeline is deleted. Only the qualified internal provider adapter can
  supply the worker's subscription-capable destination credentials.

The source must be reachable from both the worker and the managed destination;
allow the destination provider's egress addresses at the source firewall. The
operator's exact host and CIDR allowlist also applies to native subscription
connections. DNS changes outside that policy block reconnection.

## Observe and recover

```sh
gregale pipeline list
gregale pipeline status PIPELINE_ID
gregale pipeline wait PIPELINE_ID --timeout 30m
gregale pipeline pause PIPELINE_ID --wait
gregale pipeline resume PIPELINE_ID --wait
gregale pipeline verify PIPELINE_ID --wait
gregale pipeline resync PIPELINE_ID --wait
gregale pipeline delete PIPELINE_ID --wait
gregale pipeline connection rotate CONNECTION_ID --url-file ./replacement-url.txt
gregale pipeline connection delete CONNECTION_ID
```

`desired_state` records your request; `state`, `reason`, `tables_ready`, and
`retained_wal_bytes` describe the latest worker observation. `observation_fresh`
is true only for a check of the current intent within 90 seconds. Live means
all copies finished, the initial WAL barrier was acknowledged, the source slot
was active and within 16 MiB, and the target receiver was recently active. This
is an asynchronous mirror, with no promise of a zero-lag read.

A pause retains readable rows and its upstream slot. Source WAL may accumulate;
the configured finite retention backstop can invalidate the slot during a long
pause or outage. Unsupported schema changes and invalidated slots result in
`resync_required`, stop replication, and deny mirror reads. A verified mismatch
in an unchanged live table gates only that table and offers confirmed
[table repair](#repair-an-already-live-table). Explicit `resync`
starts a new generation and replaces only the preceding generation's owned
tables. It can interrupt reads and requires enough time for a full new copy. Existing
Data API queries may return no rows while the new generation denies reads.
After a resync that changes the schema, run
`gregale data-api refresh orders-data --wait`. Regenerate client types before
using new columns. Supported approved additions use the automatic refresh below.

Verification is resumable across worker restarts and is no longer limited to
10,000 rows or 16 MiB per table. Each batch checks at most 2,000 rows, normally
up to 4 MiB, with a 16 MiB maximum for one row. Status remains `pending` until
all ranges are checked and a final consistency fence succeeds; reaching a
verification work limit reports `inconclusive`, never equality. See
[Resumable verification](#resumable-verification) for progress and recovery.

Deleting a pipeline removes its publications, subscriptions, and replication
slots, while keeping destination rows. An unreachable source leaves deletion
pending; restore connectivity or rotate credentials and retry. Active source
connections cannot be removed. Database/account deletion requires pipeline
cleanup first. Historical list output is bounded to 100 records; active records
remain visible. Connection URLs and private worker credentials never appear in
list/status responses.

Limits per account are 10 source connections and 3 active pipelines, with at most
32 read subjects per pipeline and 1,000 resync generations. The worker checks a
pipeline about every 15 seconds.

### Capacity admission

Connection checks report capacity separately from compatibility: `ready`,
`insufficient_capacity`, or `needs_qualification`. The CLI requires fresh source
capacity before provisioning, and fresh capacity at both ends before creating
the pipeline. The dashboard enables creation only when both are ready. A check
reserves nothing: the catalog atomically rechecks evidence and reserves the
copy peak when accepting the pipeline. A concurrent winner can therefore make a
later creation return HTTP 409 with an actionable capacity code.

Capacity evidence expires after 30 seconds and requires a private-worker SQL
measurement plus an operator-qualified usable allocation. PostgreSQL settings
and compute class alone never establish provider capacity. Source aliases,
users, databases and accounts that share physical limits must share one reviewed
scope. The ledger serializes allocations across accounts and both endpoints;
a failed target allocation rolls back the source allocation too.

Each pipeline holds five connections at each endpoint: two control/verification
sessions and a conservative apply/copy peak of three. The source also holds three
slots and WAL senders; the mirror holds three logical workers. Peak reservations
remain through initial copy, live sync, pause and resync. Resync cleans the old
generation before starting the replacement. A delete request releases nothing;
only a current worker's confirmed cleanup releases the pipeline's allocation.
Lost responses, stale workers, restarts and expired measurements cannot free it.

Bindings on an admitted mirror receive PostgreSQL login connection limits before
credentials are delivered. A login is capped at 42 connections (two per instance
at Scale concurrency plus one rollout instance); Auth logins on qualified
non-mirror databases are capped at 84 (four per instance). Each binding reserves
both current and previous logins plus two credential-control connections: 86
connections normally, 170 for Auth. This covers wake bursts, credential rotation
and arbitrary client pools through an enforced ceiling. Active pipeline mirrors
still allow only read-only bindings. A mirror with 100 usable connections can
admit one pipeline plus one read-only binding: 5 + 86 = 91. The optional Data API
is a separate admitted binding; if it cannot fit, CLI setup retains the pipeline
and app for recovery.

`pipeline status` includes fresh capacity for already reserved resources, so a
fully allocated server can be ready for its existing sync while refusing another
pipeline. The inherited connection-budget API reports
`model: qualified_shared_connection_limits` and `enforced: true` for qualified
placements. Its modeled peak is the total reserved ceiling across the shared
physical scope, including native work, bindings, rotation and control sessions.
Canonical runtime-pool estimates remain advisory and unknown without their own
complete evidence. Neither model reports a count of live SQL sessions.

External clients, provider/admin traffic and control probes require a separately
bounded allowance in the qualification; at least two external connection slots
are mandatory. Operators must qualify this allowance for the deployed worker
count and provider control traffic. The default staging unit runs one private
worker. Observations clamp the reviewed allocation to actual server settings
minus those external allowances. PostgreSQL's limits remain authoritative if an
external consumer violates its reviewed envelope. Live-provider and load/outage
qualification remain required; production remains disabled.

Existing native pipelines retain their resources while private workers recover
ledger holds. Missing endpoint mappings or legacy unbounded credentials keep
capacity unknown and block new allocations. Remove and recreate legacy bindings
through confirmed credential cleanup before qualifying their ceilings. Native
replication can continue while capacity observations are unavailable; a capacity
block does not itself confirm a paused subscription. Staged cutovers involving
admitted databases are refused until their private credentials have a separate
capacity model.

Discovery is bounded to the first 128 public relations in catalog name order,
129 reported columns per table (the supported limit remains 128), and a 4 MiB
result. Truncation is explicit and only reported, supported tables are eligible.
One check job is retained per connection. A dead worker's 60-second lease can
be reclaimed, and a replaced job or credential revision cannot publish results.

## Operator staging pilot

Apply migrations, install the new binary and optional
`deploy/systemd/faas-apid-data-pipeline-worker.service`, and configure both apid
and its private worker from the same `/etc/faas/apid.toml`:

```toml
[data_pipelines]
enabled = true
accounts = ["00000000-0000-0000-0000-000000000001"]
hosts = ["source.example.com"]
cidrs = ["203.0.113.4/32"] # replace with the reviewed, routable source address
recipient_path = "/etc/faas/secrets/pipeline.age.pub"
identity_path = "/etc/faas/secrets/pipeline.age"
```

Capacity qualification is explicit operator configuration. Archive reviewed live
provider/native-subscription evidence and measured usable allocations, then add
entries to the same TOML. This example is illustrative: replace every identity,
report digest, timestamp and allocation with evidence for your staging resources.
A report digest is an operator attestation reference; the worker validates its
shape and lifetime, not the contents of an external report.

```toml
[[data_pipelines.capacity]]
scope = "source-cluster-immutable-id"
source_endpoints = ["source.example.com:5432", "source-alias.example.com:5432"]
placement = "reviewed-immutable-source-resource"
generation = 1
report_sha256 = "REPLACE_WITH_64_HEX_DIGEST_OF_REVIEWED_EVIDENCE"
qualified_at = 2026-10-17T08:00:00Z
expires_at = 2026-10-17T20:00:00Z
allocation = { connections = 90, replication_slots = 6, wal_senders = 6, logical_workers = 0 }
external = { connections = 7, replication_slots = 1, wal_senders = 1, logical_workers = 0 }

[[data_pipelines.capacity]]
scope = "mirror-cluster-immutable-id"
database_id = "00000000-0000-0000-0000-000000000002"
backend_fingerprint = "REPLACE_WITH_EXACT_CATALOG_BACKEND_FINGERPRINT"
data_resource_id = "REPLACE_WITH_EXACT_IMMUTABLE_PROVIDER_DATASET"
database_generation = 1
placement = "reviewed-immutable-mirror-resource"
generation = 1
report_sha256 = "REPLACE_WITH_64_HEX_DIGEST_OF_REVIEWED_EVIDENCE"
qualified_at = 2026-10-17T08:00:00Z
expires_at = 2026-10-17T20:00:00Z
allocation = { connections = 95, replication_slots = 0, wal_senders = 0, logical_workers = 3 }
external = { connections = 2, replication_slots = 0, wal_senders = 0, logical_workers = 1 }
```

Qualifications last at most 24 hours; SQL observations last at most 30 seconds.
Expired, absent or mismatched qualifications never admit a new consumer. Expired
records still permit API/worker startup so pause and confirmed cleanup remain
available. Mirror
entries pin the exact catalog database generation, backend fingerprint and dataset.
`generation` is the shared physical allocation epoch. Entries for the same scope
must agree on placement, epoch, report, validity and allocations. Include every
server alias under one scope, including endpoints used across accounts. The
worker never infers physical identity from a username or database name. Active
allocations cannot move to a different scope or epoch; confirm cleanup first.
No public API accepts operator qualification fields. Restart the worker after
updating this configuration; keep apid and worker policies consistent.

Set the deployment's existing `FAAS_ENVIRONMENT=staging`. The paid managed
Postgres provider must already be configured. Enabled configuration is refused
in production. An empty account or network policy is refused. The API needs the
public age recipient only; the worker requires the matching private identity
owned by `faas-apid` with mode 0400 (0440 is accepted only for systemd credential paths).
Use the existing secretbox raw key-file format. During rotation, retain the old key as
`pipeline.age.previous`, replace the current private/public pair together, and
restart both processes. Rotate each source credential to reseal it under the
new key before removing the previous key. Back up the keys and control catalog
using the existing secret-custody procedures.

After preparing source access, start the private worker explicitly with
`systemctl enable --now faas-apid-data-pipeline-worker`. It has no HTTP listener;
process failure restarts under systemd. Watch `journalctl -u
faas-apid-data-pipeline-worker` and pipeline status/freshness. Do not enable this
unit before configuring the pilot and its keys. Native PostgreSQL replication
continues when this worker stops, while status observations become stale.
Pause or delete existing pipelines and wait for confirmation before disabling
the pilot or retiring its worker. An outage exceeding the source
retention window requires resync, rather than continuing through a WAL gap.

For local acceptance, install the runtime's pinned Node dependencies with
`npm ci --prefix cmd/gregale/templates/data-api`, then run `make
test-data-pipeline` with Docker, Go, Node, and OpenSSL available. Containers and
credentials are disposable. These tests do not qualify a live provider or enable
production. See [ADR-1591](adr/1591-managed-postgres-data-pipelines.md).

## Live staging canary

The [staging canary](../tests/data-pipelines/staging_canary.py) records actual
fresh reserved capacity, provider replication, signed reads, read-only enforcement, subject isolation,
bounded verification, pause/resume and confirmed replication cleanup. It is an
operator-run acceptance tool. No provider result is implied by its unit tests
or the local native acceptance suite.

Prepare an **isolated, disposable staging source** with the permissions and
settings above and this one-row fixture:

```sql
CREATE TABLE public.pipeline_canary (id integer PRIMARY KEY, value text NOT NULL);
INSERT INTO public.pipeline_canary VALUES (1, 'initial');
```

Create a dedicated pipeline whose name begins with `pipeline-canary-`, using
only this table and one trusted canary reader subject. Use `--api` and `--auth`
to deploy its read-only API. Prepare valid JWTs from that Auth app for the
allowed reader and a different subject that is not allowed. Keep the source
URL and both JWTs in private, user-owned mode-0600 files. Use a standard
PostgreSQL 16 `psql` client and the current `gregale` binary.

```sh
export FAAS_ENVIRONMENT=staging
export FAAS_API=https://your-staging-control-api.example
# Authenticate gregale for the approved staging account through its normal flow.
python3 tests/data-pipelines/staging_canary.py \
  --pipeline PIPELINE_ID \
  --source-url-file ./source-url.txt \
  --reader-token-file ./reader.jwt \
  --other-token-file ./other-subject.jwt \
  --data-api-url https://your-canary-data-api.example \
  --report ./pipeline-provider-canary.json
```

The canary changes the fixture value, verifies it through signed HTTP, confirms
the paused snapshot, resumes and verifies the next value, then deletes the
pipeline and source connection after replication cleanup. It retains the mirror
and deployed Data API for inspection; remove those separately after reviewing
the report. A failure retains resources for diagnosis and returns a sanitized
phase/code. Source credentials never enter process arguments, and JWTs are not
forwarded across redirects. The report always keeps `production_ready: false`
and capacity qualification marked `operator_review_required`. Its capacity phase verifies fresh reservations at both endpoints before exercising replication. Provider/external-allocation pressure tests, outage/WAL-loss drills and a release decision are still required.

### Applied progress and recovery

Pipeline status includes `applied_through_at`, a conservative source WAL
checkpoint timestamp confirmed by the destination subscription's replication
origin and local WAL flush. This evidence belongs to the current generation,
survives worker restart, and is cleared on resync. Initial-copy readiness,
receiver messages and retained WAL alone never establish applied freshness.
The worker samples a new checkpoint after confirming the previous one; it does
not write heartbeat rows or require new source write privileges.

`health` is evaluated when status is read. A recent confirmed checkpoint and
current monitoring yield `healthy`; expired monitoring yields `unknown` even
when the last persisted lifecycle state is `live`. A checkpoint without recent
confirmation yields `apply_unconfirmed`. An idle publication may not advance its
replication origin, so this is **not** proof of stalled replication or measured
time lag. If selected rows are changing, investigate apply health. Do not resync
solely because a quiet source has an unconfirmed checkpoint.

The dashboard displays in-app alerts and recovery guidance for stale monitoring,
credential failures, blocked sync, retention pressure (80% of the existing
retention ceiling), pause and required resync. Reconciliation already retries
transient failures. Credential rotation uses the existing source-connection
rotation command; resume and confirmed resync use existing revision-guarded
controls. Resync requires confirmation and replaces imported tables. There is no
automatic destructive recovery or external notification delivery in this slice.

This remains staging-only. Actual provider compatibility, capacity and outage
recovery must still be qualified before a customer pilot; local native
PostgreSQL acceptance is not provider qualification.

### Full provider qualification

The [provider qualification runner](data-pipeline-provider-qualification.md)
creates a fresh pipeline and extends the canary with credential rotation, worker
restart, source/destination outages, atomic capacity denial, retention pressure
and independent replication-object cleanup checks. It uses scoped, explicitly
configured staging fault controls and writes a versioned report with passed,
failed and not-run phases. No live qualification has been executed here; provider
credentials and deployment-specific fault controls remain prerequisites.

## Add tables to an existing pipeline

Open **Dashboard → Data connections**, select the pipeline's source connection,
and run a fresh check. In **Add tables to an existing pipeline**, select new
supported tables and choose **Preview table additions**. Review the exact table
definitions and explicitly approve exposure: every saved reader can read every
row in the added tables. You can discard an unapproved selection. Removal and
replacement of selected tables are outside this workflow.

The current selection must be live and recently observed, without an unfinished
schema migration, table addition or manual verification. The combined selection
retains the 16-table limit. Discovery must cover all selected tables using current
source credentials. Compatibility checks account for an already occupied mirror;
capacity admission uses the existing pipeline's qualified peak reservation,
rather than reserving a second pipeline. The worker rechecks actual native-copy
worker, slot and sender headroom before starting additional copy workers.

Approval updates the saved table selection without changing the pipeline
generation. The worker creates only the added owned tables with read policies
denying access, extends the existing publication, and refreshes the existing
subscription with native initial copy enabled for previously absent relations.
Durable destination stages recover committed DDL, publication changes and
ambiguous refresh responses. Ordinary table onboarding preserves existing native
subscription relations and their ready states. Source default expressions remain on the source.

Existing table read policies stay open while new tables copy, catch up, verify
and wait for API refresh. Resumable verification tracks only the added tables;
its private checkpoint records the exact table scope so cancellation, retries
and generation cleanup remove the right tracking objects. Source writes during
copying and verification continue through native replication. Only after the
new tables pass verification, all bound runtimes refresh, and the final fenced
check succeeds do their reader policies open. Regenerate client types before
querying the new tables.

Pause/resume still controls the shared subscription. Stopping verification
retains copied data and keeps only the new tables gated; retry starts a fresh
proof. Drift in an approved new table or missing copy tracking blocks onboarding
without automatically replacing existing tables. Restore the approved definition
or explicitly confirm resync, which replaces the full selected mirror. Shared
subscription failures, existing-table schema drift and lost/excessive retained
WAL still affect the entire pipeline; available reads do not imply fresh sync.

This dashboard workflow remains staging-only and unreleased. Local native
acceptance adds a 100,000-row table, recovers a lost publication acknowledgement,
recovers a lost subscription-refresh receipt, restarts during copy and verification,
exercises concurrent updates/inserts/deletes,
and checks signed reader access while preserving table, publication, subscription
and generation identities. It requires no dedicated host.

### Recover a new table

When verification reports a row mismatch in a newly added table, choose
**Preview recovery** in **Source schema changes**. The worker checks the original
source table identity, complete definition and primary key, plus the owned target,
publication and subscription identities. Previously available tables must retain
their native ready states; other new tables can continue copying, but must remain
tracked. Review the named table and explicitly approve replacement
of only its imported rows. A preview changes no imported data. Approval requires
the exact saved preview, current revision, fresh observation and qualified copy
capacity. Recovery shares the existing ten-attempt schema retry budget.

The worker verifies the affected table's read gate, withdraws that table from the
owned publication and lets PostgreSQL retire its subscription relation and active
copy worker through `REFRESH PUBLICATION (copy_data=false)`. PostgreSQL also
removes the temporary table-sync slot. Recovery confirms the worker and relation
are gone before removing old verification helpers and clearing any rows. An
active copy already has a false reader policy; recovery checks it without issuing
policy DDL that would wait behind the copy lock. After retirement,
`TRUNCATE ONLY ... RESTRICT` clears that owned table in the same
transaction as a private stage receipt. It is then republished and refreshed with
native initial copying enabled. The shared subscription, main replication slot,
publication identity, generation and healthy table storage remain in place.
Recovery never cascades into other tables or edits PostgreSQL system catalogs.

Durable stages recognize committed publication changes and native refreshes after
lost replies or worker restarts. Once rows are cleared, recovery cannot clear
them again after copying starts. The new attempt gets a fresh bounded verification
proof; other newly added tables can be reverified, but are never recopied by this
approval. Healthy baseline tables remain readable and syncing. New tables open
only after verification, the existing API refresh workflow and the final fenced
check all pass. Lost completion acknowledgement recovers the private passed proof.

Recovery also covers an unfinished initial copy of a newly added table. The
dashboard shows each new table's native phase, worker activity, and available COPY
row/byte counters. These counters describe one attempt, not verified totals or a
completion percentage. A private receipt preserves observations across worker
restarts and lost catalog updates. Five minutes without a higher observed row/byte
count or a later native phase marks a copy as having **no recent observed
progress**. Worker retries alone do not reset this clock. This is an attention
signal, not a claim that PostgreSQL reported a definite per-table error.

Select the stalled or untracked new table and choose **Preview table recovery**,
then approve its exact preview. Missing tracking after copy admission is eligible
without a row-comparison failure. Recovery is explicit and reuses the same durable
retirement/clear/republish/copy stages and ten-attempt budget. Each fresh copy resets
only that table's saved progress clock and uses actual headroom for one copy worker
within the unchanged qualified peak reservation. If native copying resumes or
finishes before durable recovery starts, the worker withdraws the stale recovery
request and continues ordinary copying or verification without clearing rows.

Changed source identity/schema/PK and missing tracking in another table need
other remediation or an explicit full resync. Previously available tables use
the separate live-table repair workflow below. A table-only refresh cannot safely
adopt another untracked table.
Shared replication loss and lost/excessive WAL still require full pipeline recovery.

Local native acceptance uses two 100,000-row tables, corrupts the new table,
rejects source drift after approval, loses publication/removal/copy/completion
acknowledgements and restarts during recovery. It checks signed reads and healthy
CDC updates while copy is stalled, proves the healthy table's storage is unchanged,
counts exactly one committed clear, and checks fresh proof plus API read gating.
Additional native acceptance blocks a real initial COPY inside PostgreSQL while
two source tables each contain 100,000 rows, retires its active worker and temporary
slot, loses the retirement receipt, restarts, and recovers missing copy tracking
using supported native DDL. Healthy signed reads and CDC updates continue with
unchanged storage. It counts one clear per approved attempt and confirms that an
approved recovery is withdrawn when COPY finishes before the next worker tick.
Recovery remains staging-only and unreleased.

## Cancel an approved new table's onboarding

In **Source schema changes**, choose an unfinished new table and select
**Preview onboarding cancellation**. Review the named table and explicitly
confirm stopping its sync, removing it from this pipeline and its API, and
retaining any committed imported rows privately. This is available while copying,
verifying or waiting for API refresh, including stalled copy and source-definition
failures. Previously available tables cannot be selected. Discarding an unapproved
selection remains a separate action.

Before approval, **Keep onboarding this table** dismisses the cancellation
preview and resumes its normal workflow. Approved cleanup resumes durably until
completion.

The preview binds the pipeline generation, exact addition approval, request,
source publication and subscription identities, and owned target table. Approval
requires that saved preview, the current revision and a fresh observation. It
needs no new copy capacity or retry attempt. Cancellation takes priority over
onboarding or table recovery; schema/recovery approvals cannot bypass it. An
addition that already completed privately before cancellation was previewed stays
complete, and the stale cancellation request is withdrawn.

A private intent is committed before removing the selected publication member.
PostgreSQL retires its relation, active copy worker and temporary sync slot through
native publication refresh. Other tracked new tables keep copying; previously
available tables keep their storage, read policies and CDC stream. Refresh refuses
to silently register another untracked table without a snapshot. Cleanup resumes
after restarts and recognizes committed publication/refresh changes after lost
replies. Shared replication loss and the existing retained-WAL limit still require
full pipeline recovery.

After native retirement, cleanup removes the old verification proof and moves the
owned target into the private `gregale_pipeline` schema, retaining committed rows
behind its false read policy. An interrupted COPY's uncommitted rows are rolled
back by PostgreSQL. No imported rows are automatically truncated or deleted.
Retained table/index names are unique; table comments preserve the original name,
generation and cancellation identity. The private schema grants no app-reader
usage and is excluded from the public API. Retained data remains in the destination
through later resyncs and pipeline deletion. A database owner can export or remove
it through SQL; this slice adds no automatic retention expiry or customer deletion
UI. At most 32 retained tables may accumulate per mirror database; cancellation
checks this limit before starting and again before the move.

Cancellation requests a distinct API refresh through the existing durable runtime
restart workflow. Only after that refresh completes does a fenced catalog update
remove the table from saved selection. Remaining additions keep their prior copy
admission state and progress clocks, then receive a fresh verification proof. If
none remain, the existing pipeline returns to live. A later re-add creates a new
table and approval/proof identity; it cannot reuse retained rows or a cancelled
proof. Cancellation does not change the source table or saved reader list.

Local acceptance cancels a real blocked COPY while another new table is copying,
loses source/native/completion acknowledgements, and restarts cleanup. It checks
healthy signed reads and CDC against a 100,000-row baseline, private row retention,
API exclusion, cancellation of the last addition, and a fresh 100,000-row re-add
followed by another cancellation. This remains staging-only and unreleased.

## Remove a live table

In **Manage pipeline tables**, select a live table and choose **Preview live-table
removal**. Review the exact preview and confirm stopping its sync, removing its API
access and retaining imported rows privately. This requires a running, freshly
observed pipeline with at least two tables and no unfinished schema change or
verification. To stop syncing the final table, use **Disconnect pipeline**.

**Keep syncing this table** dismisses an unapproved preview. Previewing and
dismissing leave table reads and syncing in place. Confirmation binds the saved
source baseline, pipeline generation, request, publication/subscription identities
and owned target table; stale revisions or an earlier dismissed preview cannot
approve removal.

The worker closes only the selected table's read policy and commits its durable
retirement intent in the same transaction. A failed fence rolls both back.
Cleanup then withdraws only that publication member, uses native refresh to retire
its subscription relation, and moves the owned table into private retention.
Remaining tables keep their read policies, storage and shared replication stream.
Removal requires no new copy capacity or retry attempt and does not reset the
pipeline generation. Shared replication loss and the retained-WAL limit still
require full pipeline recovery.

Cleanup resumes after restarts and lost acknowledgements. A distinct refresh of
the existing Data API runtime must complete before the saved selection changes.
The remaining tables become the new schema baseline; old verification proof is
retired. The private retention rules above also apply here: no automatic row
deletion, no app-reader access, at most 32 retained tables per mirror database, and
owner-managed export/removal through SQL. Later re-addition creates a fresh table
and proof rather than adopting retained rows. The source table is unchanged.

Local acceptance removes one 100,000-row live table while both selected source
tables receive updates, loses native and final catalog acknowledgements, and
restarts cleanup. It checks scoped read gating, signed healthy reads and CDC,
unchanged healthy storage/shared identities, private row retention, API exclusion
and rejection of final-table removal. This remains staging-only and unreleased.

## Repair an already-live table

When a manual verification finds a row mismatch in an existing table whose source
identity, schema and primary key are unchanged, the worker closes only that
table's read policy. Other selected tables keep their storage, read policies and
shared replication stream. The mismatch, saved full source baseline and owned
native identities are recorded privately in the same fenced transaction as the
gate. A failed transaction is retried; a lost catalog acknowledgement cannot
reopen the affected table or discard its mismatch.

In **Source schema changes**, choose the mismatched table, select **Preview table
recovery**, and confirm replacing only that table's imported rows. The preview
does not change imported data. It binds the original mismatch, full unchanged
source definition, pipeline generation, owned target OID, publication/subscription
identities and attempt. Approval requires the exact preview, current revision,
fresh observation and qualified copy capacity. Schema approval cannot authorize
a repair, and other tables cannot be selected through this action.

Repair uses the existing durable withdrawal, native relation retirement,
transactional clear, republish and copy stages. A clear commits with its private
stage receipt, so a lost reply cannot clear rows again after copying starts.
Only the affected table receives a fresh native copy and bounded verification.
Its policy stays closed through API refresh and the final verification fence.
Proof, private completion and reopening commit together; catalog completion can
be recovered after a restart. Pipeline generation and selected table names stay
unchanged. The proof names its one-table scope; it does not mark the entire
pipeline verified.

Repair reuses the reserved copy peak, actual native headroom checks and existing
ten-attempt schema budget. Changed source definitions, shared replication loss,
lost tracking in another table or excessive/lost WAL still require full recovery.
No repair starts automatically. Stop/retry of repair verification preserves the
closed table gate. Explicit full resync and pipeline disconnect remain available.

Local acceptance uses two 100,000-row live tables with ongoing source updates. It
corrupts a previously checked range, rolls back the first quarantine transaction,
loses quarantine/native-copy/completion acknowledgements, blocks COPY and restarts
the worker. Signed reads and CDC continue for the other table, whose storage and
shared replication identities stay unchanged. It checks exact approval, one
committed clear, source-drift rejection, fresh scoped proof and read reopening.
This remains staging-only and unreleased.

## Review and approve schema additions

In **Dashboard → Data connections → Source schema changes**, inspect the saved
before/after column diff. Added supported builtin columns, including defaults,
stored missing values and `NOT NULL`, can use the approved backfill workflow.
Identity columns and primary-key changes remain unsupported.
Drops, renames, replacements, changes to existing definitions and unsupported
types require explicit recovery with resync. Some builtin type modifiers remain
unsupported for automatic additions. Older mirrors acquire a full metadata
baseline on their first unchanged successful observation; drift without that
baseline requires resync.

Approval binds the exact source definition, pipeline generation and current
revision. Confirm that every saved reader may see the added fields. A different
source definition requires a fresh preview and approval. While approval is
pending, replication is disabled and RLS denies reads (queries return no rows).
The existing publication and slot are retained within the 1 GiB WAL budget;
expired or excessive retained WAL still requires resync. Source changes reverted
before application withdraw their pending approval once any earlier migration
has completed. Conflicting unfinished migration receipts require explicit resync.

The worker briefly locks selected source tables against writes and DDL, checks
the approved definition again, and adds the columns to the owned destination
in one transaction with a recovery receipt. The existing table, subscription,
slot and generation are preserved; no full table recopy occurs. Native
replication resumes. Each bounded verification batch reads actual source values
under the source write lock and updates only the newly approved columns before
checking whole-row equality. Source defaults and their expressions are never
installed or executed on the mirror. This also backfills historical defaults
removed and rewritten before inspection, which CDC alone cannot reconstruct.
Missing mirror rows and differences in existing columns still require resync.
Larger tables continue through saved batches. Busy ranges are
checked again when they change; reads stay gated until a final consistency
check succeeds. Work limits and cancellation never reopen schema reads.

For example, `ALTER TABLE orders ADD COLUMN status text NOT NULL DEFAULT
'pending'` can preserve the current mirror and subscription. The destination
column starts nullable, historical values are copied from the source, and
`NOT NULL` is enforced before API runtime refresh. Constraint DDL and its phase
receipt commit together; lock/query timeouts retain progress and retry. The
constraint scan uses the same two-second statement budget and may remain blocked
on large or busy mirrors. The dashboard reports rows updated in new columns,
including repeated backfills. Stopping retains already applied batches; an
explicit retry starts a fresh proof and safely revisits them. A new source
definition during an unfinished backfill requires explicit recovery rather than
treating partially populated columns as a complete baseline.

For each bound app, a durable refresh operation invalidates snapshots and asks
the ordinary scheduler to replace runtimes in every live scope. A saved operation
ID prevents lost acknowledgements from restamping freshness or queuing duplicate
replacements. Reads remain gated until every operation reports completion and a
final source definition/data check passes under a source write/DDL lock. The new
runtime rebuilds its Data API schema contract and PostgREST cache on startup.
No VM lifecycle changes or separate hosting tier are introduced. Refresh failure
retains the migrated mirror; fix the app configuration or runtime failure and
choose **Retry schema workflow**. At most 100 bound apps and 10 explicit retries
per schema change are supported. Custom contract acceptance policies must remain
compatible with the new runtime contract or refresh will remain blocked.

These controls are dashboard-only in this slice. They remain operator-approved,
staging-only, with production admission disabled. Local native tests cover
approval changes, lost DDL acknowledgement, schema changes during refresh,
contract renewal, RLS/write denial and historical-default backfill. Local
100,000-row required-column acceptance also covers lost leases, stop/retry,
worker restart, concurrent updates/key moves/deletes/inserts and read gating.
They do not provide live-provider or deployed-host qualification.

## Resumable verification

Both `gregale pipeline verify` and approved schema additions use the saved-range
verifier. The dashboard shows checked rows/bytes (including repeated checks),
saved ranges, scanned tables and ranges waiting for another check. Checkpoints
belong to the pipeline generation, exact schema definition and source storage
version; schema retries
start a new attempt. A restarted worker recovers the committed destination
checkpoint even when its last catalog progress update was lost. Customer keys
and range boundaries stay in the private destination schema, outside dashboard
and control API metadata. CLI verification continues to report the existing
`pending`, `passed`, `mismatch` and `inconclusive` results.

The worker briefly obtains a source SHARE lock, waits for native replication to
acknowledge a sampled WAL insertion checkpoint, and locks the destination table
against writes while comparing one primary-key range. Comparison settings make
text ordering, timestamps, numbers and byte output consistent. A destination
`ALWAYS` trigger marks previously checked ranges dirty within the same
transaction as every replicated or direct destination write. Primary-key changes
invalidate both the old and new ranges; a replicated truncate invalidates every
range. Ranges that grow are split into smaller batches. This adds no source
triggers, heartbeat rows, extra replication slots or source-side journal.

Source physical rewrites can change values without normal CDC row events. Each
run also records opaque heap/TOAST/storage fingerprints, checks them under each
source lock and checks them again at finalization. A rewrite discards the old
ranges and starts a new run for the same approved intent; the dashboard reports
these restarts. This requires no new schema approval when the column definitions
are unchanged. Cancellation remains effective across these automatic restarts.

A set of independent range checks is not itself proof of equality. Before a
successful result, the worker briefly locks all selected source tables, waits
for native acknowledgement and locks all mirrored tables. It rechecks up to two
remaining dirty ranges under these locks, validates that canonical boundaries
form a complete, gap-free partition, and requires no dirty ranges to remain.
Missing or disabled tracking requires explicit recovery. Schema approval, data
verification and runtime refresh must all be complete before RLS opens; opening
reads, saving the completed proof and removing temporary helpers share one
destination transaction. Manual verification also removes its helpers on
completion. Lost completion acknowledgements reuse the saved proof and original
completion time.

Batches use 2,000 rows, a 4 MiB normal byte budget and at most four batches per
worker tick. One row may use up to 16 MiB. At most 4,096 ranges are retained per
pipeline, including ranges created by splits; this replaces the old per-table
size cap with a bounded amount of persistent verification metadata. Source and
destination lock acquisition is limited to 250 ms, individual statements to two
seconds, and replication catch-up waiting to two seconds. These are per-operation
bounds, not a promise of a two-second total write pause. Writes pause during a
batch or final fence; there is no source snapshot or lock held between worker
ticks. A server-side idle transaction timeout of 60 seconds bounds abandoned
verification write locks after worker/network failure. Text primary keys use C ordering, which may require an appropriate source
index to avoid slow scans on large tables. Repeated timeouts retain progress;
sustained write churn can delay completion. Production performance qualification
remains outstanding.

Choose **Stop verification** to cancel the current run. Cancellation is bound to
the account, revision and exact run, and removes temporary tracking on the next
worker reconciliation. It retains the mirror, applied schema and replication
resources. For a schema workflow, reads stay gated; choose **Retry schema
workflow** to start a fresh check. A stopped or limited manual check can use
**Retry verification**. Stopping a check does not cancel an already queued API
runtime refresh. An actual mismatch still requires an explicitly confirmed
resync, and resync clears verification progress. Cleanup also removes helpers
when the pipeline generation is retired.

Local acceptance includes a 100,000-row source with cancellation, worker restart,
lost catalog progress, changes behind the cursor, primary-key moves, inserts,
deletes, concurrent writes during finalization, signed API reads, corruption
of a previously checked range, and data-preserving/value-changing source rewrites
while a check is incomplete. Separate PostgreSQL tests cover composite text/time
keys, canonical boundaries, range splitting, replica writes/truncation, gaps in
coverage and disabled tracking. No dedicated host or live-provider credentials
are required for these checks; this feature remains unreleased and staging-only.


## Change readers on an existing pipeline

In **Dashboard → Data → Manage reader access**, enter the complete allowed JWT
subject list, one subject per line, and choose **Preview reader changes**. The
preview names every grant, every revocation, and the selected tables. Confirm
that allowed readers can read **every row in every selected table** before
applying it. This is whole-table access, not a per-customer row filter. The
existing limit of 1–32 unique subjects still applies. A retired generation must
be resynced before its readers can be edited.

Previewing does not change access. Confirmation saves an approved request;
**approved** means it is waiting for acknowledgement from the mirror, and
**applied** means the mirror's committed update has been acknowledged. The
confirmed applied-reader list remains visible while an update is pending.
A pipeline or table-selection change invalidates an unapproved preview: discard
it and preview again. Approved changes cannot be discarded because the mirror
may already have committed them. A full resync waits until the approved reader
change is acknowledged; pause and disconnect remain available. New schema and
table approvals also wait for the acknowledged reader set.

Reader changes require no row copy, resync, source connection check, new copy
capacity, or API deployment. Native replication continues. Only already-open
read policies change. Tables closed for initial copying, schema migration,
verification, or repair stay closed, and retained private tables stay excluded.
When a workflow later opens a table, it uses the committed reader list, including
after a worker restart or a lost acknowledgement. Revocation governs subsequent
database reads after commit; already-running transactions may finish under their
existing snapshot. Changes can also be applied while replication is paused or
the source is unreachable, provided the worker can open the saved credential and
reach the mirror.

Local acceptance revokes Alice and grants Bob during a real live-table repair
COPY with mixed-table source writes. Signed Data API requests prove that Alice
loses access, Bob can read the healthy table, and both subjects remain unable to
read the quarantined table. A lost catalog acknowledgement and worker restart
preserve the new access; the repaired table later opens only for Bob. Native
publication/subscription identities, the main slot, healthy table storage, and
pipeline generation are preserved. Production enablement remains disabled.

### Pipeline health webhooks (local/staging)

Each pipeline can opt in to one HTTPS webhook from **Health notifications** in
`/dashboard/data`. Supply an HTTPS URL with a DNS hostname on port 443 and a randomly generated
32–256 byte signing secret that your receiver also stores. Confirm the metadata
being sent. The complete URL (including query parameters) and secret are encrypted
with the pipeline recipient key and are never displayed again. Replacing or
disabling a destination cancels its queued events. A request already in flight
may still complete. Disabling remains available when pilot admission closes.

An independent `apid --data-pipeline-alert-worker` process reads the control
catalog; it does not connect to source or mirror databases and does not depend
on the replication worker's source credentials, capacity checks, or startup.
Monitoring and delivery run separately, so an unavailable webhook receiver or
missing decryption key cannot stop incident detection. The supplied systemd unit
is installed **without enabling it** and remains gated to the staging pilot.
Operators must run both workers for notifications. For a configured staging
pilot, start the independent monitor explicitly with
`systemctl enable --now faas-apid-data-pipeline-alert-worker` and inspect
`journalctl -u faas-apid-data-pipeline-alert-worker`. Startup does not require the
pipeline decryption files; delivery loads the configured key paths lazily.
Stopping both workers or losing the control catalog cannot be detected by this
monitor itself.

Conditions must persist for two minutes after detection. Monitoring is checked
every 15 seconds, with a 90-second observation freshness allowance:

| Code | Evidence |
| --- | --- |
| `monitoring_stale` | A running pipeline has no current worker observation, including a stopped replication worker. |
| `sync_blocked` | A fresh, current-revision observation says blocked or resync required. |
| `retention_pressure` | A fresh observation reports retained WAL at or above 80% of the 1 GiB retention ceiling, including intentionally paused pipelines. |
| `apply_unconfirmed` | A live/catching-up pipeline retains at least 16 MiB of WAL and lacks an applied checkpoint from the last 90 seconds. This signals uncertain apply progress, not proven row lag. |

An old applied checkpoint on an idle source alone does not trigger an alert.
Initial copy and ordinary in-progress controls are not blocked-sync incidents.
One incident stays open through condition changes. A fresh, current-revision live
observation with no alert condition must persist for 30 seconds before a recovery
is queued. Pausing does not claim recovery; disconnecting cancels pending events
and disables the destination.

The webhook uses the existing Gregale JSON envelope:

```json
{
  "id": "event-uuid",
  "occurred_at": "2026-10-17T17:00:00Z",
  "rule": "data_pipeline.health.incident",
  "rule_name": "data_pipeline.health.incident",
  "payload": {
    "version": 1,
    "pipeline_id": "pipeline-uuid",
    "incident_id": "incident-uuid",
    "kind": "incident",
    "code": "monitoring_stale"
  }
}
```

Recovery uses `data_pipeline.health.recovery`, `kind: recovery`, a new event ID,
and the original incident ID/code. Payloads exclude source URLs, secrets,
customer rows, table names, reader identities, and raw database errors.

Verify `X-Faas-Webhook-Signature` (`sha256=` followed by lowercase hex) as
HMAC-SHA256 over the exact bytes
`<X-Faas-Webhook-Timestamp>.<X-Faas-Delivery-Id>.<raw request body>` using your
signing secret. Compare signatures in constant time and enforce a replay window
against the attempt timestamp. Deduplicate by delivery ID. Retries retain the
same event ID and body, while their signing timestamp is refreshed;
`X-Faas-Webhook-Attempt` reports the recorded attempt number.

Delivery is **at least once**: an accepted request whose acknowledgment is lost
can repeat. The durable queue preserves incident-before-recovery attempt order;
recovery can proceed after an incident is delivered or its retry budget is
exhausted. Only 2xx responses acknowledge an event. Redirects are rejected without
following them. Other non-408/429 4xx responses and denied egress are terminal;
network failures, 408/429, and server errors retry up to seven recorded attempts.
Backoff starts at 30 seconds and doubles with jitter; bounded `Retry-After` is
honored up to six hours. Each HTTP attempt has a ten-second deadline and a
30-second claim lease. Public egress restrictions and standard TLS verification
apply on every attempt.

The dashboard shows event IDs, incident linkage, status, attempt count, HTTP code,
and sanitized result codes. Response bodies and raw errors are never stored.
Each pipeline retains up to 100 terminal deliveries and 100 pending events, with
the newest 100 shown. If the pending queue fills, monitoring defers the next
transition until space is available. Replacing a destination starts a new alert
baseline and does not replay history to the new endpoint. A still-active
condition is eligible again after the ordinary debounce.
