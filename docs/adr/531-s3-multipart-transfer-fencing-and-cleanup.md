# ADR-531: S3 multipart transfer fencing and cleanup

Date: 2026-10-01
Status: Accepted

## Context

ADR-530 reserves provider part storage before a branded UploadPart. Aborted
reservations remain charged because a provider abort can race with an arriving
part. A completion can also race with a replacement part while its size and ETag
are being inspected. Reclaiming capacity without fencing these writes could
admit more provider storage than the account limit.

## Decision

Begin each branded part transfer atomically with capacity admission under the
account lock and upload row lock. Store a transfer token and unsafe-until
deadline on its part grant. Reject simultaneous transfers of the same part;
different parts remain concurrent. A successful, fully validated provider
response settles that exact token. Failed, interrupted, or uncertain writes
retain their fence. An old token cannot settle a replacement transfer.

Bound the gateway transfer context to 30 minutes, including signing and sending,
even when an injected HTTP client has no timeout. Internal part URLs last 60
seconds. Unsettled transfers block finalization for 36 minutes from admission:
the transfer timeout, URL lifetime, and a five-minute cleanup grace. These
durations live in `pkg/api/limits.go`. Deadline expiry permits reconciliation;
it does not release a byte of capacity by itself.

Abort claims the upload first, preventing all subsequent part admissions.
It asks the provider to abort immediately. Once no unsettled transfer remains
inside its safety window, require ListParts to report an empty, untruncated
listing or NoSuchUpload. Remaining parts trigger a further abort. Provider
errors, missing permissions, and uncertain responses preserve reservations.
Only verified cleanup atomically commits `aborted` and removes tracked part
grants, under the same account lock used by admission. Stale operation tokens
cannot commit or release capacity.

An accepted S3 abort may return 204 while the upload remains `aborting` and its
capacity remains charged. The existing apid recovery worker retries the durable
intent, including while storage is disabled or budgets are exhausted. Successful
requests with cleanup still pending use a 30-second cooldown. Worker failures
use the existing persisted retry policy. No additional daemon or scheduler is
introduced.

Increment an upload's part revision on every admitted part attempt. Completion
validates provider sizes and ETags, then atomically verifies the revision and
absence of unsettled transfers, reserves final-object capacity, claims the
completion intent, and persists its size and exact part list. A failed claim
does not leave an object grant. Recovery always sees a complete intent after a
crash. A concurrent replacement requires the client to retry validation.

## Rollout and limits

Stop branded multipart writes and drain nonterminal branded uploads before the
transfer-fencing migration. It refuses an unsafe upgrade. Deploy every gateway
and API replica before reopening writes; mixed old/new writers are unsupported.
The migration marks existing grants as untracked. They remain conservative,
including when their part is later retried: a newer transfer cannot retroactively
prove that an older writer stopped. Automatic reclamation applies to newly
tracked branded grants. Historical grants and management API object grants still
require the existing operator workflow. There is no general capacity rebase.

Rollback refuses to discard any unsettled transfer token, including an expired
one. Finish verified cleanup or settle the corresponding transfer first. Keep
the provider's abort and ListParts permissions available during disablement.
Provider lifecycle cleanup is still recommended for uploads without a durable
Gregale identity. Real provider and public-edge qualification remains required;
an arbitrary compatible endpoint must demonstrate the same cleanup semantics.

## Validation

The 2026-10-04 adapter hardening requires an explicit `IsTruncated` value on
native part/upload listings, valid ordered parts and bounded progressing
continuations. An incomplete empty response cannot prove cleanup. Native
initiation uses one SDK attempt for every encryption profile; recovery validates
the full bounded discovery before adopting an identity or creating a new upload.
PostgreSQL lifecycle recovery tests retain quota through an incomplete listing
after an acknowledged abort and reclaim it only after verified cleanup.

Memory and PostgreSQL tests cover overlap, token fencing, tenant isolation,
concurrent capacity admission, completion revision changes, atomic preparation,
abort retention/reuse, and conservative legacy grants. PostgreSQL checks cover
lost processes and expired transfer windows without early capacity release.
Gateway tests exercise a live part racing with abort/completion, uncertain
responses, late provider parts, verification errors, and disabled cleanup.
Migration tests cover unsafe upgrades, paired transfer fields, finite deadlines,
legacy defaults, and rollback guards.

Reference: [AWS AbortMultipartUpload](https://docs.aws.amazon.com/AmazonS3/latest/API/API_AbortMultipartUpload.html)
requires checking ListParts and notes that in-flight parts can require repeated
aborts before storage is freed.
