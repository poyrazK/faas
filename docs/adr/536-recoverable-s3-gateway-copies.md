# ADR-536: Recoverable S3 gateway copies

Date: 2026-10-01
Status: Accepted

## Context

ADR-535 makes branded PUTs recoverable, but CopyObject still creates a
conservative key grant. A copy blocks capacity reconciliation even after its
destination is deleted. The gateway also measures a source before an
unconditional copy; a source overwrite can invalidate that size reservation.
Native metadata COPY can propagate an earlier upload's private receipt.

## Decision

Add an optional TrackedObjectCopier capability for branded, same-bucket S3
copies. Capture source size, a bounded strong ETag, customer HTTP metadata and
Expires through HEAD before admission. Require Content-Length even for empty
objects and reject sources larger than the existing 5 GiB single-write limit.
Version IDs other than empty or null are unsupported. The provider copy binds
the captured ETag through CopySourceIfMatch; a source change returns 412 and
requires a new client request. This protects the measured byte reservation.

Persist destination key, source key/ETag, account, app, bucket, credential
subject and size atomically with the receipt-owned proxy journal, key grant
and monthly authorization. Add a checked gateway_copy receipt origin and
source-shape constraint. Each S3 request remains a distinct attempt. Reuse the
existing prepared/dispatched/settled state machine, single dispatch, preparation
expiry, lease fencing and recovery worker. The ordinary settlement API cannot
close receipt-owned copy journals. Never upgrade historical conservative grants.

For customer metadata COPY, send the captured customer metadata with provider
REPLACE so a fresh private receipt can be added atomically. Preserve captured
Content-Type, Cache-Control, Content-Disposition, Content-Encoding,
Content-Language and Expires. Strip earlier upload, multipart-session and
provider-private tag markers; validate customer metadata before inserting the
new marker. Customer REPLACE uses the supplied metadata. Tagging COPY/REPLACE
retains native provider behavior independently of metadata. Metadata COPY
therefore uses the captured HEAD snapshot; metadata-only changes that preserve
the source ETag are not a new snapshot. Website redirects, ACLs, storage-class
overrides and encryption directives remain unsupported in the branded protocol.

The AWS SDK copy receives RetryMaxAttempts=1. Read and parse the full copy
response; a valid bounded destination ETag is required for acknowledgment.
Embedded errors inside HTTP 200, 408, 5xx, transport failures, truncated or
invalid acknowledgments remain pending and return ServiceUnavailable. Only a
definitive service 4xx other than 408 or a known pre-dispatch failure settles a
failed receipt. Settlement uses the existing detached five-second deadline.
Admitted copies expose X-Gregale-Upload-ID through the existing CORS policy.
No provider URL, marker, response body or error detail crosses the gateway.

Recovery probes the destination's exact receipt marker, size and ETag. It never
copies again or reads the source to infer completion. Source deletion after a
successful copy does not prevent recovery. Missing or overwritten destination
proof stays pending. Reuse existing upload limits and add the bounded
gateway_copy recovery metric operation. Record each source HEAD and copy
attempt before calling the provider. Recovery and deletion remain available
with signing disabled or spent budgets; capacity reconciliation does not reset
monthly authorizations or billing.

Providers without this capability retain their conservative copy behavior.
Direct signed uploads, environment-clone/cross-bucket copies, historical grants,
versioned buckets and independent provider writers remain outside this scope.
Client copy-condition headers and UploadPartCopy remain unsupported; the
source condition here is an internal quota fence.

## Rollout and rollback

Apply the additive migration before upgrading gateways and API workers.
ADR-534/535 workers can confirm destination receipts without understanding the
new source fields or metric origin. Existing capacity workers see the same
proxy journals. Older copy writers continue creating conservative grants.
Stop new tracked copies before rollback and settle all copy receipts and
journals. Rollback refuses unresolved work, makes settled copy grants
conservative, and removes only copy receipts/journals. PUT and application
route receipts survive. Down/up cannot invent a refund.

## Validation

Provider HTTP tests cover source conditions, private-marker replacement,
captured/replaced metadata, tag directives, source proof, bounded size/ETag,
4xx/408/5xx, embedded errors, truncated responses and a single copy attempt.
Gateway tests cover quota rejection, independent concurrent copies, failures
before dispatch and lost database acknowledgments. Memory/PostgreSQL state
tests exercise atomic admission, immutable source identity, single dispatch,
settlement isolation and capacity reuse. A real AWS SDK, branded gateway,
provider HTTP boundary and PostgreSQL test commits a destination then drops
its response; a new worker confirms it, deletion plus inventory reclaims
capacity without changing monthly ledgers. A source-change case rejects the
copy. Migration tests cover receipt shape, guarded rollback and preservation
of PUT/application records.

Provider protocol references: [AWS CopyObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_CopyObject.html),
[AWS HeadObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_HeadObject.html).
