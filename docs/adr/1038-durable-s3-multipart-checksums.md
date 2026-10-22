# ADR-1038: Durable S3 multipart checksums

Status: Proposed

Date: 2026-10-13

## Context

The gateway validates request and trailer checksums, but previously admitted
`CreateMultipartUpload` checksum selection without preserving that selection.
Part responses, completion and retained-object reads consequently could not
provide an end-to-end multipart integrity contract to standard S3 clients.

An ETag, checksum and write receipt serve different purposes. A checksum does
not prove that a write committed and cannot authorize quota reclamation after
an uncertain response.

## Decision

Capture an immutable algorithm/type policy in the existing owned upload journal.
Support CRC32 and CRC32C full-object or composite checksums, CRC64/NVME
full-object checksums, and SHA1/SHA256 composite checksums. Unsupported policies
fail before native initiation. Other provider adapters must explicitly opt in.

Checksum-enabled parts use the existing aggregate spool reservation, free-space
floor, transfer slots and deadlines. The gateway validates the complete decoded
body, signs its computed checksum into the native upload, and validates the
native acknowledgment before settling the writer. Native parts remain the
bounded, authoritative inventory for completion after restart.

Before completion, validate the customer's selected ETags and checksums against
that inventory. Require consecutive part numbers, bind each selected size and
checksum, and calculate the expected completed-object checksum. CRC full-object
checksums combine part CRCs and sizes without rereading the object; composite
checksums hash the ordered binary part digests and retain the part-count suffix.
Atomically capture this snapshot with the existing completion intent and quota
admission. A checksum or size mismatch cannot consume the upload or reach native
completion.

The native completion acknowledgment must match the snapshot. Existing
receipt/ETag/size/version proof still governs recovery; checksum recovery adds
an exact selected-version HEAD with checksum mode enabled. Missing or malformed
proof keeps the existing reservation and mutation fence. Terminal replay returns
the persisted checksum and public version, without another native write.

Memory and PostgreSQL stores clone and validate the snapshot. Database guards
prevent policy changes, completion-intent changes, and completion by old writers
that do not verify the checksum. Migration rollback refuses to discard any
accepted checksum-enabled upload, including completed uploads.

GET/HEAD validate and expose native checksum metadata. Add bounded
`GetObjectAttributes` with the standard attribute selection and part pagination,
using existing read grants, usage admission and public version references. Native
upload/version identities remain private. The management API's existing public
models do not gain private checksum-journal fields.

## Consequences and acceptance

Trailer-only checksum parts require spool space before native dispatch; clients
must choose a part size within the configured spool budget. No memory, disk,
quota, timeout or cleanup limits are raised.

Acceptance requires direct CRC combination vectors; standard SDK-to-gateway-to-
native HTTP upload, read and attribute tests; malformed-input/acknowledgment and
lost-response cases; PostgreSQL owner reconstruction, old-writer and rollback
guards; migration replay and schema/SQLC parity; and affected-package race tests.
Local fixtures do not establish live provider qualification.

Protocol references: [S3 upload integrity](https://docs.aws.amazon.com/AmazonS3/latest/userguide/checking-object-integrity-upload.html)
and [GetObjectAttributes](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObjectAttributes.html).
