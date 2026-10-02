# ADR-399: Immutable S3 copy sources and date conditions

Date: 2026-10-02
Status: Accepted

## Context

ADRs 394 and 396 reject source objects with native version IDs. ADR-398 now
accounts for retained versions, but those buckets still cannot use either
CopyObject or UploadPartCopy when their source HEAD returns a version. An ETag
can also survive a metadata change or overwrite; it does not identify an
immutable source for a last-modified predicate.

The [S3 CopyObject contract](https://docs.aws.amazon.com/AmazonS3/latest/API/API_CopyObject.html)
and [UploadPartCopy contract](https://docs.aws.amazon.com/AmazonS3/latest/API/API_UploadPartCopy.html)
accept a version in the copy source. A successful customer If-Match takes
precedence over a failed If-Unmodified-Since. Appending Gregale's internal
If-Match to a customer's standalone date predicate would change its meaning.

## Decision

Capture the native version ID from the measured source HEAD. Validate its
existing bounded private identity format. Bind both copy operations to that
exact version with an encoded versionId on the private provider CopySource
header. Encode the key and version separately so literal key question marks,
Unicode, plus signs, slashes, percent signs and query delimiters cannot change
the source identity. Never expose native source IDs in JSON, copy results or
errors. A null version remains mutable and uses the existing ETag fence.

Native-source gateway copies require a verified all_versions baseline before
destination admission. The existing capacity job fence and per-attempt native
reservation remain authoritative. A bucket that has only current-object
accounting declines the copy without reserving its destination or dispatching
it. This increment uses ADR-397's existing native observations to establish the
baseline; automatic activation from a read observation remains part of public
versioning. It does not enable provider versioning or accept customer versionId
parameters. Copies remain within the credential's logical bucket.

Accept signed copy-source If-Modified-Since and If-Unmodified-Since headers.
Reject empty, duplicate, malformed and over-128-byte dates before probing the
source. Parse HTTP dates and send the same instants in the provider SDK input.
Add explicit optional date-copy capabilities for both ordinary and part copies
so older adapters cannot silently discard new predicates.

The provider evaluates date predicates atomically on the selected immutable
version. When the customer did not supply If-Match, omit the internal ETag
predicate for a native date copy; the immutable version itself provides the
source identity fence. Otherwise retain the measured ETag: it has already
passed the customer's ETag check and preserves the documented precedence.
Forward the remaining customer predicates for the provider to evaluate using
its own date precision and combination rules. Gregale does not attempt to
reproduce those rules with a HEAD-time date calculation.

Independently restrictive date predicates on absent/null native versions remain
NotImplemented. A customer If-Match combined only with If-Unmodified-Since can
use a mutable source, because the customer explicitly selected ETag precedence.
Automatic retention of immutable source/proof versions on unversioned backends
is required to remove the remaining date limitation.

Provider predicate rejection occurs after destination admission. It returns
412, settles the existing write/transfer fence, and retains conservative grants
until verified reconciliation or multipart abort cleanup. It spends the
destination authorization and provider attempt. ETag mismatch detected during
the source probe still declines destination admission. A removed native source
maps NoSuchVersion to the safe NoSuchKey boundary. A delete marker is not a
copyable source. Lost/truncated acknowledgments and 408/5xx retain existing
pending receipts/part fences; copies are never automatically replayed.

## Rollout and rollback

No schema change. Deploy the gateway and S3 adapter together after ADR-398's
migration. Existing all-version inventory, quota and recovery workers continue
to understand the same rows. Rolling back declines these new copy requests;
persisted grants and uncertain write recovery remain intact. GCS and older
provider adapters decline dates until they implement the optional contract.

## Validation

Local AWS SDK → SigV4 gateway → real S3 adapter → HTTP fixture tests use both
memory and PostgreSQL stores. Cover current-accounting refusal, native source
overwrite with the same ETag, full-version overwrite quotas, date success and
provider rejection, the two documented ETag/date precedence cases, both date
bounds, signed/malformed/duplicate date headers, source-version deletion,
private response identities, and lost acknowledgments across PostgreSQL store
restart. Provider tests cover private key/version encoding, null versions,
markers, unsupported mutable dates, one request and normalized failures.
Race tests exercise the complete objectstorage and s3gateway suites. No real
provider test is required or run. Public version APIs, activation, deletion,
restore and the other gaps remain in the [implementation ledger](../s3-implementation-gaps.md).
