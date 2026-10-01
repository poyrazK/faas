# ADR-396: S3 multipart copy and source ETag conditions

Date: 2026-10-02
Status: Accepted

## Context

Gregale supports multipart uploads and durable same-bucket CopyObject, but
rejects UploadPartCopy, copy byte ranges and customer source conditions.
Server-side copy should have the same capacity and transfer fencing as uploaded
parts. Measuring a source and subsequently copying it without an atomic source
identity check could exceed the admitted byte reservation.

## Decision

Add an optional MultipartPartCopier capability and implement it in the S3
adapter. A source HEAD must return a bounded strong ETag and exact size. Range
copies can read sources up to the existing total-object ceiling, while the
copied part obeys the configured part limit and the 5 GiB provider-neutral part
ceiling. Accept only inclusive bytes=first-last ranges inside the source;
portable range sources must exceed 5 MiB. Completion still enforces the minimum
size for every part except the last. Versioned sources remain unsupported until
the versioning implementation owns them.

Require both read and write permission on the credential's same logical bucket.
Resolve its existing active, unexpired upload and use its private provider ID;
neither source bucket names nor customer upload IDs are provider identifiers.
CopyPartResult exposes only ETag and LastModified. Metadata and tags come from
multipart initiation; part-copy directives are rejected. Unknown copy conditions,
range headers outside part copies and encryption directives are never ignored.

Both tracked CopyObject and part copy admit the source probe as a budget-checked
read before issuing HEAD, in addition to destination write admission. Each
successful copy therefore consumes two monthly safety authorizations; a rejected
source predicate consumes only its admitted probe. Destination quota and receipt
creation remain conditional on a successful source predicate. This prevents
probe traffic from bypassing spent budgets or stale usage evidence.

Reserve the measured copied bytes with BeginObjectMultipartPart before dispatch.
Use existing account/upload locks, token fencing, maximum part grants, completion
revision and verified-abort cleanup. Record each provider HEAD/copy request before
sending it. Bound concurrent copies with the existing transfer slots and deadline.
The S3 UploadPartCopy binds CopySourceIfMatch to the measured source ETag, disables
SDK retries and parses the full response. A valid bounded ETag acknowledges the
copy. Known pre-dispatch failure or a definitive service rejection settles the
transfer fence; HTTP 408/5xx, lost/truncated responses, embedded HTTP 200 errors
and invalid success bodies retain the fence and capacity. No automatic part-copy
replay can infer that an existing part came from this attempt.

Support one strong ETag or '*' in each customer copy-source If-Match and
If-None-Match header. Check both against the measured source before admitting
the destination write. Preserve If-None-Match on the atomic provider call and
always bind the measured If-Match. Extend tracked CopyObject with a separate
optional conditional capability so older adapters cannot silently drop customer
conditions. Failed client predicates return 412 before creating a destination
receipt or part grant; a source race returns 412 on a definitively rejected
provider call. Existing CopyObject receipts and recovery remain unchanged.

Date conditions remain explicitly unsupported in this increment. AWS gives a
successful If-Match precedence over a failed If-Unmodified-Since; adding our
internal ETag fence would change a standalone customer time predicate. A HEAD
check alone cannot provide atomic semantics through same-ETag overwrites. Solve
this together with immutable source version identity rather than weakening the
quota fence. The complete remaining scope is tracked in
[S3 implementation gaps](../s3-implementation-gaps.md).

## Rollout and rollback

No schema or quota shape changes. Upgrade the gateway and S3 adapter together.
Other providers return NotImplemented for part copy or conditional copy until
they implement the optional capability. Existing part writers, completion and
cleanup workers understand the same transfer rows. Rolling back removes the
new wire surface while preserving admitted grants and conservative recovery.

## Validation

Local protocol tests must cover inclusive range boundaries, large source/small
part admission, ETag conditions, source mutation, permissions and bucket/upload
isolation. Exercise actual AWS SDK requests through the branded gateway and S3
adapter against a local HTTP provider, with memory and PostgreSQL stores.
Verify list/complete/abort behavior, quota refusal, uncertain acknowledgments,
transfer fencing, and capacity release only after verified abort. Provider tests
cover one dispatch, private source encoding, 4xx/408/5xx, embedded errors,
malformed acknowledgments and no provider error leakage. Real provider tests are
not required by this implementation scope.

Protocol references: [AWS UploadPartCopy](https://docs.aws.amazon.com/AmazonS3/latest/API/API_UploadPartCopy.html),
[AWS CopyObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_CopyObject.html).
