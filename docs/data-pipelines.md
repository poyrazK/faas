# Managed Postgres data pipelines

Pipelines copy selected tables from an external PostgreSQL 16 database into a
dedicated Gregale managed database and keep them synchronized. You can expose
the mirror through a read-only [Data API](data-api.md). This feature is available
only to operator-approved staging accounts on Hobby, Pro, or Scale.

## Connect and read

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

The command registers `orders-source`, provisions `orders-mirror`, requests the
copy, waits for fresh live status, then configures and deploys `orders-data` with
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

Generate the read contract with `gregale data-api types orders-data --output
./database.types.ts`. Use `.schema('public')` when selecting mirrored tables with
`createDataClient` from `@gregale/data`, and authenticate using the selected Auth
app. See [Data API onboarding](data-api-onboarding.md) for token and client setup.

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
pause or outage. Schema changes, invalidated slots, or proven drift result in
`resync_required`, stop replication, and deny mirror reads. Explicit `resync`
starts a new generation and replaces only the preceding generation's owned
tables. It can interrupt reads and requires enough time for a full new copy. Existing
Data API queries may return no rows while the new generation denies reads.
After a schema change, run `gregale data-api refresh orders-data --wait` and
regenerate the client types before using new columns.

Verification is limited to 10,000 rows and 16 MiB per selected table. Equality
is reported only when the source WAL position remains stable and acknowledged
while both digests are read. Busy, larger, or catching-up sources report
`inconclusive`; `verify --wait` exits unsuccessfully in that case so automation
does not treat it as proof of equality. Retry after the source becomes idle.

Deleting a pipeline removes its publications, subscriptions, and replication
slots, while keeping destination rows. An unreachable source leaves deletion
pending; restore connectivity or rotate credentials and retry. Active source
connections cannot be removed. Database/account deletion requires pipeline
cleanup first. Historical list output is bounded to 100 records; active records
remain visible. Connection URLs and private worker credentials never appear in
list/status responses.

Limits per account are 10 source connections and 3 active pipelines, with at most
32 read subjects per pipeline and 1,000 resync generations. The worker checks a
pipeline about every 15 seconds. Native replication connections and read-client
pools do not yet have qualified capacity models, so the inherited connection
budget remains unknown with explicit reasons.

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
unit before configuring the pilot and its keys. An outage exceeding the source
retention window requires resync, rather than continuing through a WAL gap.

For local acceptance, install the runtime's pinned Node dependencies with
`npm ci --prefix cmd/gregale/templates/data-api`, then run `make
test-data-pipeline` with Docker, Go, Node, and OpenSSL available. Containers and
credentials are disposable. These tests do not qualify a live provider or enable
production. See [ADR-1456](adr/1456-managed-postgres-data-pipelines.md).
