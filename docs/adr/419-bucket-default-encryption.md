# ADR-419: Bucket default encryption

Date: 2026-10-04
Status: Accepted

## Context

Explicit owned encryption is available on S3 writes, signed URLs, control
multipart sessions and application routes. Buckets lack a default policy, native
configuration reconciliation and a shared admission rule. Defaults must reach
every new write without changing accepted receipts or multipart sessions.

## Decision

Persist desired and verified bucket encryption snapshots with a monotonic
revision and durable bounded configuration leases. Reconcile the native S3
bucket configuration with exact readback, preserve unrelated native encryption
blocking settings and recover lost acknowledgments after restart. Native AES256,
owned KMS/DSSE identities and supported bucket-key options share existing
enrollment validation and request accounting. Clearing the owned policy clears
the native override; providers may retain their mandatory baseline encryption.

Apply a verified default inside each new receipt/session admission transaction.
Explicit write or route selections override it. Capture a private default
revision with the immutable snapshot. Existing accepted operations, including
implicit multipart creation replays, reuse their saved identity. New implicit
writes wait during an unresolved configuration change; admitted writes keep
their existing dispatch/recovery contract.

Configuration mutations lock the bucket without excluding foreign-key readers
and never acquire the account lock. Admissions share-lock the policy record
after ordinary account/bucket serialization; this avoids a bucket/account lock
cycle. Database guards reject stale snapshots, plaintext legacy admissions and
provenance changes. Clearing policies preserves revision tombstones. Rollback
requires clearing policies and draining configuration/default-owned work.

Expose management and standard S3 configuration routes, capability discovery,
public Go/Node/Python clients and CLI commands. Bound and verify JSON/XML request
bodies; preserve scope/grant checks and cleanup with ingress disabled. Reject
unsupported provider/cipher features explicitly.

## Acceptance

Local native HTTP/TLS fixtures with memory and PostgreSQL pass configuration,
implicit PUT/copy/multipart, signed URL, application route and control multipart
flows. Tests cover lost configuration acknowledgments, reconstructed stores and
providers, disabled KMS and ingress, native drift, unrelated blocking settings,
captured policy revisions after clear, creation replay and exact write proof.
Public SDK responses preserve KMS bucket-key settings. The encrypted presigner
keeps those settings in signed headers instead of permitting SDK query hoisting.

Strict JSON/XML, tenant/grant checks, legacy-writer/provenance database fences,
account lock ordering and guarded/drained rollback tests pass. Focused state,
provider, gateway and control API races, related regressions, full provider,
gateway and API suites, public Go/Node/Python clients and CLI checks pass.
Schema/SQLC and SDK regeneration parity, OpenAPI/SDK coverage, repository gates
and zero-issue changed-line lint pass. Evidence is recorded in
`bucket-default-encryption-acceptance.json` under the task's local S3 evidence
directory. No live provider or deployment is part of this acceptance.

## References

[PutBucketEncryption](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketEncryption.html),
[GetBucketEncryption](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketEncryption.html),
[DeleteBucketEncryption](https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketEncryption.html).
