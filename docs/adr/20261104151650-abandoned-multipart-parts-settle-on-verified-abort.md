# ADR-20261104151650: Abandoned multipart parts settle on a verified abort

Date: 2026-11-04
Status: Accepted
Amends: ADR-590 (multipart part writers: uncertain dispatch is retained)
Related: ADR-1038 (durable multipart checksums), ADR-540 (version accounting)

## Context

A part attempt reserves a transfer token, is marked dispatched before its
provider request, and settles only on validated success or a qualified
rejection. An attempt whose outcome is unknown stays dispatched and unsettled
on purpose: Gregale cannot tell whether the part landed, and database guards
refuse to complete or abort the upload while such an attempt exists.

The 11-04 prod object storage test showed the cost. An AWS SDK client gave up
on an `UploadPart` mid-transfer. Its attempt never settled, so the upload's
abort answered `conflict` every 15 minutes indefinitely
(`needs_attention`). The bucket could not be deleted, and on GCS the pending
abort also held a versioning change open, which stopped the bucket's
inventory sweep and later failed its reads. Nothing ever resolved it: there
is no provider-backed part recovery, and expiry alone releases nothing.

## Decision

An aborting upload may settle an uncertain part attempt when both hold:

1. The attempt's transfer window has ended (its grant's `unsafe_until`,
   36 minutes after dispatch, covering the 30-minute transfer context, the
   internal URL and a grace). No request carrying that attempt can still
   reach the provider.
2. The worker has aborted the native upload and verified the abort
   (`VerifyMultipartAbort`: `NoSuchUpload` or an empty part list).

Together these prove the attempt wrote nothing that survives. The store
settles only attempts whose grant still holds their transfer token, only on
an aborting upload under the caller's lease, taking account, bucket and
upload locks in the abort's order. Settlement composes as today (the grant's
token clears and the mutation receipt is deleted). The abort then finishes
through the existing guarded path.

Completion is unchanged: an uncertain attempt still blocks it, because a
completed object could include the part.

## Consequences and acceptance

- Abandoned uploads finish aborting after the transfer window. Their capacity
  is released, and versioning changes and bucket deletion no longer wait on
  them.
- An abort that is not ready costs one provider part listing per retry.
- No migration: the writer guard already permits settling a dispatched
  attempt whose grant still holds its token.
- Acceptance: `TestObjectMultipartAbandonedParts{Mem,PG}`. Nothing settles
  inside the window or under another lease; after the window the attempt
  settles and the abort finishes.
