# Building Gregale-owned object storage

The first runnable slice is `object-storaged`: a private S3-compatible node
that stores bytes on our disks. The existing public S3 gateway continues to own
customer credentials and bucket permissions. Its generic S3 adapter can target
this node using the same provider configuration shape used for R2.

Architecture and limits: [ADR-1542](adr/1542-owned-object-storage-first-node.md).
Durable receipt protocol: [ADR-1543](adr/1543-owned-object-storage-write-receipts.md).
Multipart protocol: [ADR-1544](adr/1544-owned-object-storage-multipart.md).
Inventory and disk observations: [ADR-1545](adr/1545-owned-object-storage-inventory.md).
Checksum scrubbing: [ADR-1546](adr/1546-owned-object-storage-scrubbing.md).
Offline backup and restore: [ADR-1547](adr/1547-owned-object-storage-backups.md).
Replication journal and followers: [ADR-1548](adr/1548-owned-object-storage-replication-journal.md).
Planned primary switchover: [ADR-1549](adr/1549-owned-object-storage-primary-switchover.md).
Replica-confirmed acknowledgements: [ADR-1550](adr/1550-owned-object-storage-replica-durability.md).
Journal retention and compaction: [ADR-1551](adr/1551-owned-object-storage-journal-compaction.md).
Sustained traffic qualification: [ADR-1557](adr/1557-owned-object-storage-soak-qualification.md).
Repeatable local qualification: [ADR-1552](adr/1552-owned-object-storage-local-qualification.md).
Replication change waits: [ADR-1553](adr/1553-owned-object-storage-replication-waits.md).
Publication preparation and work budgets: [ADR-1554](adr/1554-owned-object-storage-publication-preparation.md).
SSH host qualification: [ADR-1555](adr/1555-owned-object-storage-ssh-qualification.md).
Automatic journal maintenance: [ADR-1556](adr/1556-owned-object-storage-automatic-compaction.md).
Write admission: [ADR-1559](adr/1559-owned-object-storage-write-admission.md).
Host/storage preflight: [ADR-1560](adr/1560-owned-object-storage-host-storage-preflight.md).
This is a development prototype with one writable primary. It is not enabled in production.

## Development feature flags

All private flags below default to off. They accept `1`, `true`, `yes` or `on`,
ignoring case and surrounding whitespace; unset, false and invalid values keep
the gate closed. They are read at startup, so changes require restarting the
relevant process. A missing required flag identifies itself in the startup error.

| Operator environment flag | Enables | Also requires |
| --- | --- | --- |
| `FAAS_OBJECT_NODE_ENABLED` | Master permission for private development capabilities | An explicit capability below |
| `FAAS_OBJECT_NODE_SERVER_ENABLED` | Starting the loopback-only `object-storaged` daemon | Master flag |
| `FAAS_OBJECT_NODE_BACKENDS_ENABLED` | Loading a backend with `owned_node_receipts` through shared provider configuration | Master flag and explicit backend configuration |
| `FAAS_OBJECT_NODE_SETTLEMENTS_ENABLED` | Loading a backend configured with `owned_node_settlements` for new recovery acknowledgement enrollment | Master and backend flags, plus `owned_node_receipts` |
| `FAAS_OBJECT_NODE_USAGE_COUNTERS_ENABLED` | Loading a backend configured with `owned_node_usage_counters` for logical usage observations | Master and backend flags, plus `owned_node_receipts` |

The daemon checks master and server flags before creating or opening its data
directory. Shared provider loading checks every owned backend's required flags
before constructing any provider or reading provider credentials. Enabling a
flag alone does not select a backend or turn on its optional configuration.
The server and backend flags are independent: a test node can run without any
gateway enrollment, and a gateway can connect to an already-running node.
The settlement and counter flags control gateway/apid integration; they do not
disable the node's internal receipt tracking, accounting invariants or recovery.

Keep these flags unset on customer-serving installations. They are absent from
customer capabilities, UI, API/SDK provider choices and normal deployment
defaults. The existing `s3_enabled` runtime switch separately controls the public
provider-backed storage surface and defaults to false. GCS and managed S3/R2
configuration does not require any private-node flag. Enabling development
flags does not grant production enrollment or change the loopback restriction.

The explicitly invoked qualifier strips all inherited private flags, then sets
only master and server in its disposable node child. Its offline follower/control
children and SSH clients receive no private feature flags. It does not enable a
live deployment or alter the parent environment. See
[ADR-1570](adr/1570-owned-object-storage-capability-gates.md), which refines
[ADR-1569](adr/1569-owned-object-storage-development-gate.md).

## Run locally

Use the Go toolchain pinned in `go.mod`. Choose a private data directory whose
parent already exists, on a local Linux filesystem supporting atomic rename and
file/directory fsync. The service must be the only process managing those files;
an exclusive process lock enforces that rule. Do not use a shared/network mount.
New data directories must be empty. A format marker prevents startup recovery
from operating on an unrelated directory.

Supply a dedicated node credential through the environment. It has access to all
physical buckets on the node and must never be given to customers:

```sh
export FAAS_OBJECT_NODE_ENABLED=1
export FAAS_OBJECT_NODE_SERVER_ENABLED=1
export FAAS_OBJECT_NODE_ACCESS_KEY_ID=local-development
export FAAS_OBJECT_NODE_SECRET_ACCESS_KEY="$(openssl rand -hex 32)"
go run ./cmd/object-storaged --data-dir ./object-data
```

The node listens on `127.0.0.1:19000`; non-loopback listeners are rejected.
Defaults reuse the existing transfer constants: 100 MiB per PUT, four concurrent
writes, and a 1 GiB free-space floor. Flags can tune these within the existing
hard ceilings. PUT and part uploads use the existing upload-spool ceiling for
aggregate reservations; multipart assembly uses the object-size ceiling.
Reads/writes have bounded server timeouts. The data
directory must remain private to the daemon's OS user, including after restart.

Use a separate shell with the same credentials to exercise the node directly
with an installed AWS CLI:

```sh
export AWS_ACCESS_KEY_ID="$FAAS_OBJECT_NODE_ACCESS_KEY_ID"
export AWS_SECRET_ACCESS_KEY="$FAAS_OBJECT_NODE_SECRET_ACCESS_KEY"
export AWS_REQUEST_CHECKSUM_CALCULATION=WHEN_REQUIRED
export AWS_RESPONSE_CHECKSUM_VALIDATION=WHEN_REQUIRED

aws --endpoint-url http://127.0.0.1:19000 --region us-east-1 \
  s3api create-bucket --bucket owned-test

aws --endpoint-url http://127.0.0.1:19000 --region us-east-1 \
  s3api put-object --bucket owned-test --key hello.txt --body ./hello.txt

aws --endpoint-url http://127.0.0.1:19000 --region us-east-1 \
  s3api get-object --bucket owned-test --key hello.txt ./hello.download.txt

aws --endpoint-url http://127.0.0.1:19000 --region us-east-1 \
  s3api list-objects-v2 --bucket owned-test
```

Create `hello.txt` before the upload. Stop and restart the daemon against the
same directory, then repeat the download to verify persistence. These CLI
examples use `s3api` single PUT. Multipart examples follow below.
The checksum environment settings avoid SDK streaming-trailer framing, which
the private node currently rejects. The public gateway supports that customer
framing and forwards bounded bodies to the node.

## Multipart uploads

The node supports initiate, upload part, list parts/uploads, complete and abort.
With the same credentials and checksum environment above, an installed AWS CLI
can use multipart automatically for a large local file:

```sh
aws --endpoint-url http://127.0.0.1:19000 --region us-east-1 \
  s3 cp ./large.bin s3://owned-test/large.bin
```

For explicit resume, save the upload ID returned by `s3api create-multipart-upload`
and the ETag returned by each `s3api upload-part`. Restart the node against the
same data directory, use `s3api list-parts` with that upload ID, and upload only
the missing parts. Complete using the ordered part-number/ETag selection. Parts
remain invisible to ordinary object GET/HEAD/listing until completion succeeds.
UploadPartCopy and CopyObject remain unsupported.

Parts default to at most 64 MiB, non-final parts must be at least 5 MiB, and an
upload may select at most 10,000 parts. `--max-part-bytes` can tune the part cap
up to 5 GiB. `--max-multipart-bytes` can lower the 5 TiB total ceiling; existing
sessions retain their captured total-size cap. The gateway's configured limits
also apply. These ceilings do not promise that the local disk has that capacity.

Each assembly reserves free space for the entire output while the parts remain
on disk, then streams one verified part at a time. Part uploads and assemblies
share the node's write slots and free-space floor. Capacity pressure returns
SlowDown. Full-object CRC and supported composite checksum policies are verified
using the existing checksum contract.

A completion freezes its selection before assembly. Identical retries resume or
return its original result; changing its parts or conditions conflicts. A saved
conditional rejection is terminal for that session. Abort and expiry cannot
delete parts during an active assembly, and abort never deletes a completed
object. Unfinished uploads expire after 24 hours; the daemon sweeps every
30 seconds with bounded candidate, part and time budgets (32 candidate turns,
eight part directories and 25 ms by default). Large sessions can require several
ticks. Session tombstones and receipt metadata remain after part reclamation.

With `owned_node_receipts` enabled, gateway recovery confirms an exact saved
completion after overwrite, deletion or restart without uploading parts or
assembling again. Missing proof permits only replay of the already authorized
completion; it never establishes failure. Listing parts alone cannot settle an
individual uncertain gateway part writer, which retains its existing
conservative admission rules.

## Connect the existing gateway

In an isolated development registry, use a new backend ID and namespace:

```sh
export FAAS_OBJECT_NODE_ENABLED=1
export FAAS_OBJECT_NODE_BACKENDS_ENABLED=1
```

Set these in each gateway/apid process that loads this registry; the server flag
is required only in the separate `object-storaged` process.

```json
{
  "id": "owned-local",
  "driver": "s3",
  "region": "us-east-1",
  "namespace": "owned-local-development",
  "endpoint": "http://127.0.0.1:19000",
  "s3_region": "us-east-1",
  "path_style": true,
  "owned_node_receipts": true,
  "access_key_env": "FAAS_OBJECT_NODE_ACCESS_KEY_ID",
  "secret_key_env": "FAAS_OBJECT_NODE_SECRET_ACCESS_KEY",
  "allow_http": true,
  "allowed_origins": []
}
```

This is a backend entry inside the existing registry, not a complete registry.
All components using it must run on the node's host, with the node credential in
their own environment. Keep `allowed_origins` empty: provider-side CORS
configuration is outside this slice. Browser CORS qualification is deferred.
Keep the customer endpoint, region, bucket ownership and placement protections
unchanged. Do not repurpose an existing backend ID or switch an existing bucket
to this node: no data migration is implemented.

`owned_node_receipts` selects the private historical-outcome protocol and is
part of immutable placement identity. Leave it absent for managed S3 providers.
Do not toggle it on an existing backend/bucket: the changed fingerprint fences
access. Use a fresh development backend and bucket for this mode. Do not declare
encryption or Object Lock capabilities on this prototype backend.

The existing accounting and enablement requirements still apply. This node does
not yet produce authoritative usage reports or pass the full provider
qualification suite. The configuration above demonstrates the local connection;
it is not authorization or instructions to enable customer provisioning.

## Inventory, reconciliation and metrics

The node exposes private inventory queries signed with the operator credential.
With a curl build supporting AWS SigV4, inspect node-wide disk usage:

```sh
curl --fail --silent --show-error \
  --aws-sigv4 'aws:amz:us-east-1:s3' \
  --user "$FAAS_OBJECT_NODE_ACCESS_KEY_ID:$FAAS_OBJECT_NODE_SECRET_ACCESS_KEY" \
  --header 'X-Amz-Content-Sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855' \
  'http://127.0.0.1:19000/?gregale-inventory=usage'
```

Use the same signed GET with these paths for paginated inventory:

| Path | Contents |
| --- | --- |
| `/?gregale-inventory=buckets&max-keys=1000` | Physical buckets and incarnations |
| `/owned-test?gregale-inventory=objects&max-keys=1000` | Committed keys, blob identities, sizes, ETags and stored SHA256 |
| `/owned-test?gregale-inventory=uploads&max-keys=1000` | Unfinished upload identities, selected part bytes/counts and expiry |

Pass `next_cursor` back as the URL-encoded `cursor` parameter until it is empty.
Keep every page's generation, snapshot and observation time together. A mutation,
restart or expired cursor returns `InventoryChanged`; discard all partial totals
and start again. Inventory scans validate referenced file types and lengths.
They retain a bounded page but scan the catalog while holding a read lock, so
large scans can delay publication. Missing references fail the scan. Inventory
does not rehash all content or prove an uncertain write's outcome.

For a backend with `owned_node_receipts` enabled, apid uses these coherent pages
in its existing inventory/accounting worker and capacity reconciliation jobs.
Current committed bytes are separate from unfinished parts and physical disk
consumption. Failed or mixed scans leave the previous observation intact.
Ordinary scans preserve outstanding admission holds; the existing fenced
capacity job reports before/after/reclaimed capacity after its readiness checks.

The node-wide usage response includes apparent file bytes, allocated file blocks,
metadata and receipt bytes, staging, trash and unreferenced data. These are disk
observations; trash and receipts overlap other file categories. Physical bytes
are sampled while staged uploads and scrub worklists can change. Available space covers
the whole filesystem, including other files and unlinked open downloads.

`catalog_healthy`, error/warning counts and up to 100 relative-path diagnostics
describe missing/wrong-sized blobs, bad metadata, unexpected file types and
retained/unreferenced files. `issues_truncated` signals additional diagnostics.
Auditing never deletes files, follows symlinks, settles receipts or changes a
quota. Keep diagnostic evidence for inspection; existing startup recovery has
its own documented cleanup rules.

The daemon starts a checkpointed audit immediately and waits one minute after
each completed cycle. [Incremental audit budgets](#incremental-inventory-audits)
bound its work between opportunities for publication. Explicit complete
inventory/accounting requests retain their existing 45-second request budget.
Cached aggregate metrics are available on `127.0.0.1:19001/metrics`; use
`--metrics-listen` to change that loopback address or an empty value to disable
the metrics listener. Scrapes perform no disk scan and carry no per-key or
per-bucket labels. Useful metrics are:

- `faas_object_node_inventory_bytes{kind="available"}` and kinds `committed`,
  `multipart`, `staging`, `receipts`, `allocated` and `unreferenced`.
- `faas_object_node_inventory_items{kind="multipart_uploads"}` and kinds
  `objects`, `errors` and `warnings`.
- `faas_object_node_catalog_healthy`,
  `faas_object_node_inventory_last_success_timestamp_seconds` and
  `faas_object_node_inventory_scans_total{outcome}`. Alert when the healthy
  timestamp is zero or older than the existing 15-minute inventory freshness
  window, or catalog health is zero. Failed scans retain the last complete
  observations and do not refresh the healthy timestamp.
- `faas_object_node_active_writes`, `faas_object_node_reserved_bytes` and
  `faas_object_node_requests_total{method,status_class}` for concurrency,
  capacity pressure and HTTP failures.

These snapshots and process-local HTTP metrics do not supply cumulative
byte-hours, durable requests, egress or costs. They are not written as monthly
provider usage reports. The existing gateway-safety development mode and its
billing-off requirements still apply; production qualification remains pending.

## Incremental inventory audits

[ADR-1567](adr/1567-owned-object-storage-incremental-audits.md) introduces
checkpointed turns; [ADR-1568](adr/1568-owned-object-storage-mutation-aware-audits.md)
preserves verified progress while writes continue. Defaults are
`--inventory-audit-records=64`, `--inventory-audit-time=25ms` and
`--inventory-audit-backlog-records=4096`. Maxima are 256 work units, 100 ms and
65536 queued identities, with a separate 16 MiB queue byte bound. A work unit
includes a source record, projection row, physical entry, reconciliation, cleanup
row or traversal boundary. Active cycles resume after 10 ms. Publication can
proceed between steps; metadata I/O or one bounded record can exceed the soft
time budget.

Primary startup rebuilds an independent source identity registry from recovered
buckets, selected objects, sessions, active parts and receipts. A process-held
key and accumulator anchor completeness; saved derived rows cannot assert it.
Each cycle discovers physical records, traverses the registry, checks projection
rows in both directions and maintains an independently checked ledger. Durable
source publication updates the registry and a coalescing change queue in the
serving projection's transaction. Repeated hot-key writes retain one pending
identity. Changed records and affected generations are checked again without
discarding other progress. Upload identities include their active part aggregate;
terminal transitions subtract it without scanning every part.

Ledger changes, consumed queue entries and the bounded checkpoint commit
atomically. Worker interruptions resume the last committed step. Overflow
records a coverage gap without rejecting an already durable source write;
bounded turns discard the queue and establish a new baseline. Tracking gaps,
corruption or projection failures disable indexed operations. Startup recovery,
restore and promotion discard all saved coverage and rebuild from source.
Backups and replication omit the derived database. Restart to rebuild after a
tracking failure; do not edit source records or checkpoints to clear it.

Only a completed baseline with no remaining changes and matching current source
coverage publishes a healthy observation. Its snapshot describes the catalog at
completion. Freshness credits the cycle's start time, preserving the 900-second
coverage limit. Canceled, failed, incomplete and overflowed coverage never extends
freshness. Source damage withdraws health immediately; newer failed evidence
cannot be replaced by an older cycle. Physical bytes remain samples while
streams and cleanup run.

Monitor cached `faas_object_node_inventory_audit_*` metrics: `pending_records`,
`backlog_age_seconds`, `reconciled_total`, `overflows_total`, running state,
per-step work, examined units, restarts, cycles, failures and timestamps. Backlog
age conservatively starts with the first change since the queue was last empty.
Scrapes perform no filesystem or database reads. Inventory gauges retain their
last complete totals during a running cycle; partial totals cannot become
accepted accounting observations.

Completion requires validation capacity above the incoming change rate. Queue
coalescing helps hot keys but cannot guarantee liveness at arbitrary traffic.
Measure publication latency and queue growth before increasing budgets. Expired
coverage fails closed and preserves accounting and uncertain-write holds. Derived
rows and bbolt allocation can retain high-water disk space until bounded cleanup
or a startup rebuild. Explicit usage requests, source inventory, fenced capacity
recovery, backup verification and checksum scrubbing keep their existing paths.

## Checksum scrubbing

The daemon now verifies actual committed object and unfinished multipart part
contents against their stored SHA256. It starts a bounded worklist immediately
when there is no saved cycle, resumes an interrupted cycle, and waits 24 hours
after completion before the next automatic cycle. Objects added after a worklist
was captured wait for another cycle.

By default the worker reads at most 8 MiB of blob contents per one-second tick,
using a 64 KiB copy buffer and at most 32 target completions per tick. Tune this
with `--scrub-bytes-per-second` (1 byte through 64 MiB). Worklist construction and
validation, metadata reads and checkpoint writes are outside this blob read
budget. Worklists are capped at 64 MiB, reserve ordinary node write capacity,
and require the existing 1 GiB free-space floor. Construction holds the catalog
read lock and has a 45-second work budget. A node beyond those prototype limits
reports a failed scan; it does not report complete coverage.

Use the signed curl request from the inventory section with these methods/paths:

| Method | Path | Result |
| --- | --- | --- |
| GET | `/?gregale-inventory=scrub` | Durable run state, counts and byte progress |
| POST, empty body | `/?gregale-inventory=scrub` | Start a new worklist immediately; an active run returns 409 |
| GET | `/?gregale-inventory=scrub-findings&max-keys=1000` | Retained findings; pass `next_cursor` as `cursor` to continue |

The POST response describes a running scan. The background worker performs the
verification. `--scrub=false` stops that worker and preserves its progress;
manually started work also waits until the worker is enabled again.

Progress lives in `scrub/progress.json` and a private UUID worklist. Large-file
checkpoints retain a SHA256 state and offset tied to device, inode, size, mtime
and ctime. Changed files restart verification at zero. Overwritten/deleted blobs
and replaced/aborted/expired/completed parts become stale targets instead of
inheriting another blob's finding. Each new cycle hashes from the beginning.
The pass verifies bytes observed during its reads; corruption after a prefix was
read can require the next full pass to detect. Its completion is not a guarantee
about all current data at one instant.

Findings record checksum mismatch, missing blob, wrong size or invalid file type
against an exact bucket incarnation and blob identity. `current_known=true` and
`current=true` mean that identity still matches the catalog when the finding is
listed. `current_known=false` means catalog state could not be established;
`current=false` with a known state means the finding belongs to superseded data.
`state=verified` records a later successful verification of the same identity
while keeping the first corruption observation. Findings survive restart and
are retained after overwrite/deletion. They are committed before progress can
advance, so interrupted checkpoint writes cannot silently lose a finding.

Scrubbing reports damage. It does not repair, remove, quarantine or rewrite bytes,
change customer accounting, or alter historical write receipts. Transient read
errors retry without declaring the object verified. A malformed durable worklist,
checkpoint or finding stops startup instead of resetting the scan. Include the
entire `scrub/` directory in consistent backups. SHA-state serialization uses the
pinned Go toolchain; future incompatible encodings require an explicit migration.

The existing loopback metrics endpoint also exposes:

- `faas_object_node_scrub_targets{outcome}` for total, processed, verified,
  failed and stale targets in the current or last worklist.
- `faas_object_node_scrub_bytes{kind}` for read bytes and the current offset.
- `faas_object_node_scrub_timestamp_seconds{event}` for progress, completion
  and the last fully verified manifest. Only a cycle with no failures or stale
  targets advances `event="verified_manifest"`.
- `faas_object_node_scrub_running`, `faas_object_node_scrub_worker_ok`, and
  `faas_object_node_scrub_steps_total{outcome}` for operational health.

Alert on failed targets, worker errors or stalled running progress. Set scan-age
alerts for the data volume and configured read budget; these full-content scans
have a different cadence from metadata inventory. Counts describe a worklist's
history, so inspect finding identities and their current/known fields before
acting on a reported failure. Use the verified offline backup and restore
procedure below for recovery. Automatic repair remains future work.

Optional alert rules are provided in
[`pkg/promqlrules/object_node_scrub.yaml`](../pkg/promqlrules/object_node_scrub.yaml),
with synthetic alert fixtures. Load that file through the operator's Prometheus
`rule_files` configuration after adding the node's loopback metrics scrape.
It is not enabled by the deployment roles in this prototype. The rules warn on
failed manifest targets, persistent worker errors and a running scan that has
stopped checkpointing. They aggregate away instance/key/blob identities.

## Persistence and recovery

An upload streams to a bounded temporary file, verifies its exact length and
checksums, and fsyncs the bytes before publishing an immutable blob. The catalog
pointer is atomically replaced and fsynced before returning success. Concurrent
conditional PUTs check the current pointer under the same publication lock.
Failures after publication can have an uncertain outcome and return an error;
they never trigger deletion of a possibly referenced new blob.

Deleting or overwriting an object does not disrupt a download that already has
its file open. On startup the node reclaims abandoned staged files and obsolete
blobs, validates referenced blob sizes and refuses malformed catalog records.
This handles process interruption. The offline backups below provide recovery
from loss of the underlying disk. Stored SHA256 values are verified by the
background scrubber described above and during backup/restore.

Tracked writes carry the gateway's private UUID receipt. Before changing the
current pointer, the node fsyncs a commit intent; before acknowledging success,
it fsyncs the terminal outcome. An unresolved intent fences later mutations and
is completed before startup garbage collection. Recovery rehashes the pending
blob and fails closed if its bytes or journal are corrupt.

The gateway's recovery worker reads a signed private receipt HEAD. It can
confirm an original write after overwrite, object deletion, bucket deletion or
restart without uploading again. Identical retries return the original outcome
without restoring old bytes. A different request using the same receipt is a
conflict. Conditional rejection remains terminal for that receipt. Missing proof
keeps the gateway admission pending; it never establishes rejection.

By default, receipt files retain metadata indefinitely, independently of current
object bytes. They are neither object versions nor backups. The opt-in
[settled proof collection](#settled-receipt-and-multipart-tombstone-collection)
procedure below adds bounded first-use identities, gateway acknowledgement and
bounded cleanup. Monitor free space; do not manually unlink proofs. The
sealed catalog, including the format marker, recovery barrier and receipts,
belongs in every consistent offline backup.

Opening an earlier format-1 or format-2 directory upgrades its marker to format 3.
Earlier node binaries refuse format 3. Keep a consistent pre-upgrade backup if
rollback is needed, and never change the marker back manually.

[ADR-1565](adr/1565-owned-object-storage-indexed-listing-expiry.md) adds a local
ordered index for S3 object listings, active multipart counts and expiry.
Prefix and delimiter pages seek directly to matching keys and read at most
`max-keys + 2` source records, including the lookahead and boundary. Continuation
tokens resume by key; new tokens reject a different bucket generation. Concurrent
writes can affect later pages, so a listing is not a multi-page snapshot.
Coherent source inventory remains available; optional logical usage counters
are described below. Cumulative billing metering remains separate work.

The private `.catalog-index.db` is rebuilt from authoritative records at every
primary startup, in transactions of at most 128 records. Startup still scans the
catalog. Backups and replication omit this derived file; promoted primaries
rebuild it when opened. A projection failure preserves the durable source write
and returns `503 ServiceUnavailable` for indexed operations until restart.
Check `faas_object_node_catalog_index_ready` and its failures counter. Stop and
restart the daemon to rebuild; never edit source catalogs to repair the index.
If its type or permissions were changed, stop the daemon and remove only the
derived `.catalog-index.db` before restarting.
Before rollback to older backup/inventory tooling, stop the daemon and remove
only `.catalog-index.db`. Symlinked or aliased index files are refused.

The existing 30-second multipart worker now seeks due sessions and releases the
catalog lock between turns. Defaults are `--multipart-expiry-sessions=32`,
`--multipart-expiry-parts=8` and `--multipart-expiry-time=25ms`; maxima are 128,
128 and 100 ms. One turn removes at most one part directory after persisting the
terminal session status. Started durable decisions can exceed the soft time
budget. Assembly and pending source work defer expiry; tombstones and recovery
proofs remain intact. More than 16 files in a terminal part directory pauses its
online removal until offline recovery. Explicit abort/completion and offline
recovery retain bulk cleanup. Monitor the cached
`faas_object_node_catalog_index_*` counters for examined hints, expirations and
part removals; an idle queue performs no catalog scan. Development tests cover
a two-entry delimiter page reading three source records among 4,099 keys,
concurrent mutations, and idle expiry examining no sessions among 2,049 terminal
records. These are correctness and work-bound checks on the development host.

The opt-in recovery path below supports indexed automatic receipt cleanup.
Scaling still requires logical shards, replication, placement fencing and repair
before additional nodes can provide durable failover.

## Logical usage counters

[ADR-1566](adr/1566-owned-object-storage-usage-counters.md) adds a private signed
GET on `/<physical-bucket>?gregale-inventory=bucket-counters`. The complete JSON
response carries current object count and committed bytes, active/frozen multipart
upload count, selected part count and bytes, bucket generation, catalog snapshot,
observation time and last healthy audit time. Staged, unreachable and retained
proof bytes belong to physical diagnostics. Outstanding reservations remain in
the central ledger. These counters are not cumulative invoice meters.

For ordinary accounting, explicitly add `"owned_node_usage_counters": true` to
an existing backend with `"owned_node_receipts": true` after upgrading its node.
In that gateway/apid process, also set `FAAS_OBJECT_NODE_ENABLED=1`,
`FAAS_OBJECT_NODE_BACKENDS_ENABLED=1` and
`FAAS_OBJECT_NODE_USAGE_COUNTERS_ENABLED=1`. Both the backend option and the
operator gate default to off. The option preserves the placement fingerprint.
Enabled counter reads use one metered request. An unsupported node, unavailable
index or stale/unhealthy audit fails the observation and preserves previous
accounting and outstanding holds. Managed GCS and S3 providers keep their current
protocol. Fenced capacity reconciliation continues to validate source inventory
and physical references before releasing capacity.

Counters update with durable catalog publication, including overwrite, deletion,
part replacement and all terminal multipart transitions. Terminal part cleanup
does not subtract usage twice. The local index is rebuilt from validated source
records on primary startup, restore and promotion; backup artifacts omit it.
The checkpointed inventory worker checks both source integrity and counter
drift. Reads require healthy evidence within the existing 900-second inventory
freshness limit. A counter read does not perform a full physical integrity check.
Canceled audits never refresh readiness. Drift disables the index until rebuild;
source corruption requires source repair before counters become healthy again.

Monitor cached `faas_object_node_usage_counters_ready`, logical byte/count gauges,
`last_audit_timestamp_seconds`, `queries_total` and `drift_failures_total`.
Scrapes perform no catalog or database I/O. Startup, periodic physical audits and
fenced capacity recovery still scan source data. Obsolete part-index rows can
remain until the next startup rebuild, preserving bounded terminal transitions.

## Settled receipt and multipart tombstone collection

[ADR-1561](adr/1561-owned-object-storage-recovery-retention.md) adds an explicit
maintenance path for historical recovery metadata. It is disabled by default.
New gateway write and multipart IDs are canonical UUIDv7. First use must be
within 30 days of their encoded issue time, with at most five minutes of future
clock skew. Existing exact proofs remain recoverable after that window. Legacy
UUIDv4 identities retain indefinite proof and replay behavior.

Collection requires all of the following:

- The gateway owner has committed terminal settlement, then delivered an exact
  acknowledgement to the node.
- At least 30 days have elapsed since the node persisted that acknowledgement,
  and the request identity is older than the issue-time cutoff.
- The proof does not describe a current object. Unfinished decisions, active
  sessions, unacknowledged outcomes and legacy identities stay pinned.
- Multipart completion/rejection has a matching terminal receipt and session;
  aborted/expired sessions have no completion receipt and are beyond expiry.

The node stores a monotone retry barrier before deleting records. A retired
identity cannot recreate a PUT or native multipart session, even if its proof
has gone or the clock moves backward. Exact replay against a surviving proof
still returns the historical outcome without changing current bytes. A missing
proof remains unknown. `WriteReceiptExpired` is an HTTP 409 conflict and never
proves provider rejection or releases a gateway capacity reservation.

Upgrade the primary, followers and recovery CLI to executables supporting format
5 before enabling the feature. Stop the primary and take a verified backup of
its complete directory and a compatible gateway/PostgreSQL recovery point.
Enable the existing replication journal if it is not already enabled; local
durability mode does not require a follower. Then opt in:

```sh
object-storagectl replication-enable --data-dir /srv/gregale/object-node
object-storagectl recovery-enable --data-dir /srv/gregale/object-node
object-storagectl recovery-status --data-dir /srv/gregale/object-node
```

The upgrade is irreversible in place. Older executables refuse format 5 or the
new recovery journal record; do not lower the marker manually. Followers running
new executables apply the upgrade and barrier in journal order. Existing
registered retention pins and replica confirmation continue to protect history;
collection never retires a follower or changes promotion authority.

Enable acknowledgement delivery on the corresponding S3 backend in the gateway
and apid registry, preserving its endpoint and namespace:

```json
{
  "owned_node_receipts": true,
  "owned_node_settlements": true
}
```

These are fields within the existing backend configuration. In each process
loading it, explicitly set `FAAS_OBJECT_NODE_ENABLED=1`,
`FAAS_OBJECT_NODE_BACKENDS_ENABLED=1` and
`FAAS_OBJECT_NODE_SETTLEMENTS_ENABLED=1` as well.
[ADR-1562](adr/1562-owned-object-storage-settlement-outbox.md) adds durable
acknowledgement delivery. Apply its additive PostgreSQL migration before
deploying the new writers and apid worker, then enable the switch after all
gateways, apid writers and recovery workers are upgraded. New opted-in admissions save their
original placement in `object_node_settlement_outbox`. The row becomes ready
in the same transaction as terminal settlement; unknown writes remain waiting.
Prepared failures discard their waiting row. Managed providers and configurations
without `owned_node_settlements` do not enroll new jobs.

Apid's recovery loop delivers saved terminal evidence asynchronously, at most
16 jobs per sweep with two-minute fenced leases. A sweep has a 40-second
deadline; each signed RPC has a two-second timeout and one attempt. Temporary
failures back off from five seconds to one hour; configuration failures retry
hourly. The recovery loop polls every 15 seconds. A failed or lost
acknowledgement leaves the customer result committed and keeps the node's proof
protected. Delivery never retries object bytes or releases capacity. Missing or
invalid node replies keep the job pending without an age cutoff.

Workers use the saved backend ID, fingerprint and physical bucket, including
after bucket deletion. Keep that original backend configured until its backlog
drains. Changed endpoint/namespace fingerprints defer delivery; workers never
retarget jobs to a replacement bucket. Disabling `owned_node_settlements` stops
new enrollment while saved jobs can drain with `owned_node_receipts` enabled.
To roll back settlement enrollment, first set that backend option to false; the
settlement environment flag can then be removed. Keep master/backend flags and
receipts enabled until the saved backlog drains. Leaving an option true while
removing its required flag deliberately refuses startup. Changing or removing
the receipts capability prevents delivery.

Monitor `faas_object_storage_settlement_backlog{state="waiting"|"ready"}` and
`faas_object_storage_settlement_oldest_ready_seconds`. Delivery outcomes appear
in `faas_object_storage_recovery_attempts_total{operation="settlement"}` with
`delivered`, `unavailable`, `configuration` or `stale` outcomes. A growing ready
backlog or increasing oldest age requires checking worker health, original
placement, credentials and retained node evidence. Waiting rows follow pending
write recovery; they must never be marked delivered because they are old.

Include the outbox in central database backups. The existing 30-day compatible
gateway/node restore contract still applies. Jobs have no automatic expiry or
dead-letter deletion. Historical settlements created before enrollment was
enabled, including old writers during a rolling upgrade, are not backfilled;
their retained proofs can use the explicit retry path below.

For a retry, the signed S3 adapter exposes `AcknowledgeOwnedSettlement`. With the
primary stopped, an operator can also supply the exact already committed gateway
settlement as a JSON file to `object-storagectl recovery-settle --data-dir
/srv/gregale/object-node --settlement-file /secure/settlement.json`. Its fields
are `id`, `bucket` (physical name), `key`, `kind` (`put` or `multipart`), `status`
(`committed`, `rejected` or multipart `aborted`), `size`, `etag` and
`manifest_hash`. Committed outcomes require the saved ETag; rejected outcomes
use an empty ETag. Multipart completion includes the frozen manifest hash.
Plain multipart abort uses size zero and empty ETag/manifest. Every field must
match retained node evidence. The CLI does not verify a PostgreSQL backup or
query central state: the operator must source this file from durable terminal
settlement, never merely from a node receipt or an elapsed timeout. Repeating
an exact acknowledgement preserves its original time.

On an already opted-in format-5 primary, the daemon runs indexed cleanup by
default. [ADR-1563](adr/1563-owned-object-storage-indexed-recovery-cleanup.md)
adds a local `.recovery-index.db` B+tree, not a new node format. It indexes exact
acknowledged UUIDv7 terminal proofs. Every retirement still rechecks the sealed
proofs, retention window and current object under the catalog lock. Legacy IDs,
unknown outcomes, active sessions and current object proofs remain protected.

The worker wakes once per second and examines at most eight due identities.
It releases the catalog lock between identities and checks cancellation and a
soft 25 ms budget before starting further work. A started durable decision
finishes its fsyncs, so the budget does not guarantee a maximum request delay.
Cleanup defers while an earlier source write or journal application awaits
ordinary recovery; it does not replay object payloads within its own budget.
Use `--recovery-cleanup-records` (1–128) and `--recovery-cleanup-time` (positive,
at most 100 ms) to tune these bounds. `--recovery-cleanup=false` disables the
worker. These flags do not enable format 5 or bypass acknowledgement requirements.
Protected entries are checked again after one hour. Terminal multipart cleanup
requires empty parts; retained part entries defer paired removal. An idle or
protected tick does not advance the journaled retirement barrier.

The index persists its source stream/sequence/hash and deferred checks. Startup
replays at most 4096 retained entries from a validated checkpoint. A missing,
unfinished, foreign, ahead or pruned checkpoint rebuilds from the sealed catalog
in transactions of at most 128 candidates. Startup recovery/rebuilding can scan
the catalog. Online cleanup uses B+tree seeks and exact source reads, with no
periodic catalog scans or object hashing.

Monitor `faas_object_node_recovery_cleanup_enabled`, `_ready`, `_deferred`,
`_candidates`, `_due_age_seconds`, `_last_success_timestamp_seconds`,
`_steps_total`, `_failures_total`, `_examined_total`, `_protected_total`,
`_stale_total`, `_receipts_removed_total`, `_uploads_removed_total`,
`_index_rebuilds_total` and `_index_replayed_total`. All use that full prefix.
Counters reset with the process. Candidates include live protected proofs;
due age measures the oldest scheduled check, not confirmed reclaimable data.
Metrics use cached state without disk I/O or object identity labels.

Index failures pause collection and retry at most once every 30 seconds; an
already durable source write retains its successful outcome. If the index is
damaged, stop the daemon and remove only
`/srv/gregale/object-node/.recovery-index.db`, then restart to rebuild. The local
index is disposable; receipts, sessions, `recovery.json` and the journal remain
authoritative and must be retained. bbolt reuses pages without automatically
shrinking its high-water file. Backups omit the local index, retain source
acknowledgements and barriers, and restore as followers. Promotion rebuilds the
index from replicated records. Backup verification does not create an index.

For manual maintenance with the primary stopped, collect one offline page:

```sh
object-storagectl recovery-collect \
  --data-dir /srv/gregale/object-node \
  --limit 128
```

Read the JSON `done` field. If false, run the command again with its exact
`next_cursor` as `--cursor`, until `done` is true. A page examines at most the
requested number of identities (maximum 1024) with bounded working memory.
Directory scanning can read the full receipt/session directories, so this is an
offline procedure rather than a request-latency guarantee. The report counts
removed receipts, removed uploads and protected identities; it is not customer
usage or billing evidence. Collection does not hash object payloads. It journals
each paired deletion and fsyncs the barrier before unlinking. After interruption,
startup finishes that decision before normal receipt or multipart recovery.
A missing or corrupt barrier stops startup. An interrupted initial upgrade
resumes only from its already durable journal decision.

Run ordinary journal compaction separately to reclaim safely retained physical
history. Collection itself does not bypass journal limits or follower pins.
If the journal budget is already exhausted, drain the pinned followers and
compact safe history before starting collection.
Back up and verify the resulting format-5 primary with the existing backup
commands. The backup contains the barrier, acknowledgements and still-required
proofs, and restores as a non-serving follower. Catch-up and promotion retain the
barrier, and do not resurrect collected records.

Keep compatible gateway/PostgreSQL recovery points within the 30-day window
following acknowledgement, or restore a matching older node backup that still
contains the required proofs. Restoring older pending gateway state against a
newer collected node can leave admissions unresolved; missing evidence cannot
release them. Independent rollback to arbitrary old gateway backups is outside
this recovery contract. Use current executables for restore and keep clocks
accurate. An older node backup does not extend first use for an absent UUIDv7 ID.

Current object proofs, legacy history and undelivered acknowledgements still
require disk space. Existing central gateway journals are unchanged. Object
listings and active multipart expiry use the separate local catalog index
described above. Coherent inventory and usage audits still scan their source
catalogs. Independent-host and dedicated-disk qualification remains pending
access to the two test hosts.

## Verified offline backup and restore

Build the operator CLI with the repository's pinned Go toolchain:

```sh
go build -o /tmp/object-storagectl ./cmd/object-storagectl
```

Stop the daemon and fence gateway traffic to this node before backup. The tool
takes the same exclusive lock and refuses a running node. It performs normal
startup recovery first, including pending receipt settlement, garbage collection
and multipart expiry, then captures that recovered state. Use an initialized
format-3 directory or format-4/format-5 primary; upgrade older directories through
normal node startup first.
The source remains locked until backup publication completes.

Choose an existing private parent for the new backup, on storage with atomic
rename and file/directory fsync. Keep the backup in a separate failure domain:
another machine or independent backup storage. A copy on the node's own disk is
useful for a local drill but cannot survive loss of that disk. The tool accepts
filesystem paths, so arrange the independent mount or transfer separately.

```sh
/tmp/object-storagectl backup \
  --data-dir /srv/gregale/object-data \
  --backup-dir /mnt/object-backups/node-2026-10-16

/tmp/object-storagectl verify \
  --backup-dir /mnt/object-backups/node-2026-10-16
```

The backup destination must not exist, including as an empty directory. The tool
copies current objects and catalogs, empty buckets, historical write receipts,
multipart sessions/parts/tombstones, and the complete live scrub state. It does
not copy the advisory `.lock`, abandoned staging/trash or obsolete object bytes
removed by startup recovery. It checks object and part contents against their
stored SHA256, fsyncs the copied files, and reads the complete artifact back
before publishing it atomically. Corrupt source bytes never become a newly
verified backup just because a fresh manifest could hash them.

A completed backup contains `backup.json`, `manifest.jsonl` and `data/`.
Verification checks each file, catalog references, receipts and scrub records.
Live tracked objects must retain matching write proofs, and multipart receipts
must retain their terminal sessions;
missing/extra files, traversal, duplicate entries, ambiguous manifest JSON,
symlinks, special files and hard-linked files are rejected. Run `verify` again
after transfer and periodically on retained copies. It does not modify the
backup or expire its sessions. Keep the entire artifact together; never start a
daemon against the backup's `data/` directory. Backups hold customer data and
private metadata. Restrict access and protect transport; checksum verification
does not authenticate an artifact an attacker can replace in full. Encryption
and transfer orchestration are not part of this CLI.

Version 1 permits at most 1,000,000 entries including directories, a 256 MiB
manifest and 4 KiB records. It streams one file at a time with a 256 KiB buffer
and retains a bounded path set for verification. Destination file writes retain
the existing 1 GiB free-space floor. Allow space for a full copy; these checks do
not reserve storage against other programs. Source and destination must not
contain each other, including through a parent-directory symlink.

Restore to a new private node directory on a local Linux filesystem. Its parent
must exist, and the final directory must not exist. Use a separate isolated node
or test directory for the drill:

```sh
/tmp/object-storagectl restore \
  --backup-dir /mnt/object-backups/node-2026-10-16 \
  --data-dir /srv/gregale/object-restored
```

Restore verifies the backup while copying, rereads the actual restored bytes,
then runs normal node recovery for format 3 and fsyncs before atomically
publishing the new directory. Format-4 restores instead become fenced followers
as described below; they cannot start an S3 listener. Restore never overwrites or
merges existing data. Modes are 0700 for
directories and 0600 for files. Multipart deadlines remain unchanged: sessions
that expired while in backup are expired at restoration. Active and frozen
completions can resume within their original TTL. Scrub findings and progress
survive; changed restored file fingerprints restart a partial blob hash at zero.
Use the pinned Go version because scrub checkpoints contain serialized Go hash
state. Node credentials are supplied separately through the environment.

On ordinary pre-publication errors the staging copy is removed and the final
path stays absent. A killed process may leave `.object-backup-*` or
`.object-restore-*` siblings. They are unpublished copies; do not serve or adopt
them. Retry using a fresh final destination, and remove known abandoned copies
only after their process has stopped. If the final path already exists after
interruption, publication may have finished before success output. Verify that
backup, or inspect the restored node in isolation; never overwrite it on retry.
A parent-fsync error explicitly reports uncertain durability after publication.

For a restore drill, start `object-storaged` with the restored directory on an
isolated loopback endpoint and fresh operator credentials. Download representative
objects and check their saved checksums and metadata, list/resume a known multipart
upload within its TTL, and inspect known write receipts and scrub findings.
Record the backup UUID, capture time and verified bytes from the CLI's JSON
success output, together with the drill result. Exercise loss of the entire
original data directory
in development, as the automated tests do. Do not use production data for a
destructive drill.

An object-node backup excludes the gateway's PostgreSQL state. Returning an old
node snapshot to current traffic requires placement fencing and control-plane
reconciliation: writes accepted after capture may be absent, and missing receipt
proof must remain unknown. Coordinate that recovery before reopening traffic.
Automatic cutover, scheduled backups/retention and failover remain future work.

## Asynchronous replication journal and follower catch-up

Replication is explicitly enabled on a stopped primary. It upgrades that node
to format 4 so earlier binaries cannot write while ignoring the journal. The
default format-3 node remains available without replication. Back up before the
upgrade if rollback is needed; never edit the format marker or role by hand.

```sh
/tmp/object-storagectl replication-enable --data-dir /srv/gregale/object-data

/tmp/object-storagectl backup \
  --data-dir /srv/gregale/object-data \
  --backup-dir /mnt/object-backups/replication-seed

/tmp/object-storagectl verify --backup-dir /mnt/object-backups/replication-seed
```

The primary journals catalog mutations before applying them, retaining verified
bytes even after overwrite/delete or multipart cleanup. Records include ordered
deletions, new bucket generations, multipart sessions/parts and historical write
receipts. A durable record is recovered before ordinary receipt settlement or
garbage collection. A failed local journal/application checkpoint fences later
catalog access until recovery succeeds; it never silently disables replication.

Copy the complete verified backup to independent storage on the follower's host,
verify it there, and bootstrap a new local data directory:

```sh
/tmp/object-storagectl replica-bootstrap \
  --backup-dir /mnt/object-backups/replication-seed \
  --data-dir /srv/gregale/object-follower
```

Bootstrap records the snapshot's exact stream and applied sequence as its
baseline. It retains the snapshot's current data and metadata while clearing the
copied old journal/payload cache. Later changes arrive through the ordered stream.
Format-4 backups restored with the ordinary `restore` command also produce
followers. Format mismatch between a backup header and its actual node marker
fails verification rather than bypassing this role fence.

Restart the original primary with the usual `object-storaged` command. Keep its
operator credentials private. The primary still binds loopback; for a second
machine, use an authenticated encrypted tunnel to that listener or an existing
private TLS boundary. The following endpoint assumes an operator tunnel on the
follower host forwarding local port 19000 to the primary:

```sh
/tmp/object-storagectl replica-catch-up \
  --data-dir /srv/gregale/object-follower \
  --source http://127.0.0.1:19000 --region us-east-1

/tmp/object-storagectl replica-catch-up \
  --data-dir /srv/gregale/object-follower \
  --source http://127.0.0.1:19000 --region us-east-1 --follow
```

Supply the primary's `FAAS_OBJECT_NODE_ACCESS_KEY_ID` and
`FAAS_OBJECT_NODE_SECRET_ACCESS_KEY` through the environment. Requests use the
private operator SigV4 protocol, validate sealed record identities and blob
ranges, reject redirects, and have no fallback to ordinary S3 listing or GET.
Do not expose this endpoint or these credentials to tenants.

One step applies at most 32 records and transfers at most 64 MiB of blob bytes;
`--max-entries` and `--max-bytes` can lower those bounds. HTTP ranges are at most
4 MiB with a 30-second request timeout. Partial bytes are fsynced and resume at
their saved offset; complete payload hashes must match before application.
Local full-file verification and metadata I/O are additional work. `--follow`
drains consecutive bounded steps immediately while behind, then waits for source
changes through an authenticated request capped at 20 seconds. An unchanged
timeout resamples normally; transient connection/I/O failures back off one second. Fork,
authentication/configuration, schema and checksum errors stop the command.
Run one catch-up worker per directory; its process lock excludes competing work.
Continuous follow keeps that lock and the validated journal open across waits,
so startup history validation is performed once per worker lifetime.

Each successful step prints JSON with the sampled source head, local received
and applied positions, transferred bytes and `caught_up`. Received can be ahead
of applied while a payload downloads. `caught_up` refers to the sampled head;
the primary can accept more writes afterward. With the worker stopped:

```sh
/tmp/object-storagectl replica-status --data-dir /srv/gregale/object-follower
```

Caught-up progress includes `wait_cursor`, binding the stream, primary, epoch,
applied sequence/hash and a revision of relevant source state. Continuous JSON
also includes `follow` counters: `change_waits`, `change_wait_ms`, `retries`,
`reconnects`, and `source_lag_records`. Counts describe prior work as of each
progress record; reconnects count successful catch-up after transient failure.
Lag is relative to the sampled head. Wait duration includes idle time and must
not be interpreted as transfer latency.

The signed GET `gregale-replication=wait` accepts exactly `stream`, `primary`,
`epoch`, `sequence`, `hash` and `revision`. Each handler admits at most 64 wait
requests independently of upload slots; excess requests receive 503. The server
returns current status on a relevant change or the fixed 20-second timeout, below
the 30-second client timeout. Cancellation and graceful daemon shutdown release
idle requests. Upgrade daemon and CLI together; the HTTP worker does not silently
fall back to polling an older daemon.

New applied data, fencing, authority/policy and registration changes affect the
wait revision. Received positions and acknowledgement updates do not, avoiding
self-wake loops. Waits hold no store locks while idle. The worker always reruns
normal authenticated head/hash-chain verification and applied-only fsynced
reporting after a wake or timeout. Notifications grant neither durability nor
promotion authority. History below a compacted floor still requires reseeding.

The metrics listener exposes `faas_object_node_replication_waiting` and
`faas_object_node_replication_change_wait_seconds{outcome}`, with bounded outcomes
`changed`, `timeout`, `canceled`, `error`, `rejected`. Use these with existing
replica confirmation lag and retained-prefix gauges; no identity labels are added.

The corresponding signed primary GETs use `gregale-replication=status`,
`gregale-replication=entry` with `stream`/`sequence`, and
`gregale-replication=blob` with `stream`/`sha256`/`offset`/`length`. They are
private observations/transfers, never customer object selectors or receipt
proofs. Monitor position lag, repeated worker errors and physical disk use.

The follower rejects normal `object-storaged` startup. It runs no local expiry,
receipt settlement, scrub or primary object garbage-collection workers; catalog decisions come
from the primary. Promotion requires the planned handoff below. A sampled
`caught_up` result alone grants no write authority.

The default local mode acknowledges local durability. Asynchronous lag can lose
acknowledged writes if the primary disk fails before catch-up. Do not promise
zero-data-loss failover or edit a role file. Optional replica durability is
described below; automatic failover remains future work.

Both nodes retain journal history until safe online or offline compaction,
described below. Explicitly registered followers and the designated durability
replica hold back the eligible prefix. Retention is capped at 1,000,000 records,
1 GiB of journal metadata, and 8 TiB of replay payloads (at most 1,000,000 files).
Each record is limited to 2 MiB; the existing free-space floor also applies.
Sequences continue monotonically up to 9,007,199,254,740,991, independently of
retention. Reaching a budget refuses further publication until space is reclaimed.
Backups include the primary's retained journal, subject to the backup entry and
manifest ceilings. Existing disk metrics include retained bytes in physical
usage; logical customer capacity remains based on the current catalog.
Provision room for retained and live copies, plus full backups. Replay preparation
now runs outside the catalog lock; metadata publication and fsyncs remain
serialized. Measure foreground traffic and catch-up before expanding usage.
Bounded automatic compaction now runs in the daemon and continuous follower;
broader publication pipelining remains future work. The offline command can also
reclaim history; do not manually delete retained files.

## Publication preparation and replication work limits

PUT, part upload and multipart completion verify immutable replay bytes before
taking the catalog write lock. One preparation runs per store, with cancelable
queueing, while reads and metadata operations can progress. Publication rechecks
conditions, generation, multipart state, quotas and file identity, then installs
and fsyncs replay data before the journal decision. Preparations grant no write
outcome. Restart replay and uncertain decisions retain full payload verification.
This uses the existing disk format and private filesystem ownership contract.

CLI catch-up now paces work across batches and reconnects. Defaults are 64 MiB/s
accounted byte work and 256 entries/s, with bursts of one 4 MiB range and one
32-entry batch. Explicit controls are:

```sh
/tmp/object-storagectl replica-catch-up \
  --data-dir /srv/gregale/object-follower \
  --source http://127.0.0.1:19000 --follow \
  --work-bytes-per-second 67108864 --work-entries-per-second 256
```

Rates must be positive, at most 1 GiB/s and 4,096 entries/s. Byte work includes
network attempts, downloaded-file hashing, retained cache reads and replay
copy/verification reads. Cached payloads still consume tokens when reread.
Actual payload throughput can be lower because one payload may require multiple
reads/copies. Progress adds `work_bytes` and `work_wait_ms`; transferred-byte and
applied/received fields retain their meanings.

Pacing cancels and resumes partial work safely. Low rates or sustained backlog
can exceed the replica response-wait timeout: retain receipts and capacity holds
until the normal applied confirmation resolves them. These per-worker budgets
exclude startup checks, offline work, foreground uploads and metadata/fsync cost;
they are not OS-level CPU/disk isolation. Library callers can leave both rates
zero for existing explicitly unpaced behavior.

The daemon separately admits two active private payload responses, independently
of upload and change-wait slots. Saturation returns 503; status and confirmations
can still progress. Observe `faas_object_node_replication_transfers` and
`faas_object_node_replication_transfer_rejections_total` alongside lag and disk
metrics. Each response retains the existing 4 MiB range limit.

To reproduce the storage-engine profile with 24 distinct 5 MiB tracked writes,
four workers and a continuous seed-object metadata/open/close reader:

```sh
profile_dir="$(mktemp -d)"
GOMAXPROCS=4 go test ./pkg/objectnode -run '^$' \
  -bench '^BenchmarkReplicationPublication$' -benchtime=24x -count=1 \
  -o "$profile_dir/objectnode.test" -cpuprofile "$profile_dir/cpu.pprof" \
  -mutexprofile "$profile_dir/mutex.pprof" -blockprofile "$profile_dir/block.pprof"
go tool pprof -top -cum "$profile_dir/objectnode.test" "$profile_dir/mutex.pprof"
```

The 2026-10-16 before/after profile measured 119–128 to 259–324 MiB/s timed write
throughput, with mean reader delay falling from 3.7–5.1 ms to about 0.05 ms.
Aggregate sampled mutex delay fell from 7.37 s to 0.194 s. Preparation queueing
still exists outside the catalog mutex. The follower case uses unpaced in-process
replay in both profiles. CPU profiles include setup/warmup, background reads and
replay; benchmark allocations are not isolated per-PUT costs. These short engine
profiles do not represent gateway performance or production latency bounds.

Two matched gateway pairs used four workers, two cycles, four workflows per cycle,
a 30-second admission ceiling, 64 KiB objects, seed 1 and `GOMAXPROCS=4`.
Each run used the same qualification runner and completed all workflows, passing
14 checks per durability mode. The combined change includes both preparation and
the default work budgets; it does not isolate their effects individually.

| Observation across matched cycles | Before preparation/work budgets | After |
| --- | --- | --- |
| Local GET p95, ms | 72–108 | 43–49 |
| Local PUT p95, ms | 166–226 | 172–202 |
| Local mixed requests/s | 25.8–43.7 | 33.2–39.0 |
| Replica PUT p95, ms | 364–530 | 392–689 |
| Replica mixed requests/s | 13.1–19.4 | 12.6–16.0 |
| Whole-run user + system CPU, s | 12.22–14.85 | 12.18–13.35 |

Whole-run CPU includes both modes, recovery and reaped child processes. PUT has
12 attempts per matched cycle, including conditional rejections. Foreground reads
improved in these samples; throughput/PUT latency were mixed and replica throughput
was lower with pacing. Treat the work budget as a configurable tradeoff, retain
the adverse results, and measure on actual separate disks/hosts. The final runner
also passed all 14 checks in both modes with the original two-second admission
profile and eight-workflow ceiling; inspect its actual admitted workflow counts.

## Planned primary switchover

Use this procedure while both nodes and the old primary's durable data directory
are reachable. It permanently fences the old writer and grants exactly one
follower authority at the final journal position. There is no force promotion,
unfence or retarget operation. Confirm the candidate and its disk before starting.

Upgrade both participants to this binary first. Replication state version 2 adds
node/primary UUIDs, an authority epoch and sealed handoff lineage. Legacy state
is upgraded under its process lock; older replication binaries reject the new
state. Journaled backups/restores use format 4, or format 5 after recovery-retention
opt-in, and always assign restored followers fresh identities. Never raw-copy or roll back a running primary's directory or
VM image: the local role fence cannot arbitrate cloned writers.

1. Pause all customer object mutations/admission at the gateway, including
   previously issued write URLs, and pause background object mutations. Drain
   existing requests. Preserve gateway/PostgreSQL state and outstanding receipt
   IDs. Stop the follower's continuous catch-up worker, then finish a catch-up
   step if it has an incomplete transfer or receipt transition.
2. On the follower host, keep an encrypted tunnel to the old primary's loopback
   listener. This example uses local port 29000 for that tunnel so port 19000 is
   available for the eventual successor. Supply the old primary's operator
   credentials through the existing environment variables.

   ```sh
   /tmp/object-storagectl primary-fence \
     --data-dir /srv/gregale/object-follower \
     --source http://127.0.0.1:29000 --region us-east-1
   ```

   This first verifies the candidate's current catalog and hashes every baseline,
   live and retained payload. It removes recognized unpublished temporary files
   left by interrupted publication under the process lock; unsafe types, aliases
   and unknown paths fail closed. A failed preflight does not fence the source.
   The source then drains in-flight uploads/assembly, settles durable pending
   decisions, and fsyncs its final sequence/hash and selected target identity.
   New mutations fail. A long drain can exceed the HTTP timeout: retry the same
   command and target to resolve the outcome. Never choose a second target after
   an uncertain response.
3. Leave the fenced primary's daemon/export running. Its ordinary object,
   receipt and inventory requests return `PrimaryFenced`; authenticated journal
   status and transfer still work after restart. Catch up to its sealed head:

   ```sh
   /tmp/object-storagectl replica-catch-up \
     --data-dir /srv/gregale/object-follower \
     --source http://127.0.0.1:29000 --region us-east-1
   ```

   Repeat bounded steps until `caught_up` is true. Inspect `replica-status` if
   needed: received and applied sequence/hash must both match the handoff.
   Then promote using live confirmation from that same fenced source:

   ```sh
   /tmp/object-storagectl replica-promote \
     --data-dir /srv/gregale/object-follower \
     --source http://127.0.0.1:29000 --region us-east-1
   ```

   Promotion rehashes the complete candidate again, checks terminal historical
   receipts and multipart/catalog references, and refuses lag, foreign authority,
   a different target, pending transfers or damage. It atomically publishes the
   primary role and epoch plus one with the original grant. The stream and
   sequence continue; existing retention/record ceilings do not reset. Epochs
   are capped at 1,000,000. A repeated successful promotion returns the existing
   authority, including after a lost response; it never increments twice.
4. Start `object-storaged` on the successor using its promoted data directory and
   normal private listener/credential setup. Keep customer writes paused. Verify
   object bytes, selected active multipart parts, known historical PUT/completion
   receipts and a healthy coherent inventory. Move the gateway's stable private
   tunnel/routing target to the new node and drain connections to the old target.
   Preserve backend ID, namespace, physical bucket names, configured endpoint,
   signing configuration and `owned_node_receipts`. Editing the configured URL
   changes its placement fingerprint and causes existing buckets/receipts to
   fail resolution; it is not a supported migration.
5. Enable the existing apid receipt recovery and inventory workers against the
   successor while keeping customer mutations paused. Confirm pending PUT and
   multipart outcomes, current committed bytes/keys, and retained admission
   holds. Missing receipt proof remains uncertain; never force-release capacity
   from a timeout, sequence comparison or missing current object. If a capacity
   rebase is needed after settled writes/deletions, use the existing
   `gregale bucket reconcile start/status` workflow described in
   [object storage](object-storage.md#reclaim-reserved-capacity). Finish or abort
   live multipart sessions before a capacity reconciliation. Reopen customer
   writes after these checks; retain unresolved holds for normal recovery. In
   replica mode, first bind and confirm a new standby as described below: a
   promoted primary retains replica mode and refuses new mutations while unbound.
6. Keep the old data directory fenced. Old followers reject the successor's new
   authority epoch. To rejoin them, stop the new primary for a verified backup,
   then use `replica-bootstrap` into a **new** directory on the old host and
   catch up from the new primary's private endpoint. Preserve the old fenced
   directory until the reseeded follower is verified. Never change its role or
   reuse its node identity. Successor backups preserve epoch/lineage but restore
   only as a new follower.

Before/after fence and promotion crashes are retryable. A failed first promotion
leaves the follower without write authority unless its atomic state publication
already completed; retry or inspect its durable status to resolve that outcome.
A source whose fence never published may reopen as a primary; a successfully
fenced source always reopens as a sealed export and cannot regain write authority.
If the source is unreachable before the first promotion, restore connectivity
and retry. Independent fencing or reliable lease/quorum authority is required
before supporting unreachable-primary failover. This procedure does not create
automatic routing or leader election. The separately configured durability mode
continues to govern service acknowledgements.

## Replica-confirmed acknowledgements

Local mode remains the default. Optional replica mode withholds S3 success until
both the primary and one designated follower have durably applied the captured
journal prefix. Use independent hosts/disks for failure protection. This is a
durability policy for the private S3 service, not leader election or automatic
failover. Upgrade both node and catch-up binaries before enabling it; replication
state version 3 makes older binaries fail closed. Restarting does not reset mode.

Stop the catch-up worker and inspect its durable identity:

```sh
/tmp/object-storagectl replica-status --data-dir /srv/gregale/object-follower
```

Copy its `node_id` into `OBJECT_REPLICA_NODE_ID`. Pause gateway mutations, drain
requests, stop the primary, and configure its existing data directory:

```sh
/tmp/object-storagectl replication-durability \
  --data-dir /srv/gregale/object-data \
  --mode replica \
  --replica-id "${OBJECT_REPLICA_NODE_ID:?set the follower node_id first}"
```

Restart the primary normally and start the follower's existing `replica-catch-up
--follow` command against its private endpoint. The selected follower now sends
authenticated confirmations after fsyncing its applied checkpoint. Reports bind
stream, primary, epoch, policy and follower identities plus sequence/hash. A
partial transfer can report only its earlier applied prefix. Other followers
can copy data but cannot confirm this policy. No additional listener is needed.

The signed `gregale-replication=status` response includes `durability`: mode,
policy/follower UUIDs, `confirmed_sequence`, `confirmed_hash` and `confirmed_at`.
Sequence -1 means no confirmation. Successful catch-up progress includes
`confirmed_sequence` when the selected worker's report was accepted. Confirming
a verified baseline requires no new journal entries or object downloads.

The S3 service snapshots the current final journal position after local
publication and waits for confirmation before sending success headers. This
includes bucket/object/multipart mutations and positive receipt/inventory
responses; conditional rejection also waits for its negative receipt. The wait
holds no catalog lock. Concurrent publications can extend the captured prefix,
so an unrelated pending write can delay an older receipt/read. Replication RPCs
continue while ordinary responses wait. In-process `Store` callers still have
local publication semantics and must explicitly call `WaitForReplica` when they
need this service-level boundary.

The response wait is at most 10 seconds and respects request cancellation.
Timeout returns 503 `ReplicaDurabilityPending` with no success ETag, size,
checksum or receipt headers. Local publication may already have committed:
retain the saved receipt and use normal recovery/retry with its original
identity. Do not create a new upload identity to settle an uncertain operation.
Private PUT/completion receipt probes and coherent inventory stay unavailable
until their captured prefix is confirmed, preserving gateway admission holds.
Missing proof never releases capacity. Large writes or lag can exceed the wait
even with a healthy follower; catch-up retains its existing 32-record/64-MiB
per-step transfer bounds.

The confirmation endpoint is private SigV4 POST
`/?gregale-replication=confirm`, with a canonical SHA256-signed JSON body of at
most 4 KiB. The primary verifies its retained hash chain and persists the
confirmation before responding or releasing waiters. Repeated reports recover
lost acknowledgements without replaying mutations or downloading completed
payloads. Operator credentials and cross-host tunnel/TLS requirements are the
same as catch-up; tenants never receive these credentials.

Monitor these memory-only operational metrics:

- `faas_object_node_replica_required`, `faas_object_node_replica_bound` and
  `faas_object_node_replica_confirmation_known`.
- `faas_object_node_replica_lag_records` and
  `faas_object_node_replica_caught_up`, which describe the sampled journal head.
- `faas_object_node_replica_waiting_responses` and
  `faas_object_node_replica_wait_seconds` by confirmed/pending/error outcome.

There are no identity labels. A caught-up sample is not authority to promote.

To replace a failed follower or correct a binding, take a verified current
primary backup, bootstrap a new follower and obtain its new UUID. Stop the
primary and repeat `replication-durability --mode replica --replica-id ...` with
that UUID. Replica mode stays enforced with a new policy identity and reset
confirmation. Old reports cannot release responses; the replacement must confirm
the pending prefix. This preserves unresolved writes and reservations rather
than falling back to local durability. Restart and run the new worker, verifying
its confirmation before reopening customer traffic.

To return deliberately to local mode, first catch up and confirm all current
records, then stop the primary and run:

```sh
/tmp/object-storagectl replication-durability \
  --data-dir /srv/gregale/object-data --mode local
```

The command refuses an unconfirmed tail. Startup recovery/expiry can add records,
so restart/catch up again if necessary; there is no force downgrade. Local mode
restores the original weaker acknowledgement semantics for future operations.
Already issued replica promises are not silently weakened.

Planned switchover in replica mode grants only the designated follower. The
verified final handoff certifies that old prefix on the promoted node, which
retains replica mode with no standby bound. New S3 mutations are refused until
another follower is configured. Preserve the old fenced directory, bootstrap a
new follower from a verified successor snapshot, bind its fresh UUID and confirm
any new recovery/expiry records before reopening writes. Restores always create
followers and never clone the primary's confirmation authority.

Replica mode acknowledges two completed filesystem durability boundaries. It
does not protect against loss of both copies or permit unreachable-primary
promotion without independent fencing or lease/quorum authority. The existing
production-enrollment restriction, explicit journal maintenance, physical disk
budget and qualification requirements still apply.

## Journal retention and offline compaction

The daemon and continuous follower now run bounded automatic retention by default;
see the automatic maintenance procedure below. The explicit offline operation
retains the full integrity preflight and remains available for maintenance and
recovery at a hard retention ceiling.

Compaction reclaims physical replay history while preserving current object
bytes, all historical write receipts, and active or terminal multipart state.
It changes no customer capacity reservations, SQL rows or billing records.
Receipt and multipart tombstone expiry require a separate settlement protocol.

Compaction and follower registration upgrade the sealed replication state to
version 4. Older binaries reject that state. Deploy this version to the primary
and catch-up workers before using these commands; retain a verified backup for
rollback rather than editing markers. State version 4 supports the existing
local and replica durability modes. Compaction never changes an active replica
policy or substitutes an asynchronous cursor for a durability confirmation.

### Register followers before pruning

With a follower's worker stopped, obtain `node_id`, `applied_sequence`, and
`applied_hash` from `replica-status`. Stop the primary, then enroll that exact disk
using those values. The following names stand for the actual JSON field values:

```sh
/tmp/object-storagectl replica-status --data-dir /srv/gregale/object-follower

/tmp/object-storagectl replica-register \
  --data-dir /srv/gregale/object-data \
  --replica-id "$FOLLOWER_NODE_ID" \
  --sequence "$FOLLOWER_APPLIED_SEQUENCE" --hash "$FOLLOWER_APPLIED_HASH"
```

The primary checks the position against its retained hash chain. At most 64
followers can be registered. The operator must identify the intended follower;
registration accepts a trusted operator's cursor, not a remote disk attestation.
An enrollment has a fresh registration UUID. Retrying the same current position
preserves that UUID. A cursor below the retained floor is refused with an
explicit reseed-required error.

Restart the primary and the existing `replica-catch-up --follow` worker. After
each successful step, including steps which apply nothing, registered followers
send an authenticated `gregale-replication=cursor` POST containing their stream,
primary, epoch, registration UUID, node UUID, applied sequence and hash. The
source fsyncs each verified cursor before acknowledging it. Received records
and partial payloads never advance it; a failed checkpoint sends no report.
Reports are bounded by the existing 4 KiB body limit and require a signed SHA256
body. Unknown or duplicate fields, stale registrations, regressions, foreign
identities, wrong hashes and future positions are refused.

Registrations do not expire when a follower is offline. An unregistered follower
can copy available history, but has no retention guarantee. Stop the primary to
release an abandoned follower's pin explicitly:

```sh
/tmp/object-storagectl replica-retire \
  --data-dir /srv/gregale/object-data --replica-id "$FOLLOWER_NODE_ID"
```

Retirement preserves a separate replica durability requirement for that node.
Re-enrollment creates a new registration UUID, so delayed reports cannot cross
that transition. A replacement for the designated replica keeps its separate
unconfirmed-tail pin under its new acknowledgement policy. Retirement grants no
write authority and never un-fences an old primary.

### Compact a stopped disk

Pause traffic and stop the primary. The command takes the same exclusive process
lock as the daemon. The default cut is the minimum of the applied head, every
registered follower's applied cursor, and any replica durability confirmation.
An unknown replica confirmation prevents advancing the current floor. An
explicit `--through` value is refused if it passes any pin or the applied head.

```sh
/tmp/object-storagectl compact --data-dir /srv/gregale/object-data

# Optional smaller cut for retaining additional replay history:
/tmp/object-storagectl compact \
  --data-dir /srv/gregale/object-data --through "$RETAINED_CUT_SEQUENCE"
```

JSON output includes the new status, before/after physical journal and replay
usage, records pruned and bytes pruned. Provision room for a small checkpoint
write even when a retention budget is reached. The command validates the retained
chain, rejects aliased or unsafe file types, and verifies referenced current and
replay bytes before advancing the floor. It seals and fsyncs the new floor/hash
with a pruning-in-progress marker before unlinking anything. It deletes old
records and payloads with no retained-journal or pending-receipt reference,
fsyncs both directories, then seals the completed checkpoint.

A killed process or interrupted command may have published the new floor.
Restart or repeat the command: startup validates all required retained history
and resumes a recorded cleanup before serving traffic. Missing history above the
floor still fails closed. Never infer failure from a lost command response and
restore old journal/state files over the directory.

Compaction deliberately does not run ordinary receipt settlement or multipart
expiry. This lets it free a full journal when publication stopped between a
receipt's intent and terminal record. Pending receipts retain their recovery
payload even below the new floor; their physical object bytes remain in place.
Normal primary restart completes those decisions under the existing durability
policy. A second compaction can reclaim the now-obsolete replay bytes. If a
follower pins a full journal, let it apply/report the available prefix or retire
its registration; a designated durability replica must confirm that prefix or be
replaced while preserving replica mode.

Stop a follower's worker to compact that disk with the same command. Its local
received and applied positions must match; finish any partial transfer first.
Follower compaction preserves its non-serving role and copied recovery state.
Restart catch-up to continue from the same monotonically increasing sequence.
A fenced source refuses new compaction. Startup can finish cleanup whose floor
was already sealed before fencing; its final handoff position remains intact.

Backups verify the compacted prefix and only include retained replay files.
Restore/bootstrap remains a new non-serving follower at the snapshot's applied
position, clears copied registrations and acknowledgement authority, and assigns
a fresh node UUID. Register that new UUID if it needs a retention guarantee.
Planned promotion keeps the stream and sequence numbers; a successor can compact
beyond its sealed promotion boundary while retaining the handoff certificate.
Obsolete local-mode confirmation telemetry is dropped when the floor passes it;
an active replica confirmation remains a mandatory pin.

### Reseed a follower below the floor

An older applied position receives `ReplicaReseedRequired` (HTTP 410 for the
private protocol). The CLI stops continuous follow with an explicit instruction
to reseed. Do not skip records, reset its cursor, or enable its S3 listener.
Create and verify a fresh offline primary backup, transfer and verify it on the
follower host, and bootstrap a **new** data directory. Preserve the retired disk
for inspection, retire its registration if still present, and enroll the new
node UUID before resuming compaction. A follower already at the floor can
continue normally; a report retry needs no replay bytes.

### Observe retention pressure

Memory-only metrics include `faas_object_node_retention_base_sequence`,
`_applied_sequence`, `_safe_sequence`, `_pinned_records`,
`_registered_followers`, `_records`, `_journal_bytes`, and `_blob_bytes` under the
same `faas_object_node_retention` prefix. They have no node, bucket, key or receipt
labels and perform no scrape-time disk walk. Counts include unpublished replay
payloads. Compare these samples with the limits in `pkg/api/limits.go`, existing
physical free-space metrics and the separate replica acknowledgement metrics.
Compaction bounds replay history; current objects, receipts, multipart tombstones,
backup copies and unrelated files still need their own disk capacity planning.

## Automatic journal maintenance

Upgrade the daemon, control tool and continuous workers together before enabling
online retention. Storage keeps the existing journal checkpoint; follower
position validation now handles a floor advancing between status and entry reads.
Unknown/unregistered followers can still require reseeding after a floor advances.
Registered follower cursors and replica confirmations remain strict pins.

Automatic maintenance runs by default in `object-storaged` and
`object-storagectl replica-catch-up --follow`. It starts at any of 750,000 retained
journal/replay files, 768 MiB of journal bytes, 6 TiB of replay bytes, less than
4 GiB available space, or less than 25 percent filesystem space available to the
process. These watermarks precede the existing hard retention budgets; unavailable
followers can still prevent reclamation and eventually stop writes. No worker
retires a follower, weakens durability or frees an uncertain admission.

```sh
/tmp/object-storaged \
  --data-dir /srv/gregale/object-data \
  --compact=true --compact-records 750000 \
  --compact-journal-bytes 805306368 --compact-replay-bytes 6597069766656 \
  --compact-min-free-bytes 4294967296 \
  --compact-step-records 128 --compact-step-blobs 128
```

The same controls apply to continuous catch-up. Set `--compact=false` on either
process to disable its worker. One-shot catch-up does not start background
maintenance. File/byte watermarks must be positive and within their hard budgets;
the free-byte watermark may be zero, retaining the percentage trigger. Per-step
file counts are 1–1,024. Configure the free-space watermark above any larger
custom write free-space floor.

One step runs every 100 ms. A record step reads at most 1 MiB of metadata, with
one indivisible record allowed up to its existing 2 MiB ceiling. A replay step
examines at most the configured number of directory entries, including referenced
files it keeps. It streams no payload bytes. Metadata work, unlinks and fsyncs
still hold the catalog lock for one batch, so slow disks can affect requests.
Tune these bounds using foreground latency and cleanup progress; there is no
hardware-independent throughput guarantee. Errors back off five seconds.
Idle ticks avoid replication metadata reads/fsyncs; cached usage and filesystem
space determine whether a cycle is needed. A pinned cycle backs off until its
retry time or an applied cursor advances.

A cycle captures an applied target and advances the floor in bounded, sealed
batches no farther than every registered cursor and replica confirmation permit.
The durable pruning marker precedes unlinking; directory fsync precedes quota
credit and completion. Live writes and reports can advance between batches.
Replay cleanup requires zero journal references and zero committing-receipt pins,
and defers while an upload owns the replay-preparation slot. Historical receipts,
multipart sessions/tombstones and current object/part bytes remain intact.

Primary and follower receipt pins are rebuilt from vetted metadata on startup,
including an intent below a compacted floor. Partial follower transfers are
deferred. A lost checkpoint acknowledgement blocks stale replication observations
until durable reconciliation; lost unlink acknowledgements keep conservative
usage charges until a successful sync. Restart validates the retained chain and
finishes sealed cleanup before serving, using the existing recovery path. Startup
recovery can take longer than a single online batch.

Online cleanup uses validated journal references and immutable publication. It
does not hash the whole current catalog or all payload bytes for each batch.
Checksum scrubbing, offline full verification and verified backups retain that
integrity role; the explicit offline compactor's preflight is stronger than an
online cleanup tick. Private filesystem ownership remains required.

Observe `faas_object_node_retention_compaction_steps_total`, `_failures_total`,
`_cycles_total`, `_records_pruned_total`, `_blobs_pruned_total` and
`_bytes_unlinked_total`, plus `_active`, `_pinned`, `_deferred` and
`_last_success_timestamp_seconds` under the same prefix. They are cached,
process-local aggregate observations. Bytes unlinked do not measure freed blocks
while a reader still has an inode open. Compare them with existing retained usage,
safe/base sequences, pinned records, free space and request latency. Continuous
follower JSON adds `compaction`; it does not change applied confirmation semantics.

For a short qualification run, use `--compaction-records 16` and
`--automatic-compaction=true` on `object-storage-qualify`. Compare with
`--automatic-compaction=false` using fresh directories/reports and the same load.
The runner forwards settings through SSH and planned role reversal; upgrade the
remote qualification agent to protocol 2 as well. Reports record the configuration
and sampled primary compaction counter peaks. Peaks can miss work or reset across
process restarts; they are not lifetime totals.

The 2026-10-17 development qualification used two matched enabled/disabled pairs
with identical binaries, four workers, two cycles of four workflows, 64 KiB
payloads and a 16-file trigger. Both durability modes passed all 14 checks in
every run, with zero unexpected request failures. Enabled primary process peaks
showed 43–44 records and 10–11 replay files pruned; disabled runs showed zero.
Latency varied: replica-mode GET p95 ranged from 90–224 ms enabled and 84–134 ms
disabled, while local-mode PUT p95 ranged from 145–244 ms enabled and 164–203 ms
disabled. Each per-cycle operation has four samples, making p95 its maximum.
These short shared-host measurements validate reclamation and recovery; size
cleanup against foreground latency on the intended hardware before enrollment.
An additional shared-host OpenSSH run passed the same checks with protocol 2,
automatic cleanup, planned promotion and restart/rejoin. It sampled 43–48 primary
records and 10–11 replay files pruned; independent-host durability remains untested.

## Sustained traffic and cleanup qualification

Use `object-storage-qualify --soak` for repeated online cleanup under customer
traffic. This uses the same disposable local or SSH arrangement and byte,
historical-receipt and capacity model as the short qualification profile.

```sh
/tmp/object-storage-qualify \
  --daemon /tmp/object-storaged --ctl /tmp/object-storagectl \
  --work-dir /tmp/object-soak-run --report /tmp/object-soak-report.json \
  --soak --durability both --workers 4 --cycles 1 \
  --duration 5m --max-workflows 4096 --object-bytes 65536 \
  --compaction-records 64 --soak-interval 5s \
  --soak-workflows-per-second 1 \
  --soak-fault-duration 1s --disk-budget-bytes 2147483648
```

The example selects an aggregate target of one workflow per second. Omit
`--soak-workflows-per-second` for the default unpaced profile, or choose an integer
target from 1 to 32. Pacing allows an initial burst of one workflow; actual
throughput may be lower under contention. The remaining settings are soak
defaults. Each mode runs sequentially for its configured
admission duration, then drains in-flight requests and runs the existing
correctness, backup/restore, crash and planned-promotion checks. A workflow retains
its 30-second deadline. Explicit duration, cycle, workflow and cleanup watermark
flags override profile defaults. Add the existing `--follower-ssh` options for a
remote follower; matching node binaries and agent protocol 2 are still required.

The live object set reuses worker keys for PUT/GET, overwrite, conditional
rejection, DELETE, multipart completion and abort. Historical receipts,
multipart metadata, backups and retained history still consume disk and memory.
At most 16,384 configured workflows are allowed across all cycles of a soak mode,
with at most 16 workers, 16 cycles, 8 MiB bodies and 30 minutes of aggregate
admission per mode. The ordinary profile retains its 256-workflow ceiling.

`--disk-budget-bytes` must be positive and at most 2 GiB. It covers sampled
disposable file sizes on both participants, including backups and timeline files.
It stops new workflows; already admitted work and later maintenance copies may
overshoot it. It is not an OS disk quota or allocated-block measurement. Soak
admission stops when its latest disk probe failed or is older than five seconds.
Existing node free-space rejection remains active. Keep enough free space for
in-flight work, backup copies and filesystem overhead.

Report version 3 adds `soak` to each load, linking to an exclusive 0600
`soak-<cycle>.jsonl` in the mode directory. Its interval is five seconds by default;
`--soak-interval` allows one second to one minute. Each row records operation
counts and exact p50/p95/p99/max latency, request rate, completed workflows and
observed primary cleanup rates. Latency belongs to the interval where the request
finishes. The same resource sampler supplies RSS, file/journal/replay bytes,
retained records, applied/safe/base sequences and primary cleanup counters.
Observation time, age and validity flags distinguish failed/stale probes from
zero usage. Rates are sampled counter differences over the window, not exact
instantaneous disk throughput. Each timeline is limited to 16 MiB and each row to
32 KiB; a final row is flushed and the file synced/closed on exit. The summary
retains the existing 2 MiB bound and includes cleanup totals relative to the load's
starting primary generation.

With `--soak-faults=true`, replication is partitioned at one third of admission,
then the follower is SIGKILLed and restarted at two thirds. The default one-second
hold can be set from 200 ms to two seconds with `--soak-fault-duration`. Customer
requests continue, retaining normal replica response gating and deadlines.
Record actual customer HTTP starts and active requests/bodies during each
interruption, plus primary journal positions before and during the hold. A
request may wait on an already-published unconfirmed prefix, so that position can
remain unchanged. Require live HTTP activity and a non-regressing position, then
wait for the registered follower and, in replica mode, its confirmation to reach that
finite captured position. Timings and positions are included in the summary;
periodic reporting continues during recovery. Normal post-load maintenance still
requires exact catch-up after traffic drains.

The soak coverage check requires the complete requested duration, fresh interval
metrics, at least two observed completed cleanup cycles when automatic cleanup is
enabled, and both requested interruptions with live HTTP activity and rejoin evidence.
Reaching `workflow_limit` early fails duration coverage. Increase the workflow
budget within its hard bound or shorten the duration; do not count an early stop
as a completed soak. Disk/probe, timeline, request and cancellation errors preserve
a failed report and available timeline rows.
When the timeline remains writable, the final row waits for canceled requests to
finish recording so interval counts remain consistent with the failed load's
aggregate counters. A timeline I/O or size-limit error can prevent a final row;
the failed summary retains aggregate counts and the available rows remain intact.

For matched comparisons, use a new directory/report and
`--automatic-compaction=false` or `--soak-faults=false`; the report records these
settings and omits only the corresponding coverage requirement. All post-load
correctness checks remain. The tool supplies measurements for the chosen host,
not a universal latency threshold. Actual independent-host qualification remains
pending disposable hosts with known backing storage.

### Development soak measurements

On 2026-10-17, one sequential enabled/disabled pair used identical binaries,
four workers, an aggregate target of one workflow per second, 60 seconds of
admission per durability mode, one cycle, a 512-workflow ceiling, 64 KiB bodies,
seed 1 and a 16-record cleanup trigger. Each load completed 60 workflows and both
live interruptions; all 13 checks per mode passed, with zero unexpected request
failures. Interval counts matched the aggregate counts exactly.

| Durability | Automatic cleanup | Observed load cleanup cycles | Whole-mode sampled replay peak (MiB) | Load PUT p95 (ms) | Load GET p95 (ms) |
| --- | --- | ---: | ---: | ---: | ---: |
| Local | Disabled | 0 | 159.1 | 58.6 | 15.2 |
| Local | Enabled | 95 | 10.5 | 88.5 | 21.3 |
| Replica | Disabled | 0 | 159.1 | 316.8 | 71.4 |
| Replica | Enabled | 143 | 10.1 | 323.8 | 75.0 |

Enabled loads observed 735 primary journal records and 180 replay files pruned
per load. Other space/byte cleanup triggers retained their defaults; this test
does not isolate the record-count trigger. Peaks include post-load mode checks
and are sampled lower bounds. Cleanup reduced retained history and increased
request p95 in this pair. One shared-host pair at a capped offered rate does not
establish sustainable capacity or a general latency effect.

An earlier unpaced four-worker local-durability run with cleanup disabled
completed 248 workflows before its first interruption exceeded the 30-second
finite-position rejoin deadline. Its failed report and timeline were retained;
the deadline and request-error handling were preserved. Use the pacing control
to compare chosen offered loads, and retain overloaded runs as evidence when
selecting replication work budgets and capacity. Independent-host qualification
remains pending.

A separate OpenSSH run used 30 seconds per mode at the same one-workflow-per-second
target. Each mode completed 30 workflows, both live interruptions and all 13
checks, including planned promotion. It observed 49/72 cleanup cycles and
372–374 records/91–92 replay files pruned. The two participants shared a kernel
and filesystem; physical failure domains remain unverified.

## Repeatable local load and recovery qualification

Build the daemon, control tool and qualification runner with the pinned Go
toolchain. Supply a **new** work directory and report filename. This runner creates
only disposable storage, node processes and a customer gateway with isolated
in-memory state; it does not accept existing node directories or live endpoints.

```sh
go build -o /tmp/object-storaged ./cmd/object-storaged
go build -o /tmp/object-storagectl ./cmd/object-storagectl
go build -o /tmp/object-storage-qualify ./cmd/object-storage-qualify

/tmp/object-storage-qualify \
  --daemon /tmp/object-storaged --ctl /tmp/object-storagectl \
  --work-dir /tmp/object-qualification-run \
  --report /tmp/object-qualification-report.json \
  --durability both --workers 4 --cycles 2 \
  --duration 2s --max-workflows 32 --object-bytes 65536 --seed 1
```

Both durability modes run independently. Each load cycle performs concurrent
PUT/GET, overwrite, conditional rejection, DELETE/missing GET, multipart completion
with a 5 MiB first part, and abort. It compares acknowledged bytes, SHA256 digests,
ETags, current/deleted state, historical receipts, gateway receipt settlement and
reconciled capacity. The continuous follower is a real `object-storagectl` process.
Worker keys are independent; this profile does not measure competing writes to
one key. The generator coordinates DELETE phases against concurrent writes and
multipart sessions because gateway deletion admission requires a quiescent bucket.
A separate check requires deletion to be refused while a multipart reservation
is active, with no provider DELETE dispatched.

After each cycle it drains traffic, catches up, stops processes, compacts both
disks, verifies a backup, checks deliberate backup corruption is rejected, restores
and rejoins a new follower, then restarts and verifies the original model. Fault
checks discard a completed provider response, drop a PUT before forwarding,
partition replication, SIGKILL/restart follower and primary processes, reject a
write below an artificially raised free-space floor, and execute the supported
planned promotion. Replica mode binds a fresh standby before successor writes.
The lost-acknowledgement drill overwrites through the private operator adapter
while the original customer receipt stays pending, then recovers its historical
proof. Missing or unconfirmed proofs must preserve capacity holds; recovery must
not replay PUTs. The space test does not fill the host disk or inject kernel I/O faults.

The report separates per-cycle performance from named correctness checks. Each
operation has attempts, successes, expected rejections, unexpected failures,
successful payload bytes and nearest-rank p50/p95/p99/max latency in milliseconds.
Throughput includes in-flight work draining after the admission window closes.
`stop_reason` distinguishes duration, workflow ceiling, failure and cancellation.
A run which hits its workflow ceiling early is not a full-duration soak.
Each admitted workflow has a 30-second deadline covering its operations and
bucket-coordination waits. Accumulated lag or contention can exhaust that bound;
the resulting failure is recorded even if the source and follower later catch up.

Resources include sampled daemon, follower and runner RSS peaks, physical file
bytes (including backups), journal/replay peaks and records held by replication.
Samples occur every 200 ms; successful metrics/disk sample counts and a coverage
check distinguish missing measurements from zeroes. Report sample errors, including restart windows, and
treat peaks as lower bounds. Probes and the in-process proxy/gateway consume
shared host resources. Daemon scrub is disabled for this profile. JSON records
the exact executable SHA256 identities, seed, configuration and toolchain.
It contains no credentials; generated credentials exist only in the isolated
fixture and child environment. Child logs and data remain in the private work
directory for inspection; all processes are stopped/reaped before the runner exits.

Correctness failures exit nonzero and still write a failed report. Reports use
0600 permissions and refuse replacement. Keep a new directory/report for each
run. Defaults are four workers, two cycles, two seconds and 32 workflows per cycle,
with 64 KiB object bodies. Limits are 16 workers, 16 cycles, 256 workflows per cycle,
8 MiB object bodies and 30 minutes of configured admission time per mode. The
sampled disposable file budget is 2 GiB per mode; already admitted work and backup
copies can temporarily overshoot it. Existing node free-space checks still apply.

For longer runs, increase `--cycles` and `--duration` while retaining bounded
workflows and checking `stop_reason`. Compare worker counts 1 and 4 and larger
`--object-bytes` values on the same host/configuration. Replay preparation now
copies/hashes outside the catalog lock, with metadata/fsync work still serialized.
Continuous replication drains bounded steps immediately within its work budgets
and waits for changes when idle; compare it with the earlier polling worker using
the same configuration and actual workflow/sample counts. Use measurements to select an optimization,
without assigning arbitrary hardware-independent performance pass thresholds.

Before publication preparation (ADR-1554), a 2026-10-16 comparison of the polling
and change-wait loops ran two sequential matched pairs: four workers,
two cycles, four workflows per cycle, a 30-second admission ceiling, 64 KiB
objects and seed 1. The same daemon/runner binaries were used; a Go source overlay
restored only the previous CLI polling loop for the baseline. Every run completed
all four workflows per cycle and passed 14 checks in each durability mode.
Ranges below span the four measured cycles per variant; PUT samples include the
expected conditional rejection (12 attempts per cycle).

| Observation | Previous polling loop | Change waits |
| --- | --- | --- |
| Replica-mode mixed requests/second | 2.27–2.31 | 4.19–17.63 |
| Replica PUT p95, milliseconds | 1,073–1,136 | 507–1,752 |
| Local-mode mixed requests/second | 36.50–41.79 | 4.58–32.90 |
| Local PUT p95, milliseconds | 137–239 | 205–3,060 |
| Whole-run user + system CPU, seconds | 13.30–30.99 | 23.84–30.77 |

CPU includes the runner and reaped children across both modes and recovery drills,
not just load. Runtime CPU selection was automatic, with five visible/affinity CPUs
and a four-core cgroup quota. These samples show higher replica throughput,
variable tail latency and CPU cost, and lower local foreground throughput in the
matched runs. They do not establish a general latency or CPU efficiency gain.
Profile publication-lock work and follower resource contention before tuning
batching or expanding usage. One local cycle had a pronounced latency outlier;
keep it in the comparison.

A separate run using the earlier two-second admission profile and eight-workflow
ceiling also passed all 14 checks in both modes: eight workflows per local cycle
and seven per replica cycle. A larger polling baseline attempted before the
matched comparison exhausted the existing 30-second workflow deadline; retain
that failed report separately from the matched performance ranges.

The report explicitly describes local processes on a shared host/filesystem and
an in-memory gateway ledger with a controlled recovery clock. It does not qualify
independent disks/hosts, PostgreSQL recovery, WAN partitions, billing reports or
the full provider release contract. Production enrollment remains unchanged.

The process integration test is opt-in and consumes the supplied built binaries:

```sh
FAAS_OBJECT_QUALIFICATION_DAEMON=/tmp/object-storaged \
FAAS_OBJECT_QUALIFICATION_CTL=/tmp/object-storagectl \
go test -race ./pkg/objectqualification -run '^TestQualificationBuiltProcesses$' \
  -count=1 -timeout=5m
```

### Qualify settlement delivery and indexed cleanup protection

Add `--recovery-retention` to a local or SSH qualification run to opt its fresh
fixtures into format 5. [ADR-1564](adr/1564-owned-object-storage-retention-qualification.md)
adds this profile without changing the default format-4 run. It enables the
isolated gateway's settlement outbox and requires current matching binaries on
both participants. It never opens existing node data or changes a live backend.

The profile drops one selected settlement reply after the node acknowledges it.
It requires the central terminal result and retryable job to survive, then retries
without replaying object bytes. A historical acknowledged proof and a current
unacknowledged proof must survive while the cleanup worker runs. After maintenance,
backup/restore, primary process recovery and promotion, another exact signed
acknowledgement must return the original node timestamp. Terminal ready jobs must
drain while unknown waiting jobs and their existing capacity holds remain.

Reports add per-mode `recovery_retention` evidence: `window_seconds`,
`lost_reply_retried`, observations of acknowledgement time, cleanup worker steps,
indexed candidates and ready/waiting jobs. The field
`aged_retirement_exercised` is explicitly false. Only the controller's in-memory
retry clock advances; node clocks, request issue times and acknowledgement records
remain on real time. A minutes-long soak cannot establish that 30 days elapsed or
qualify eligible retirement. Controlled-clock aged collection and crash replay
remain covered by the object-node tests.

Run the built-process regression alongside the default profile:

```sh
FAAS_OBJECT_QUALIFICATION_DAEMON=/tmp/object-storaged \
FAAS_OBJECT_QUALIFICATION_CTL=/tmp/object-storagectl \
go test -race ./pkg/objectqualification \
  -run '^TestQualificationBuiltProcesses(RecoveryRetention)?$' \
  -count=1 -timeout=5m
```

For independent-host evidence, combine `--recovery-retention` with
`--require-dedicated-storage` and the host selections below. Run the controller
on the intended primary host. SSH access and dedicated data mounts must already
exist; staging test binaries does not authorize selecting a production node.
The local/shared-host profile remains development evidence. PostgreSQL crash
recovery and independent physical failure domains remain outside its claims.

The 2026-10-17 development run passed all 16 standard-profile checks in both
durability modes with format 5, including the selected lost settlement reply,
unchanged acknowledgement times across restart/promotion, healthy cleanup steps
and preserved unknown waiting jobs. A real loopback OpenSSH run also passed all
16 checks per mode, including primary-role reversal. Its shared kernel identities
and overlay storage remained unqualified: the dedicated-storage guard rejected
both placements before creating fixture directories. This validates preparation
and SSH transport; execution on independent hosts and dedicated disks is pending
operator-provided host access and backing-storage selections.

## Check hosts and data filesystems before qualification

Run the controller on the intended primary Linux host, with a second disposable
host reachable through an operator-configured SSH alias. Install identical
daemon, control and qualification executables on both. Select existing dedicated
data mounts with fresh work paths; record their actual drives, controllers and
storage failure domains separately.

The new `preflight` command inspects those selections without creating node data,
generating node credentials or starting service listeners. It writes only the
requested exclusive 0600 report. For example, run this on the primary host with
an existing `/srv/object-qualification` data mount on each participant:

```sh
/opt/object-qualification/object-storage-qualify preflight \
  --daemon /opt/object-qualification/object-storaged \
  --ctl /opt/object-qualification/object-storagectl \
  --work-dir /srv/object-qualification/run-01 \
  --follower-ssh object-qualification-standby \
  --follower-agent /opt/object-qualification/object-storage-qualify \
  --follower-daemon /opt/object-qualification/object-storaged \
  --follower-ctl /opt/object-qualification/object-storagectl \
  --follower-work-dir /srv/object-qualification/standby-run-01 \
  --report ./object-preflight-01.json
```

Preflight requires distinct hashed kernel boot identities and matching hashes
for all three executables. It observes the exact Linux `statx` mount ID and
mountinfo entry, including stacked mounts. Parents must have no symlink
components and must permit traversal/writes. Storage must be local block-backed
ext4 or XFS, mounted outside the namespace root filesystem, with the whole
filesystem root rather than a bound subtree. A bind of the namespace root device
at another path also fails. Overlay/network filesystems, read-only mounts,
inconsistent observations, existing work paths and missing mount identity fail.

The current minimum available space is 3.5 GiB per participant: the existing
2 GiB fixture budget, 1 GiB node floor and 512 MiB recovery margin. Require
available inodes as well. Leave additional room for both modes, backup copies
and admitted work; this check neither reserves blocks nor enforces an OS quota.
Mount IDs and device major/minor numbers are local to a kernel namespace and
cannot identify shared physical disks across machines.

Inspection uses one private JSON request over noninteractive OpenSSH, with strict
host-key checking, configured forwards/local commands disabled and no control
master reuse. Normal SSH routing/authentication remain operator-configured. It
does not configure connectivity, accept an unknown host key, install software or
mount/format disks. Incomplete/failed SSH inspection preserves a failed readiness
report. An omitted follower produces a partial local report with `ready: false`.
The private `--inspect` helper must be upgraded on the follower as well.

For the actual run, add `--require-dedicated-storage`. It repeats inspection
inside that invocation before creating the controller fixture and includes the
result in the normal report; it does not import or trust a saved readiness file.
For a first bounded native soak, use the same placement flags as above:

```sh
/opt/object-qualification/object-storage-qualify \
  --require-dedicated-storage \
  --recovery-retention \
  --daemon /opt/object-qualification/object-storaged \
  --ctl /opt/object-qualification/object-storagectl \
  --work-dir /srv/object-qualification/run-01 \
  --follower-ssh object-qualification-standby \
  --follower-agent /opt/object-qualification/object-storage-qualify \
  --follower-daemon /opt/object-qualification/object-storaged \
  --follower-ctl /opt/object-qualification/object-storagectl \
  --follower-work-dir /srv/object-qualification/standby-run-01 \
  --soak --durability both --workers 4 --cycles 1 \
  --duration 5m --max-workflows 4096 --object-bytes 65536 \
  --compaction-records 64 --soak-workflows-per-second 1 \
  --report ./object-host-run-01.json
```

Readiness is an observation, not a mount lock, write/fsync certification or
physical failure-domain proof. `physical_failure_domains_verified` remains false.
Different virtual machines or local-looking block devices can share underlying
hardware/storage. Preserve the operator's storage inventory with the eventual
run evidence. The actual workload still checks acknowledged bytes, historical
receipts, capacity holds, replication, restart/rejoin, backups and promotion.

The guard rejects `--allow-shared-host`. Existing local and explicit shared-host
transport profiles remain available without the guard. Inspection/readiness uses
document version 1; the existing agent protocol 2 and qualification report 3
remain unchanged. Preflight itself does not alter node storage formats. Normal reports add optional preflight
evidence and `require_dedicated_storage` configuration.

See [ADR-1560](adr/1560-owned-object-storage-host-storage-preflight.md) for the
observation bounds and refusal conditions. Independent-host execution is pending
test hosts with configured access and known backing data mounts.

The 2026-10-17 development checks exercised the built helper over real loopback
OpenSSH. Complete inspections and matching executable hashes were recorded,
while the shared kernel and overlay/root storage correctly returned not ready.
The guarded runner created no fixture directories. Missing host configuration,
an older helper without inspection support and a mismatched control binary also
preserved failed evidence without fixture data. A separate explicit shared-host
SSH compatibility run passed all 12 standard checks in both durability modes,
including promotion and host-role reversal; both topologies recorded
`distinct_kernel_boots: false`. These checks validate preparation and transport,
and do not supply independent-host or independent-disk qualification evidence.

## Qualification across SSH hosts

Run the controller on the primary Linux host. Preinstall the same built daemon
and control binaries plus `object-storage-qualify` on a second disposable Linux
host; remote and local daemon/control SHA256 identities must match. Use an SSH
alias configured with noninteractive authentication and a verified known-host
entry. The account needs local and remote TCP forwarding, permission to execute
the binaries, and an existing parent directory on its intended test filesystem.
No root access or system network configuration change is needed.

```sh
/tmp/object-storage-qualify \
  --daemon /tmp/object-storaged --ctl /tmp/object-storagectl \
  --work-dir /srv/object-tests/controller-run-01 \
  --report /srv/object-tests/report-run-01.json \
  --follower-ssh object-qualification-standby \
  --follower-agent /opt/object-tests/object-storage-qualify \
  --follower-daemon /opt/object-tests/object-storaged \
  --follower-ctl /opt/object-tests/object-storagectl \
  --follower-work-dir /srv/object-tests/standby-run-01 \
  --durability both --workers 4 --cycles 2 \
  --duration 30s --max-workflows 32 --object-bytes 65536 --seed 1 \
  --replication-work-bytes-per-second 67108864 \
  --replication-work-entries-per-second 256 \
  --replication-delay 10ms --replication-bytes-per-second 33554432
```

Use fresh paths for every run. The controller directory and report must not
exist; the remote prefix creates separate `standby-run-01-local` and
`standby-run-01-replica` directories, both of which must not exist. The runner
refuses existing storage. `--ssh-config` optionally selects an OpenSSH config
file; otherwise normal SSH configuration applies. It always enables batch mode
and strict host-key checking. Generated node credentials travel in the private
SSH stream and child environment. Retained logs/data and reports are private;
the runner stops/reaps children and removes its temporary SSH control socket.

The remote agent carries out bootstrap, compaction and planned promotion on the
remote disk. During promotion the verified successor backup returns over SSH,
and a fresh standby starts on the original primary host. Customer requests keep
the same gateway backend identity and existing receipt/capacity checks. Existing
failure scenarios, backup checks and acknowledged-byte validation run in both
durability modes. Service and metrics listeners remain bound to loopback.

Replication travels from the remote follower through a reverse SSH forward and
the controller proxy to the primary. After promotion, the route uses the local
proxy and a forward to the remote primary. Reports explicitly include this
controller/SSH overhead. They do not measure a direct private link. The optional
delay adds at most two seconds per replication RPC; payload bandwidth shaping
uses a shared 64 KiB burst and affects only replication blob responses. Zero
delay/zero bandwidth rate selects an unshaped transport. These are application
faults, including the existing 503 partition; they do not emulate packet loss or
kernel TCP behavior, and active change waits can finish before a partition takes
effect on their next RPC.

Version 2 reports add the configured work rates/network profile, remote executable
hashes, platform, filesystem type, hashed kernel boot identities and
`distinct_kernel_boots`. Equal identities are rejected by default. For a local SSH
transport exercise, explicitly pass `--allow-shared-host`; its report records the
shared kernel and is not independent-host evidence. Distinct kernel boots also
do not prove independent physical disks: record the hosts' actual backing storage
separately. `physical_failure_domains_verified` remains false.

Remote RSS and file probes execute on the remote host. Reports aggregate local
and remote sampled file bytes, require successful `remote_samples`, and record
`recoveries` for partition healing and primary/follower restarts. `rejoin_ms`
starts at healing/restart and ends at the ordinary applied/confirmed catch-up
check, including startup when applicable. It is distinct from the total named
scenario duration. Probe overhead and missed peaks retain the local runner's
limitations; the gateway ledger still uses isolated in-memory state.

For a budget comparison, repeat identical seed, object size, workers, cycles and
workflow ceilings with new controller/report/remote paths. Start with an unshaped
run, then compare 64 and 32 MiB/s byte-work limits under the same delay and payload
rate. Keep actual workflow counts, stop reasons, attempts, errors, p95/p99,
throughput, peak sampled lag and rejoin time together. Reaching a workflow ceiling
early is not a duration soak. The existing 30-second workflow deadline and
10-second replica response wait still apply; a deliberately slow profile can
fail or leave an uncertain response, which must retain its receipt/capacity hold.
Do not select production defaults from shared-host transport validation.

On 2026-10-17, local OpenSSH validation passed all 14 checks per durability mode
for three profiles: unshaped transport with 64 MiB/s work, and 10 ms RPC delay /
32 MiB/s payload pacing with either 64 or 32 MiB/s work. Every cycle completed
four workflows. Reports explicitly record the shared kernel/filesystem; these
short runs validate the controls and lifecycle, not separate-host capacity.
Actual EOF, killed SSH clients and a killed controller stopped their managed
workers. Race-enabled local and SSH process integration tests also passed.
Independent-host measurements remain pending configured disposable hosts.

The SSH process integration test is opt-in. Set
`FAAS_OBJECT_QUALIFICATION_SSH_HOST`, `FAAS_OBJECT_QUALIFICATION_SSH_CONFIG`
(optional), `FAAS_OBJECT_QUALIFICATION_AGENT`,
`FAAS_OBJECT_QUALIFICATION_REMOTE_DIR` (a fresh prefix), and the existing daemon
and control binary variables, then run:

```sh
go test -race ./pkg/objectqualification -run '^TestQualificationSSHProcesses$' \
  -count=1 -timeout=5m
```

This test expects daemon/control binaries at the same absolute paths on both
hosts. `FAAS_OBJECT_QUALIFICATION_SHARED_HOST=1` permits only the explicitly
reported shared-host transport fixture. Independent-host qualification requires
the actual disposable targets and separate storage; no such capacity results are
implied by local SSH validation.

## Diagnose replication throughput

Continuous catch-up emits one JSON progress record per bounded attempt, including
an `observed_at` timestamp and `error` for failed attempts. The additive `profile`
contains wall-time milliseconds for source position checks, entry fetches,
durable journal reception, download, durable apply, applied checkpoint and
reporting. `download_verification_ms` is included in `download_ms`.
`byte_budget_wait_ms` and `entry_budget_wait_ms` are also included in their
respective phases; do not add them again. Durable phases include metadata/copy
work and fsync, not just device sync latency. `total_ms` excludes catalog-lock
acquisition, between-step maintenance and idle change waits. See the separate
follow counters for idle waits, retries and lag.

Qualification now saves each follower process's progress in an owner-only
`follower-progress-*.jsonl` file in its local or remote mode directory. Each file
is bounded to 16 MiB with 32 KiB rows, preserving complete rows across pipe
writes. `{"truncated":true}` ends capture at either limit without stopping the
follower. Killed processes can omit an unfinished observation; partial rows are
discarded. Progress is diagnostic and never substitutes for an applied cursor,
replica confirmation or receipt proof. The existing version-3 report and
protocol-2 SSH agent remain compatible.

New sources advertise bounded journal pages in authenticated status responses.
An upgraded follower fetches up to 32 contiguous records and 4 MiB of canonical
JSON per page; two page responses may be active per handler. Page slots are
separate from payload transfers, status, cursor and confirmation requests. An
older source without the advertisement keeps single-entry reads. A failed or
corrupt advertised page fails normally, without a fallback that hides it.

Prefetch reserves the requested entry tokens and verifies the full returned
prefix before receiving its first record. Unused tokens after a short page,
partial transfer or error remain conservatively charged. Every record keeps
its existing durable received/apply/checkpoint ordering; prefetched records
exist only in bounded memory. Replica confirmations and registered retention
pins never advance to prefetched positions. Byte/entry work defaults and the
existing response, workflow and recovery deadlines remain unchanged.

For a capacity trial, keep the seed, workers, object size, durability mode,
compaction controls and work budgets fixed. Use fresh report/data paths for each
offered start rate, then compare completed workflows, errors, request p95/p99,
applied records/second, lag growth and interruption recovery. Increase the
workflow ceiling enough to cover the admission duration. A run that stops early
or misses its rejoin deadline is failed evidence even if some requests succeed.
Select an offered rate below sustained replay throughput with room to recover
from pauses. Shared-host measurements do not establish production capacity or
independent-host durability. Use the [write admission controls](#write-admission-under-storage-pressure)
below to protect headroom while retained history grows.

See [ADR-1558](adr/1558-owned-object-storage-replication-profiling.md) for the page
bounds, capability negotiation, profiling semantics and validation contract.

### Development replication measurements

On 2026-10-17, sequential local trials used four workers, the same seed and
64 KiB/5 MiB multipart workload, a 16-record compaction trigger and unchanged
64 MiB/s / 256-entry/s work budgets. Each trial admitted traffic for 60 seconds.
Completed counts include in-flight drain; finite workers and replica gating can
underfill an offered start target. Lag is sampled pinned-record retention lag,
and peaks cover the whole mode including recovery checks.

| Journal fetch / durability | Target workflow starts/s | Completed workflows | Peak lag records | PUT p95 ms | GET p95 ms | Longest live-fault rejoin s |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Single / local | 2 | 120 | 126 | 87.8 | 23.4 | 3.80 |
| Batch / local | 2 | 120 | 49 | 84.8 | 17.9 | 1.04 |
| Batch / replica | 2 | 116 | 13 | 557.9 | 127.7 | 0.23 |
| Batch / local | 3 | 165 | 154 | 199.0 | 49.0 | 3.03 |
| Batch / local | 4 | 210 | 775 | 223.8 | 37.7 | 14.80 |

Each row passed both scheduled interruptions and all 13 correctness/recovery
checks, with zero unexpected customer request failures and matching interval
counts. At target four, sampled lag was still 762 records near the end of
admission. A passing short recovery drill alone does not make that rate
sustainable; target two retained more measured recovery margin in this fixture.

In separate unpaced comparisons, profiled replay increased from 23.07 to
34.23 records per active catch-up attempt second, and journal fetches fell from
30.9% to 2.6% of that time. This excludes lock acquisition, between-step cleanup,
startup and idle waits. The baseline failed its first recovery deadline at
50 seconds. Batch fetching completed 280 workflows over 60-second admission and
all 13 checks; its second recovery took 28.0 seconds and finished at about
69 seconds after traffic admission had stopped. Lag still grew to 1,479 records
at 60 seconds, with higher request p95 than the shorter failed baseline.
Keep the adverse result and differing observation lengths with the improvement.
These are shared-host development trials, not independent-device service
capacity measurements. The admission policy below protects growing backlog.

## Write admission under storage pressure

`object-storaged` enables write admission by default. Before staging a new PUT
or part, freezing a new completion, or creating a bucket/session, reserve
conservative room for that operation alongside concurrent admitted work.
Pressure responds with S3 `503 SlowDown` and `Retry-After: 1`. A later request
or advisory probe automatically reopens each pressure reason at its recovery
watermark; no operator reset is required.

| Flag | Default close watermark | Recovery flag/default |
| --- | --- | --- |
| `--admit-lag-high` | 4,096 projected pinned records | `--admit-lag-low=2048` |
| `--admit-records-high` | 900,000 projected retained journal/payload files | `--admit-records-low=600000` |
| `--admit-journal-high` | 939,524,096 bytes (896 MiB) | `--admit-journal-low=671088640` (640 MiB) |
| `--admit-replay-high` | 7,696,581,394,432 bytes (7 TiB) | `--admit-replay-low=5497558138880` (5 TiB) |
| `--min-free-bytes` | Free bytes after reservations below 1,073,741,824 (1 GiB) | `--admit-resume-free-bytes=0` derives the floor plus 536,870,912 (512 MiB) |

Thresholds are startup configuration. Validate low < high and stay within the
hard retention bounds. `--write-admission=false` disables this extra policy;
existing transfer slots, disk reservations and hard retention limits still
apply. Library stores are opt-in through `ConfigureWriteAdmission` before
serving. A restart rebuilds durable usage, while runtime latches and counters
start fresh.

Lag follows the slowest registered follower or mandatory replica-confirmation
pin. Without a pin, lag alone does not close admission. Retention and disk
checks continue. A failed free-space probe closes new admission until a probe
succeeds. Each metric's latch clears independently.
Latched retained-history or disk pressure also wakes automatic compaction below
its usual watermarks, with unchanged work bounds and required retention pins.

Estimates include three records per PUT, one per part, six per completion,
one per bucket/session, the maximum record size, a potential replay payload,
and two body copies for disk use. An in-flight reservation may overlap already
published retained bytes. Deduplication does not reduce an admission estimate.
Large operations can therefore be throttled before the filesystem is full.
This policy reserves headroom, not filesystem blocks; other processes and
already durable recovery work can still exhaust a shared filesystem.

Receipt recovery, exact historical receipt replay, frozen completion resumes,
reads, deletion/abort/expiry, compaction and replication continue under their
existing authority/durability gates. Accepted in-flight operations finish
without another pressure check. The controller neither removes retention pins
nor downgrades replica durability. An unlimited stream of cleanup metadata is
outside its new-write bounds.

For owned-node backends, the branded gateway sends a signed, read-only HEAD
Bucket pressure hint before a new credential upload hold or multipart transfer/
completion admission. Visible pressure is rejected before staging a PUT body
or creating a new hold. Prepared PUT URLs and existing completion resumes keep
their prior paths. Normal R2/S3 providers make no extra request. Older nodes
without the hint remain compatible during a rolling upgrade.

A ready hint is not a reservation or durability proof. The node checks again
at actual admission. If pressure rises after the hint, or a response is lost,
the already dispatched write remains uncertain and its capacity hold remains.
Missing receipts and HTTP 5xx never authorize releasing a hold or replaying an
unknown PUT. Direct/prepared paths retain these same conservative rules.

Observe `faas_object_node_write_admission_enabled`, `_blocked`, `_active`,
`_accepted_total`, `_rejected_total`, `_reserved_records`,
`_reserved_replay_bytes`, `_reserved_disk_bytes`, and the
`faas_object_node_write_admission_pressure_{lag,records,journal_bytes,replay_bytes,free_bytes,disk_probe}`
metrics (accepted/rejected totals are counters; the rest are gauges).
Accepted/rejected counts cover actual node operation admission, not
advisory probes; normal request metrics count probe responses. Scrapes use cached
values, so block state is the last admission/probe observation. Check follower
reachability and applied reports, then safe compaction progress and disk usage.
Restore required replica/report progress or add disk capacity to relieve
pressure; do not delete journal files or retire a required follower to reopen.

See [ADR-1559](adr/1559-owned-object-storage-write-admission.md) for reservation
and gateway uncertainty semantics.

### Development admission measurements

On 2026-10-17, a disposable built-process local-mode drill paused a registered
follower for three bursts of 64 attempted 64 KiB writes each, using ordinary
compaction watermarks. These deliberately small admission thresholds exercise
control behavior; they are not the daemon defaults or a device throughput benchmark.

| Pressure | Configured high / low | Accepted / throttled across three bursts | Longest catch-up and reopen |
| --- | --- | --- | --- |
| Pinned lag, tracked PUTs | 32 / 8 records | 30 / 162 | 0.395 s |
| Replay bytes, untracked overwrites | 1 MiB / 512 KiB | 45 / 147 | 1.121 s |

Each lag burst stopped at 30 durable records and each replay burst at 960 KiB
of retained replay payloads; the proposed next write reached the high
watermark. Throttles published no journal records or fabricated receipts.
Resuming the follower's built CLI and safe automatic compaction reopened
admission without policy changes. Acknowledged bytes remained readable under
pressure and survived primary restart. These processes share one host and
filesystem; the observations establish development behavior only.

Real gateway/adapter checks additionally verify that a visible pressure hint
reads no PUT body and creates no new hold, while post-hint rejection and a lost
committed response retain their existing uncertain holds with one write attempt.
A durable frozen multipart completion resumes while fresh admission is blocked.
Replica checks verify that a hint cannot confirm data and that mandatory
confirmation pins reopen admission only after applied confirmation.

Final built-process qualification passed all 12 standard checks in each durability mode.
The preceding 60-second-per-mode soak, before the additional maintenance trigger,
also passed all 13 checks per mode and both scheduled interruptions, completing
120 local and 76 replica workflows. Other Go validation ran on this shared
host during that soak; these counts are correctness evidence, not a capacity
comparison with earlier trials.

## Local checks

```sh
go test -race ./pkg/objectnode
go test -race ./pkg/objectinventory
go test -race ./pkg/s3gateway -run '^Test(DiskNode|DiskReplicationWait|HTTPReplicationWait)'
go test -race ./pkg/objectstorage -run 'OwnedReceipt|OwnedMultipart|OwnedInventory|ManagedConfirmation'
go test -race ./pkg/apid/object -run '^TestOwned(Receipt|Multipart|Inventory)'
go test ./pkg/state -run '^TestReceiptRejectionRecovery'
go test ./cmd/object-storaged
go test -race ./pkg/s3gateway -run '^Test(DiskWriteAdmission|GatewayWritePressure)'
go test ./cmd/object-storagectl
go test -race ./pkg/objectqualification ./cmd/object-storage-qualify
```

These checks include AWS Go SDK requests through the existing branded gateway
and real S3 adapter, lost-response settlement through the apid worker, plus
storage subprocesses killed during an upload and after durable PUT/multipart
commit intents. They also verify multipart resume, actual assembled bytes,
checksums, corrupt parts, races with abort/expiry, coherent inventory and capacity
reconciliation, read-only diagnostics for damaged references, checksum scrubbing,
restartable hash checkpoints and retained corruption findings. Backup checks
cover complete source loss, restored receipts and multipart resume, corruption,
unsafe paths, destination races and processes killed before/after publication.
Replication checks add ordered mutation replay, fenced snapshot bootstrap,
resumed ranges, partitions, corrupt transfers, checkpoint failures, process
kills, authenticated transfer and CLI progress. Switchover checks add streaming
drain, competing targets, permanent restart fencing, exact checkpoint promotion,
new epochs, successor backup/rejoin and interrupted authority publication.
Real gateway PUT/multipart recovery also runs through a handoff behind a stable
backend endpoint, retaining missing-proof capacity holds without replaying writes.
Replica-mode checks additionally cover applied confirmations, response/proof
gates, delayed negative receipts, replacement and downgrade safeguards, lost
reports, processes killed at confirmation checkpoints, and real gateway capacity
holds before and after replica confirmation. Compaction checks cover killed
processes across floor publication and unlink/fsync boundaries, shared payloads,
pending receipt recovery, registered/retired cursors, explicit reseeding,
compacted backups and promotion across the retained floor.
Change-wait checks cover acknowledgement churn, unchanged timeouts, checkpoint
uncertainty, missed-event races, authority changes, cancellation, bounded admission,
graceful shutdown, immediate partial transfers and reconnects without recopying.
Publication preparation checks cover reads during blocked copies, queue cancellation,
conditions/generation races, multipart abort and frozen completion, stale file
proofs and kills across preparation/install/journal boundaries. Work-budget
checks cover retained reads, resumed pacing, persistent entry tokens and bounded
bulk HTTP slots independent of status and confirmations.
They do not use an external provider or production credentials.
