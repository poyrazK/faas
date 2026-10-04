# ADR-532: Conditional S3 multipart completion

Date: 2026-10-01
Status: Accepted

## Context

ADR-530 supports conditional ordinary PUTs and explicitly rejects conditional
multipart completion. Large-object clients need create-only writes and safe
replacement using an ETag. Checking HEAD before completion cannot enforce those
conditions atomically. Recovery must preserve the same condition even after a
gateway disconnect or process restart.

## Decision

Support `If-None-Match: *` and `If-Match` on branded CompleteMultipartUpload
through an optional provider capability. The S3 adapter sends the condition on
the provider completion request. Providers without that capability, including
GCS, continue to return 501. Conditions are mutually exclusive; If-Match is
bounded to 256 bytes and rejects control characters.

Persist the condition together with the exact part list, final size, operation
lease, and capacity admission. Use a distinct `completing_conditional` state.
Older recovery workers do not select this state and cannot replay its intent
unconditionally. The live-key index includes the state to preserve exclusion.
PostgreSQL checks and a trigger prevent changing or removing the condition once
completion starts. Client retries must match both the saved parts and condition.

Recover a pending completion using its persisted request without listing parts
again or admitting another capacity grant. A provider may already have consumed
the upload after a successful completion whose response was lost. On
NoSuchUpload, PreconditionFailed, ConditionalRequestConflict, or a missing
destination, HEAD can prove success only when reserved Gregale session metadata
and the exact size match. A different object does not prove success. If that
check is unavailable, retain the intent and reservations for recovery.

Disable SDK completion retries for conditional requests; the durable operation
owner controls replay. Definitive precondition rejection returns 412, a
conditional conflict returns 409, and a missing If-Match destination returns
404. These failures atomically move the upload to `aborting` and persist a
bounded terminal outcome. They never retry completion after rejection. Clients
must create a new upload after conflict and upload its parts again.

The existing recovery worker aborts and verifies cleanup before releasing
tracked part grants under ADR-568. Cleanup works with storage disabled or
budgets exhausted. Preserve the terminal outcome through cleanup so identical
completion retries receive the same error. Final-object grants remain
conservative; this change does not introduce general quota rebasing.

## Rollout and limits

Apply the additive migration before deploying new API and gateway replicas.
Upgrade every API replica before deploying conditional completion at the edge.
Older gateways may reject conditional requests during the rollout; older API
workers leave conditional completion to upgraded workers. A rollback of the
migration refuses to discard any conditional intent until completion or verified
abort is terminal. Keep provider completion, HEAD, abort, and ListParts
permissions available. Public-edge and real-provider qualification is still
required, especially for compatible S3 implementations.

## Validation

AWS SDK protocol tests cover both condition headers, 412/409/404 classification,
HTTP 200 embedded errors, one completion attempt per provider call, and recovery
with exact session/size proof. Branded endpoint tests use the current AWS Go SDK
and cover replay, condition removal, terminal error replay after cleanup, and
recovery without provider parts. Memory/PostgreSQL tests cover immutable intent,
old-worker exclusion, stale tokens, and quota retention/release. Recovery worker
tests exercise success, transient failures, all conditional rejections, and
verified cleanup while disabled. Migration tests exercise checks, immutability,
and guarded rollback.

References: [AWS conditional writes](https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html)
and [CompleteMultipartUpload](https://docs.aws.amazon.com/AmazonS3/latest/API/API_CompleteMultipartUpload.html).
