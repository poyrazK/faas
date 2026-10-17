# ADR-1544: Durable multipart uploads on the owned object node

- Status: Accepted for the local prototype; no production enrollment.
- Date: 2026-10-15
- Related: [ADR-1542](1542-owned-object-storage-first-node.md), [ADR-1543](1543-owned-object-storage-write-receipts.md), [ADR-544](544-durable-s3-multipart-completion-identities.md), [ADR-1038](1038-durable-s3-multipart-checksums.md)

## Context

The public gateway already admits multipart uploads, records their selected
parts and conditions, and recovers uncertain completion. The owned node needs
durable sessions and parts to support that contract on local disks. Assembly
must not expose partial bytes, lose acknowledged parts after restart, or race
abort and expiry. A completed object may be overwritten or deleted before the
gateway recovers a lost acknowledgment.

## Decision

Add native CreateMultipartUpload, UploadPart, ListParts,
ListMultipartUploads, CompleteMultipartUpload and AbortMultipartUpload to the
private node. Tenant ownership, admission, credentials and public upload UUIDs
remain in the existing gateway and apid services. No database migration or
deployment enrollment is introduced.

Each native upload has a sealed `multipart/<native-id>/session.json`, a bucket
incarnation, initial metadata, checksum policy, captured total-size limit and
24-hour expiry. Each part number selects a separate sealed record and immutable
UUID-named blob. Acknowledgment follows blob fsync, atomic pointer replacement
and directory fsync. Replacing a part accounts for its selected bytes, while
disk admission also reserves temporary replacement bytes. Startup validates
metadata before reclaiming unreferenced part blobs and interrupted staging files.

For gateway-created uploads, derive a distinct native UUIDv5 from the public
receipt UUID and a fixed owned-node namespace. This mapping makes initiation
idempotent without adopting a different session on the same key. It is an
identity mapping, not authentication or secrecy. Both identities remain scoped
by operator authentication, physical bucket/key and bucket incarnation. Direct
operator SDK initiation without a receipt generates one internally. Terminal
session tombstones prevent reuse and are excluded from active-upload listings.

## Completion and recovery

Under the catalog lock, validate the entire ordered selection, exact ETags,
non-final minimum part sizes, selected checksums and total size. Fsync the part
directories and persist a sealed completion manifest before assembly. The
manifest binds the native identity, selected part numbers/ETags/checksums, exact
total size, final checksum and conditions. Later part writes cannot change a
frozen manifest. An identical completion can resume; a changed selection or
condition conflicts.

Assembly releases the catalog lock and streams one part at a time into a staged
object. Memory and open descriptors do not grow with object size. Rehash each
part against its stored SHA256 while copying and calculate the assembled SHA256.
The S3 multipart ETag is MD5 of the binary part MD5s plus the part count. Support
the existing explicit checksum policies: composite SHA1/SHA256 and CRC checksums,
and full-object CRC32/CRC32C/CRC64NVME. Selected checksum parts must be consecutive
as required by the shared checksum contract.

Publish the assembled object through ADR-1543's write-ahead receipt protocol.
Multipart receipts have a separate kind, the public session marker and the
completion-manifest hash. They cannot establish single-PUT proof. After the
terminal receipt is durable, mark the session completed/rejected and reclaim
its part bytes. Startup recovers receipt intents before part or object garbage
collection. An assembly interrupted before its commit intent retains the frozen
session and parts; a later identical completion restarts assembly. A durable
intent finishes publication on restart. No partial assembly becomes visible.

The opted-in S3 adapter sends an operator-signed HEAD with
`X-Gregale-Multipart-Receipt: <public-uuid>`. Its response must carry the exact
size, ETag, public session marker, terminal status,
`X-Gregale-Multipart-Manifest` hash and selected checksum. Native version,
delete-marker, encryption and Object Lock claims are rejected. Selector headers
must be signed, mutually exclusive and used only on an unconditional object
HEAD. Customer input does not select this private operation through the gateway.

Exact committed proof settles recovery without another native completion POST
or part PUT, even after overwrite/deletion. Exact saved conditional rejection
follows the existing conditional-abort path. A missing receipt permits only
replaying the already authorized immutable completion; missing upload identity,
missing proof, malformed responses and timeouts never establish rejection or
release an uncertain admission. Managed S3/GCS backends retain their existing
behavior when `owned_node_receipts` is absent/false.

## Abort, expiry and limits

An in-process assembly guard excludes abort, expiry and a second assembly.
Once a commit intent exists, it finishes before later mutations. Abort checks
the receipt before durably tombstoning a session and deleting parts; it never
removes a completed object's bytes. Completion precondition rejection is terminal
for that session. Stale part writers recheck the session before publication and
cannot restore an aborted, expired or frozen upload.

Reuse existing `pkg/api/limits.go` ceilings: 10,000 parts, 5 MiB minimum non-final
part, 5 GiB maximum part, 5 TiB maximum object, 100 active uploads per bucket and
24-hour upload lifetime. Node defaults cap parts at 64 MiB; operators can lower
the multipart total or tune part size within these ceilings. Existing sessions
retain their captured total-size cap. Concurrent part writes and assemblies
share write slots and free-space reservations. Assembly reserves its entire
additional output size while original parts still occupy disk.

The daemon sweeps expired sessions every 30 seconds in batches of at most 32
expirations; active assemblies are excluded. Startup also expires abandoned
sessions. Session/receipt metadata is retained; safe metadata pruning still
needs a gateway settlement/retention protocol. Listings scan filesystem metadata
and remain prototype implementations.

Opening disk formats 1 or 2 advances the marker to format 3 before multipart
state is created. Earlier binaries reject format 3. Rollback requires a
consistent pre-upgrade backup of the entire directory, including multipart
sessions and receipts; never manually downgrade the marker.

## Validation and boundaries

Tests exercise SDK upload/resume after restart, actual completed bytes and
metadata, checksum policies, ordered and missing parts, size/ETag mismatch,
replacement capacity, frozen-manifest races with abort/expiry, interrupted
assembly, commit-boundary failures, corrupt parts and process kill after a
durable multipart intent. Integration crosses the stock customer SDK, gateway,
S3 adapter, real disk node, restart and apid worker after a lost completion
response, including later overwrite, deletion, saved rejection and missing proof.

This remains a private single-node prototype. UploadPartCopy/CopyObject, tags,
versioning enablement, SSE, Object Lock, provider CORS, authoritative usage,
replication, backups and periodic integrity scrubbing remain outside this slice.
Private-node AWS streaming-trailer framing remains unsupported; the public
gateway terminates that framing before forwarding bounded parts. Listing parts
does not prove an individual uncertain gateway part writer; existing
conservative writer-admission rules remain in force.
