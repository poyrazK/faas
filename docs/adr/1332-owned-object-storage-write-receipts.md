# ADR-1332: Durable write receipts for the owned object node

- Status: Accepted for the local prototype; no production enrollment.
- Date: 2026-10-15
- Related: [ADR-1331](1331-owned-object-storage-first-node.md), [ADR-535](535-recoverable-s3-gateway-puts.md)

## Context

A provider can commit a PUT while its acknowledgment is lost. The gateway
retains its dispatched admission and recovers it by checking a private receipt.
Current-object metadata stops proving that write after overwrite or deletion.
The owned node needs independent outcome retention before multipart or
distributed storage work can build on it.

## Decision

Store a durable journal entry per canonical upload-receipt UUID in a global
`receipts` directory. Bind its request hash to the physical bucket, bucket
incarnation, key, exact size, computed SHA256, stored HTTP/user metadata,
checksum selection and conditional-write headers. A separate record checksum
detects accidental changes to its outcome and object identity. This checksum is
not authentication against an operator with filesystem access.

An identical replay returns the original acknowledgment without changing the
current object. A conflicting replay returns `WriteReceiptConflict` (409).
Replays cannot restore deleted bytes, replace a later write or cross a bucket
incarnation. Conditional rejection is terminal for that receipt even if the
condition would succeed later. Invalid/incomplete bodies that never reach the
journal do not acquire a durable outcome.

Commit ordering is:

1. Stream, verify and fsync the staged body; install and fsync its immutable blob.
2. Atomically publish and fsync a `committing` receipt containing the intended
   object and the preceding blob identity.
3. Atomically publish and fsync the current-object pointer.
4. Atomically publish and fsync the `committed` receipt, then reclaim old bytes.

The catalog lock admits at most one unresolved intent globally. An uncertain
publication fences later mutations until that intent finishes; reads and
listing fail closed while it is unresolved. Receipt queries also retry finishing
the intent. Startup validates the journal and completes its pending intent
before orphan collection, rehashing the pending blob before publication. A
durable intent commits after restart even if the original request returned an
error. A rejected conditional write stores a durable `rejected` record before
returning 412 and never installs its candidate bytes.

Receipts survive overwrites, object deletion and bucket deletion. They retain
outcome metadata, not old bytes or S3 versions. There is no receipt expiration or
automatic garbage collection in this milestone: removing proof without a
gateway settlement/retention protocol could strand an admitted write. Startup
and receipt disk usage therefore grow with tracked operations. Monitor disk use;
do not manually prune receipts for space.

## Private protocol and placement

The S3 backend explicitly opts into `owned_node_receipts: true`. The flag adds
a protocol identifier to its placement fingerprint; all existing configurations
with the flag absent/false retain their exact previous identity. Use a new
development backend ID and namespace rather than changing an existing bucket's
fingerprint. Encryption and Object Lock capabilities cannot be declared with
this mode.

The adapter sends an operator-authenticated HEAD on the original bucket/key
with signed `X-Gregale-Write-Receipt: <uuid>`. The node accepts this selector only
for an unconditional object HEAD with no operation query or range. It returns
the original ETag, size, private receipt metadata and
`X-Gregale-Write-Receipt-Status: committed|rejected`. Normal HEAD/GET continue to
select the current object. Customer input does not select or reach this private
protocol through the public gateway.

The adapter requires the exact receipt, size, ETag and one valid terminal status,
with no native version/delete marker or encryption claims. Exact rejection
returns `ErrWriteRejected`. Missing, malformed or unavailable proof remains
uncertain and does not initiate generic native-version history probing. A 409
from an owned-node tracked PUT also remains uncertain: it may describe a
previously committed original operation.

The existing apid recovery worker can settle either exact committed proof or
explicit durable rejection under its recovery lease. Both MemStore and PgStore
reject stale workers and reject all other failure reasons for dispatched
recovery. Missing proof keeps the admission and bound mutation receipt. This
requires no database schema or SQL changes. Managed S3/GCS confirmation behavior
does not change.

Opening a version-1 node data directory upgrades its format marker to version 2
before any receipt can be written. Earlier binaries reject version 2. Do not
manually downgrade the marker: an older binary could garbage-collect a blob
referenced by a pending intent. Rollback requires a consistent pre-upgrade
backup of the entire node directory.

## Validation and remaining work

Tests cover identical/concurrent and conflicting replays, durable conditional
rejection, bucket recreation, receipt retention through deletion and restart,
publication failures before/after each commit boundary, corruption and missing
pending bytes, format upgrade, and a subprocess killed after a durable intent.
HTTP tests enforce the signed selector and exact terminal proof. An integration
test crosses the customer AWS SDK, public gateway, real S3 adapter, disk node,
restart and apid worker with a lost provider response, followed by overwrite,
delete or rejection. Missing receipts stay pending; recovery sends no PUT.
State contracts exercise lease and failure-reason fencing for MemStore and
PgStore (PostgreSQL tests require the local test database).

This remains a single-node development prototype. Multipart, receipt retention
coordination, indexed metadata, authoritative usage, scrubbing, backups,
replication and distributed placement/fencing remain separate milestones.

[ADR-1333](1333-owned-object-storage-multipart.md) adds multipart sessions and
extends the journal to exact completion proof. The current disk format is 3.
