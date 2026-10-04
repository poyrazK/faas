# ADR-420: Owned cross-bucket copy sources

Date: 2026-10-04
Status: Accepted

## Context

S3 credentials deliberately address one logical bucket. Ordinary and multipart
copy reject any other source bucket. Supporting customer data movement requires
explicit source authority, unambiguous logical selectors and destination-only
write admission without weakening those credential boundaries.

## Decision

Add bounded copy-source grants to an owned destination credential. Each grant
allows reading one owned source bucket and an optional exact key prefix solely
for CopyObject/UploadPartCopy. Grant creation requires matching destination write
and source read authority. Listing and revocation require destination write
authority; revocation remains possible after source access or the destination
credential is revoked. Grant creation requires ready buckets on the same
immutable placement. Other placements/providers return an explicit unsupported
response until a separately qualified transfer executor exists.

Address cross-bucket sources by their public bucket UUID in the standard copy
source header. This remains unambiguous when different apps have equal bucket
names. UUID selectors take precedence over bucket names. For a bucket whose
name is itself a canonical UUID, select same-bucket copy with its bucket ID;
this prevents a revoked source from silently becoming a different copy. Other
same-bucket names retain their current behavior. A source grant
never permits ordinary reads, listing or mutation using the destination key.
Signed URL credentials cannot acquire source grants. Managed rotation stages
resolve the parent's current grants without independently expanding authority.

Persist a fresh immutable grant identity when its prefix changes or it is
revoked and recreated. PostgreSQL retains an append-only account-owned identity
ledger after grant, credential and bucket removal. New identities are recorded
after the winning upsert so idempotent retries remain valid, while direct SQL
cannot revive prepared work by reusing a historical identity. Account deletion
cascades this private history. Capture source bucket and grant identity in
ordinary copy receipts. Atomically check authority and the measured source identity with
destination quota/default-encryption admission; revalidate prepared work at
dispatch. Revocation prevents subsequent dispatch, while dispatched work keeps
its bounded completion/recovery contract. Native operations select the captured
source version or ETag and never automatically repeat an uncertain copy.
Multipart copy atomically admits copied bytes and claims its single dispatch
with the current source grant. It persists immutable source provenance with the
part transfer and retains the session's destination encryption and cleanup fence.
Source metadata never exports provider identities.

Serialize quota and source-grant creation with the existing account lock, then
lock buckets in UUID order before credential/grant locks. This preserves the
account deletion and quota lock order. Dispatch holds credential/grant read
locks until its durable transition commits. Database guards pin owned
placement, bounded counts, grant epochs and immutable receipt provenance.
Rollback requires removing grants and draining cross-bucket work.

Expose grant management through the control API, Go/Node/Python clients and CLI.
Qualify customer API to AWS SDK to native HTTP/TLS flows using memory/PostgreSQL,
including prefixes, wrong/foreign selectors, source versions, metadata/tags,
destination encryption, quota, revocation races, rotation, restart and lost
responses. No real provider test or deployment is required.

## Acceptance

Implemented through grant management APIs, Go/Node/Python SDKs, CLI discovery,
state admission/dispatch guards, native S3 ordinary/part copy and destination
recovery. Local control API → AWS SDK → TLS gateway → native HTTP fixtures pass
with both memory and PostgreSQL stores. They cover equal names across apps,
source read/destination write management authority, copy-only access, prefix
bounds, selected source versions, metadata/private proof separation, destination
KMS defaults, multipart ranges, lost acknowledgments, reconstruction, revocation
and disabled-ingress cleanup. Other placements fail explicitly before dispatch.

State tests qualify concurrent idempotent updates, bounded counts with structured
limit errors, fresh epochs, prepared/dispatched revocation races, managed
rotation and part replacement. Database tests reject source/receipt mutation and
historical identity reuse, permit already dispatched settlement and account
history cleanup, and require a drained rollback. Selector tests prevent UUID
name collisions and revoked-source reinterpretation.

Focused state/API/gateway race tests, related S3 state/API/migration regressions,
full provider/gateway/API/CLI suites, Go/Node/Python SDK tests, isolated generation
and schema/SQLC parity, OpenAPI route/DTO compliance, SDK coverage and repository
policy gates pass. Changed-line lint reports zero issues. The generated CLI
reference is current; write-receipt flag errors use the JSON-aware constructor.

Qualification uses local HTTP/TLS and PostgreSQL only. Cross-placement/GCS
transfers and automatic completion-proof retention through unversioned
overwrite/deletion remain open. The broader S3 gaps goal remains active.
