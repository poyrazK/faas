# ADR-20261121150000: Unversioned S3 backends read the null version

Date: 2026-11-21
Status: Accepted
Related: ADR-20261025115958 (deletions wait only for work on their own key),
ADR-20261029174627 (one provider project per backend)

## Context

The first production smoke of customer object storage on OVH
(`ovh-bhs-prod-v1`, 2026-11-21) created a bucket, uploaded one object and
then could not delete it: every delete answered `OperationAborted`.

OVH returns a version ID for every object it stores, even in a bucket whose
versioning was never enabled. `PutObject` answered
`x-amz-version-id: 1795265333.741474`, and `ListObjectVersions` listed the
object under the same ID, while `GetBucketVersioning` reported no status.
AWS S3, R2 and MinIO answer an unversioned bucket with no header and list
the object as the `"null"` version.

The adapter reads any ID other than empty or `"null"` as proof that the
provider keeps history: it sets `recovery_versions_observed` and the
version references' `versions_observed`. A deletion on a bucket with
observed versions but no versioning status is refused, because Gregale
cannot prove what the delete would leave behind
(`pgstore_object_deletions.go`). Every OVH bucket would therefore become
undeletable after its first write.

Gregale cannot version buckets on OVH either: the provider has no
`*Version*` object actions, so the gateway policy
(`deploy/ovh/customer-object-storage-policies/`) cannot address one version.

## Decision

A backend may declare `"versioning": "unsupported"` in the object-storage
registry (`BackendConfig.Versioning`). It is valid only for an S3 backend
without Object Lock. For such a backend the S3 adapter:

1. drops `X-Amz-Version-Id` and `X-Amz-Copy-Source-Version-Id` from every
   provider response, in the response transport, before the SDK or any
   caller reads them;
2. reads every listed object version as the `"null"` version, and resumes a
   listing by key marker alone (one version per key);
3. keeps the IDs of listed delete markers, which an unversioned bucket never
   has, so they still trip the versions-observed latch;
4. refuses `PutBucketVersioning` with `ErrUnsupported`.

`GetBucketVersioning` still reports the provider's real status. A deletion
compares it with the recorded status before it dispatches, so versioning
enabled outside Gregale (with the bucket-admin key) still fails closed.

`ovh-bhs-prod-v1` sets `"versioning": "unsupported"`. The backend
fingerprint does not include the field, so changing it does not fence
existing buckets.

## Consequences

- OVH buckets behave like unversioned AWS buckets: overwrites and deletes
  work, and customers see no version IDs.
- Object versioning, version listings by ID and Object Lock stay unavailable
  on that backend. Their capability flags stay unreleased for customers.
- Rows already marked `versions_observed` by the old reading stay marked.
  The one such production bucket (the smoke bucket) is cleaned up by hand.
- Versioning enabled at OVH outside Gregale would keep noncurrent versions
  of overwritten objects that Gregale neither lists nor bills. The bucket-admin
  key never leaves the control plane, and deletions refuse the bucket.

## Alternatives considered

- **Trust OVH's IDs.** Every bucket becomes undeletable after its first
  write.
- **Look up versioning status on every write.** One more provider request
  per PUT, for an answer Gregale already controls.
- **Normalize IDs at each call site.** About fifteen read sites and their
  response validators would each need the rule; one transport and three
  listings keep it in one place.

## Amendment 2026-11-21: the S3 gateway's own dispatches

The transport covers every request the SDK sends, but the S3 gateway sends
presigned requests with its own HTTP client (tracked and legacy PUTs, reads,
multipart part uploads, mutations and branch overlays) and reads the
provider's response headers directly. On OVH after the rc.364 rollout, a
gateway PUT still recorded the provider's `X-Amz-Version-Id`, latched
`recovery_versions_observed`, returned the ID to the client, and the next
delete was refused with `OperationAborted`.

`objectstorage.NormalizeProviderResponse` applies the transport's rule to
those responses: a provider that reports `Versionless()` (an `S3` provider
on a `"versioning": "unsupported"` backend) has its version-ID headers
dropped right after each gateway `Do`. `VerifyProviderObjectWriteAcknowledgment`
also drops them for a versionless provider, so a new dispatch path that
forgets the call still records no version. The rule stays in one helper; the
gateway's five dispatch sites each call it once.

## Amendment 2026-11-21: OVH's final ListParts page

A live run of every launch capability against OVH also found that OVH ends
every `ListParts` page with `NextPartNumberMarker` set to the last part it
returned, even when `IsTruncated` is false. That is the marker's documented
meaning on AWS. Gregale treated any non-zero marker on a final page as
contradictory provider evidence, so gateway `ListParts` and every gateway
`CompleteMultipartUpload` (which lists parts first) answered 503.

A final page may now name its own last part (or, when empty, the request's
marker). A marker past the last returned part still claims parts the page did
not return and is still refused, so the provider-evidence rule of #6670 keeps
its intent. The other S3 listings Gregale reads end without next markers on
OVH (captured the same day) and are unchanged.

## Amendment 2026-11-22: copies without copy-source conditions

`TestObjectStorage` against production after the rc.367 rollout found that
every gateway `CopyObject` on OVH answered 409 `OperationAborted` and left its
completion `provider_write_uncertain`. The provider copy sends
`x-amz-copy-source-if-match` with the snapshot ETag as the fence that the copy
reads the bytes the gateway snapshotted. OVH answers that header with
501 NotImplemented ("Copy object not implemented with
X-Amz-Copy-Source-If-Match"), which Gregale correctly reads as an uncertain
write. The same copy without the header succeeds. `UploadPartCopy` sends the
same conditions.

On a `versioning: unsupported` backend, copies and part copies no longer send
copy-source conditions. The coupling is deliberate: OVH is the only such
backend today, an unversioned bucket has no version fence to fall back on,
and a separate registry field would make running binaries reject the strictly
parsed registry until the new release is out. A backend that needs the
behaviour without being unversioned gets its own field then.

- Client copy conditions (`x-amz-copy-source-if-*`) are still evaluated
  against the gateway's own HEAD snapshot before dispatch, as before.
- The fence moves after the copy. A single-part, unencrypted source's ETag is
  its content MD5, and OVH returns it unchanged on a copy of the same bytes
  (verified on production). A copy that returns any other ETag wrote bytes
  other than the snapshot, so it is treated as uncertain and recovery settles
  the destination from its receipt.
- A multipart source gets a whole-content ETag on copy (verified on
  production) and cannot be fenced that way. A ranged part copy cannot be
  either. If the source is overwritten in the window between the snapshot
  HEAD and the copy, the copy holds the newer bytes, which is S3's own
  behaviour for a copy without conditions. Capacity reconciliation corrects
  the recorded size.
