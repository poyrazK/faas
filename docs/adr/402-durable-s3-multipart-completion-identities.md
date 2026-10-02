# ADR-402: Durable S3 multipart completion identities

Date: 2026-10-02
Status: Accepted

## Context

Multipart completion currently returns only an error from the provider adapter.
The gateway reconstructs an ETag from part checksums and cannot expose the
created version. Its recovery checks the current object, so an overwrite can
hide proof of an earlier completion. A conditional retry could then classify a
committed upload as rejected.

The [CompleteMultipartUpload contract](https://docs.aws.amazon.com/AmazonS3/latest/API/API_CompleteMultipartUpload.html)
returns the resulting ETag and version header. Its NoSuchUpload error can refer
to an invalid, aborted or completed upload; absence alone does not distinguish
these outcomes. A successful HTTP status can also contain an embedded error.

## Decision

Add an optional result-bearing completion capability, implemented by the S3 and
GCS adapters. Return the actual ETag, private native version ID when available,
and private bounded recovery continuation/observation fields. The S3 SDK performs
one completion attempt, including conditional completions, and handles embedded
errors. Missing, malformed or ambiguous result/proof headers remain uncertain.
GCS validates the complete-result XML and its ETag; it exposes no native S3 ID.

Persist a sticky dispatch flag before any completion request. Require an owned,
ready bucket and the current completion lease, key, size, provider upload ID,
part revision, part list and conditions. The result-aware customer handlers and
recovery worker require a result-aware store before dispatching. Both memory
and PostgreSQL implement this contract.

Store the final ETag and public version ID atomically with terminal completion.
Create/reuse ADR-400's owned bucket/key/native reference in the same transaction
or memory lock. Store only the public ID on the multipart session. Terminal
results and dispatch state cannot be overwritten or forgotten. A failed
settlement retains the pending intent for receipt recovery.

Replay the stored result on successful completion retries without calling the
provider. A different part list or condition is rejected. The control-plane
complete/get/list DTOs and typed client include `etag` and `version_id` when
available. Native identities and recovery cursors remain private.

## Recovery and accounting

Keep the original provider upload ID, part list and predicates on every retry.
First check the current object's exact session receipt, declared size and actual
ETag. For uncertain conditional retries, a positive ListParts result for that
same upload can establish a rejection; a missing upload cannot. An initial
single-attempt conditional rejection can be settled after excluding a current
receipt match. NoSuchUpload remains uncertain even on the first observed reply.

If current proof is hidden, scan one bounded retained-version page per attempt
using ADR-397's continuation. Bind cursors to bucket, key, session receipt, size
and the multipart receipt kind. Probe only matching immutable versions of that
key, with the multipart metadata key and exact native identity. Multipart proofs
support the full multipart size limit rather than the single-PUT limit. An
exhausted or empty history scan never proves failure or refunds reservations.

Meter each S3 completion, current HEAD, upload-presence check, version listing
and historical HEAD before its one provider attempt. GCS ObjectState probes
disable internal SDK retries so callers can meter and schedule subsequent
attempts. Historical GCS generation recovery remains outside this increment.

Persist native-history observations alongside retries and rejection, including
current delete-marker responses and uncertain acknowledgments. These observations
activate the existing all-version admission and reclamation fences in both
stores and the PostgreSQL triggers. Pending parts and final capacity admission
remain reserved until completion or verified abort; recovery does not admit
the final object twice.

## Migration and compatibility

The additive migration stores the result, dispatch flag, recovery cursor and
sticky observation. Existing in-flight completions are conservatively marked
dispatched because an older process may already have sent them. Existing
completed sessions retain their unknown result; do not invent a version by
inspecting a later current object. Error-only custom providers retain legacy
completion behavior and gateway ETag reconstruction.

Deploy the updated completion writers and recovery workers together. Older
workers cannot safely settle result-aware dispatched rows. Pause/drain old
writers before rollback; the down migration refuses to discard any populated
completion identity, dispatch, observation or cursor. Keep the schema and roll
forward when those records must remain replayable.

Provider versioning configuration, version deletion/tagging, proof retention on
unversioned providers, direct URL write recovery and historical GCS generation
proof remain open in the implementation ledger.

## Validation

Local AWS SDK → SigV4 gateway → S3 adapter → HTTP fixture tests cover actual
ETags, owned/null/unversioned completion IDs, exact-version reads, stable replay
after overwrite/delete, changed parts/conditions, lost/truncated/malformed
acknowledgments and two-page recovery across PostgreSQL store restart. Memory
and PostgreSQL suites validate ownership, stale leases, atomic mappings,
immutability, rollout backfill, rollback refusal, reservations and sticky
accounting fences. Typed control-plane clients cover complete/get/list results
and replay with both stores. Adapter tests include ambiguous historical proof,
per-attempt accounting failures, multipart sizes beyond a single PUT, and GCS
result/probe behavior. Race and pinned lint checks supplement these tests.
All providers used for tests are local fixtures.
