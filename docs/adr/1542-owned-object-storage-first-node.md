# ADR-1542: First Gregale-owned object storage node

- Status: Accepted for a local prototype; no production enrollment.
- Date: 2026-10-15
- Related: [ADR-151](151-provider-neutral-object-storage.md), [ADR-530](530-s003-compatibility-and-multipart-capacity.md)

## Context

The public S3 gateway already owns tenant authentication, logical buckets,
accounting admission and immutable provider placement. The platform currently
delegates object bytes to managed providers. We want to begin implementing our
own storage engine while retaining that customer-facing contract.

## Decision

Add `pkg/objectnode`, a single-process local storage engine, and
`cmd/object-storaged`, a private loopback S3 node. A separate disk handler in
`pkg/s3gateway` reuses existing SigV4 verification and XML responses. The current
generic S3 provider connects the branded gateway to the node; no new provider
driver, customer API, schema or public listener is introduced.

The node's static operator credential grants access to the entire node. It is
never issued to tenants. Customer credentials remain owned by the existing
gateway. The daemon accepts only a loopback listen address. Its endpoint may be
configured only for components on that same host; this prototype does not
establish cross-host storage-node transport or authentication.

Each bucket has a random incarnation. Bucket/key hashes select internal
directories; literal object keys never become paths. Objects are immutable
UUID-named blobs. A bounded JSON catalog record selects the current blob.
Publication fsyncs the blob, atomically renames it, fsyncs the object directory,
then atomically publishes and fsyncs the catalog record before acknowledgment.
Conditional overwrites are checked under the catalog lock at publication.
Staged uploads capture bucket incarnation so deleting/recreating a bucket cannot
adopt an old upload. Deletion durably removes catalog visibility before physical
reclamation. Already-open readers retain their immutable file descriptor.

An exclusive process lock protects the entire data directory. Startup validates
the catalog and referenced blob sizes, fails closed on corruption, and reclaims
unfinished uploads and unreferenced blobs. This recovery runs before serving.
Directory enumeration and listing candidate memory are bounded. Listing still
scans a bucket and holds a catalog read lock; it is not an indexed scalable
metadata service. SHA256 is retained for later integrity scrubbing; restart does
not rehash every blob or claim to detect same-size disk corruption.

## Supported first slice

Path-style SigV4 header/query authentication; empty-body CreateBucket;
HeadBucket/DeleteBucket; unversioned single PUT/GET/HEAD/DELETE; basic conditional
PUT; HTTP conditional and range reads; standard HTTP/user metadata; prefix,
slash-delimiter and paginated ListObjectsV2; disabled bucket-versioning readback;
and signed download response overrides. Uploads verify signed SHA256,
Content-MD5 and supported explicit S3 checksum headers before publication.

Concurrent uploads, aggregate reservations and a free-space floor bound disk
use. Unknown-length nonempty bodies and AWS streaming-payload framing are
unsupported at the private node. The branded gateway handles customer SDK
framing before forwarding a bounded request. Unsupported operations return
NotImplemented, including multipart, copies, CORS configuration, tags, version
selection/enablement, Object Lock and server-side encryption.

## Consequences and next milestones

This starts the owned byte-storage implementation without replacing our
existing S3 service. It is a local development prototype with one failure domain,
an unindexed filesystem catalog and no backup, usage-export or HA qualification.
Do not enroll customer workloads or repoint existing placements. The existing
accounting, provider qualification and rollout gates remain in force.

Next work: durable multipart/receipt recovery, authoritative inventory and usage
exports, an indexed catalog, bounded background integrity checking, disk-failure
recovery and backups. Distributed operation additionally needs explicit logical
shards, placement epochs, replication, fencing, repair and safe rebalancing. No
node-count or durability guarantee is implied by this prototype.

[ADR-1543](1543-owned-object-storage-write-receipts.md) adds durable write
receipts and the explicit private recovery protocol. Multipart remains deferred.

[ADR-1544](1544-owned-object-storage-multipart.md) subsequently adds durable
multipart uploads, completion recovery and safe expiry/abort.

## Validation

Portable tests exercise stock AWS Go SDK requests, presigned PUTs, metadata,
Unicode/escaped keys, ranges, listing boundaries, checksum/condition rejection,
unsupported features, and default SDK framing through the branded gateway and
the real S3 adapter. Storage tests cover concurrent conditional creation,
bucket-incarnation fencing, lock exclusion, restart/deletion persistence,
corrupt-catalog preservation and killing a subprocess during upload. These are
local correctness checks, not production or distributed failure qualification.
