# ADR-606: Durable Object Lock policy for new object versions

Status: Accepted
Date: 2026-10-05

## Context

ADR-584 manages fixed retention and legal holds on existing owned versions.
Creation paths still need an immutable protection policy: inferring protection
from a successful PUT or re-reading mutable defaults during recovery can settle
an incorrectly protected version. ADR-564 already drains accepted writes and
multipart sessions before dispatching a native bucket default change.

## Decision

Capture the verified Object Lock revision, its fixed default retention, admission
time and any explicit fixed retention/legal hold in the existing write receipt or
multipart journal. Capture commits with quota admission under the same account
and bucket locks. Do not change the snapshot after admission. Retention dates
round upward to milliseconds, as in ADR-584. Reject event-hold defaults, event
headers, past explicit deadlines and unsupported or unknown policies.

A missing per-write retention inherits the captured native bucket default. Gregale
cannot dispatch a default replacement until the accepted writes and multipart
sessions drain, including unknown outcomes. S3 applies that default at object
creation. Readback must show the captured mode and at least the admission time
plus the captured period; this is a conservative minimum, not a replacement for
S3's creation-time default. Explicit retention overrides the bucket default and
is applied at PUT, CopyObject or CreateMultipartUpload. Multipart completion
keeps the initiation policy. An independent explicit legal hold is applied at
creation. Copies use destination policy and never copy private source proofs.

The native S3 adapter explicitly opts into consuming snapshots on all tracked
write and proof paths. It stores a private SHA-256 policy proof alongside the
private receipt/session metadata. Public metadata cannot supply or expose these
proofs. Protected brokered PUTs sign a computed Content-MD5 and every protection
header. Protected multipart parts also stage under the existing aggregate disk
and free-space budgets, verify client integrity and sign Content-MD5 before the
native attempt. They do not change the initiation policy. Protected streaming
route uploads use native SDK checksum trailers over TLS. No body is re-uploaded
during receipt recovery.

A mutation acknowledgment alone cannot settle a protected write. Read the exact
non-null native version and require the receipt/session, size, ETag, private
policy proof and fixed retention/legal-hold headers. Missing, duplicate, unknown
or mismatching observations retain custody and the quota reservation. Current
and bounded retained-history recovery use the original snapshot, including after
backend enrollment or ingress is disabled. Never substitute a changing current
selector for exact acknowledgment readback.

Memory and PostgreSQL enforce snapshot identity and verified settlement. SQL
constraints/triggers reject legacy untracked writes, snapshot replacement,
unaware dispatch or multipart claim, settlement without a recorded proof, and
rollback while protection history exists. Branded URL authority also binds the
explicit protection selection to its durable receipt.
The protected URL constraint uses a separate validator so replaying historical
S3 migrations cannot replace the new protection checks.

Expose standard signed Object Lock headers for S3 PUT, copy and multipart
initiation, and an optional `protection` selection on owned signed-upload and
multipart-creation APIs and Go/Node/Python clients. Reads, parts and completion
cannot introduce a new protection selection. Application upload routes inherit
bucket defaults through the same captured journal.

## Limits and consequences

The private snapshot is bounded to 16 KiB in `pkg/api/limits.go`. Existing upload,
lease, retry, history-page and provider-response budgets apply. Extra exact
version reads are metered as native requests. Protection does not itself change
object capacity.

Out-of-band provider configuration and Object Lock changes remain outside
Gregale's serialization boundary. Stronger or malformed observations do not
silently rewrite the accepted snapshot. Direct provider URLs and GCS protection
are unsupported. Cleartext streaming protected uploads fail before the native
attempt; the branded spool broker still supports local HTTP protocol fixtures.

New Object Lock enrollment remains disabled until protected lifecycle deletion is
implemented and locally qualified. This increment does not add event-hold
mutation, governance bypass, or provider qualification against real services.

Native protocol references: [PutObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html),
[CreateMultipartUpload](https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateMultipartUpload.html),
[UploadPart](https://docs.aws.amazon.com/AmazonS3/latest/API/API_UploadPart.html),
and [CopyObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_CopyObject.html).

## Validation

Memory/PostgreSQL journal tests cover immutable capture, queued default changes,
legacy guards, reconstruction, exact public/native version binding and verified
multipart settlement. Local native S3 tests cover exact-version readback,
malformed/missing protection, copy and multipart dispatch, and retained-history
recovery. SDK-to-gateway tests cover explicit protection, inherited defaults,
copy, multipart completion and lost-acknowledgment recovery with enrollment
subsequently disabled. No real provider is required for this contract.
