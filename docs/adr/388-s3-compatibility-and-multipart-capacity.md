# ADR-388: S3 compatibility and multipart capacity admission

Date: 2026-10-01
Status: Accepted

## Context

The branded S3 gateway dropped conditional write headers, rejected the checksum
read mode sent by current SDK defaults, and omitted standard listing semantics.
UploadPart consumed provider storage without reserving capacity. A single size
setting coupled object totals to the edge's request-body ceiling. Disabled
storage and exhausted admission budgets also prevented S3 cleanup.

## Decision

Conditional ordinary PUTs use provider-atomic `If-Match` or `If-None-Match: *`
through signed provider requests. Providers lacking this capability return 501.
Conditional multipart completion and CopyObject return 501 until their conditions
can be persisted and respected during recovery. There is no HEAD/PUT emulation.
Checksum-enabled GET/HEAD requests use a provider capability and preserve real
checksum response headers. Providers without that capability serve a normal
read without inventing a checksum.

ListObjectsV2 supports `start-after`, `encoding-type=url`, `max-keys=0`, ETags,
and request field echoes. Multipart listing uses filtered active sessions ordered
by UTF-8 key and upload ID; S3 key/upload markers replace the unrelated catalog
cursor. Unsupported options fail explicitly.

Keep `max_upload_bytes` as the total object limit. Add `max_single_put_bytes` and
`max_part_bytes` to the provider registry. Omitted single PUT limits retain the
previous behavior; omitted part limits use at most the 64 MiB multipart default.
The proxied example profiles explicitly cap both request sizes at 64 MiB and
allow a 100 MiB object. Management API layouts and signed PUT validation also
honor these limits. Operators must match their edge's own ceilings.

Before forwarding UploadPart, reserve its maximum admitted byte length in a
per-session, per-part ledger. All account capacity decisions share one account
lock. Retrying or shrinking a part does not release a previous grant; larger
parts reserve only the difference. Incomplete parts add to object capacity and
do not consume completed-object key slots. A configured object ceiling bounds
the sum of part grants. Streaming parts share the gateway's upload concurrency
bound.

Completion admission substitutes this session's reserved part bytes when checking
the final object grant, then keeps both grants until provider completion is
confirmed durably. Crashes, uncertain responses, and failed integrity checks
retain reservations. Confirmed completion removes the parts from live capacity.
Abort does not reclaim capacity automatically: S3 can finish in-flight parts
after an abort, and recommends further abort/list verification. Reconciliation
and safe reclamation are follow-up work. Deleted buckets leave live capacity
according to the existing accounting contract.

CORS preflights use the union of explicit backend origins because browser
preflights have no SigV4 identity. This browser policy does not authorize data
access. Actual requests still authenticate and enforce bucket permissions.
Provider CORS allows upload headers for signed metadata/tag/checksum requests.
Object DELETE, DeleteObjects, and AbortMultipartUpload remain authenticated but
bypass new-work budget admission and the global disable flag. Provider request
metrics remain durable and mandatory.

## Rollout and validation

Drain all nonterminal branded multipart sessions before applying the migration.
The migration refuses an unsafe rollout with existing unreserved parts. Keep
provider cleanup credentials available. Inspect historical provider orphans as
part of provider qualification. Rollback refuses to discard unsettled part
reservations; settle capacity or delete the affected disposable bucket first.

Regression tests cover signed provider conditions/checksum reads, current AWS Go
SDK checksum validation, listing encoding/markers/zero limits, CORS, disabled
cleanup, a 65 MiB multipart completion with 64 MiB parts, and quota denial before
provider I/O. The shared memory/PostgreSQL suite exercises concurrent reservations,
retries, tenant isolation, completion conversion, abort retention, and terminal
history exclusion. Provider and public edge qualification remain required before
production activation.

References: [S3 conditional writes](https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html),
[AbortMultipartUpload](https://docs.aws.amazon.com/AmazonS3/latest/API/API_AbortMultipartUpload.html),
[ListObjectsV2](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectsV2.html).
