# ADR-843: GCS conditional PUT capabilities

Date: 2026-10-08
Status: Accepted

## Context

ADR-530 and ADR-535 support conditional S3 PUTs and recoverable write receipts.
The GCS adapter previously rejected these writes. Native GCS XML PUT supports
content-generation preconditions; XML If-Match is a read predicate. Forwarding
the customer's If-Match header on a native PUT would silently lose the condition.
GCS XML multipart upload requests do not support preconditions.

## Decision

Support create-only and replacement single PUTs through the GCS adapter,
including tracked receipts and enrolled AES256. Translate If-None-Match `*` to
a signed `x-goog-if-generation-match: 0`. For If-Match, admit and meter a native
XML HEAD immediately before gateway signing, check one strong quoted XML ETag
or the existence wildcard, and sign the observed positive content generation.
Never substitute the JSON metadata ETag. A concurrent content replacement
receives a native 412, including a replacement with identical bytes/ETag. This
conservative generation fence prevents overwriting a write that raced the
observation. Weak ETags and ETag lists remain explicitly unsupported on GCS.
Use the XML ETag returned by a gateway HEAD or CLI upload/download result for
replacement. The current GCS object-listing ETag comes from JSON metadata and
cannot be used as this content predicate; listing ETag parity remains separate.

The HEAD requires a recorder. Gateway safety mode atomically reserves each
actual request against its request/egress safety budget; it also rechecks the
credential, bucket and service state before observation. An unavailable or
malformed observation produces no signed native PUT. The destination mutation
keeps the existing durable single-dispatch fence. Unknown acknowledgments remain
pending and recover only through an exact private receipt/generation proof;
neither recovery nor a URL retry performs a second native PUT.
Preflight failure settles only a prepared receipt, atomically against dispatch.
A losing preflight cannot settle another request's dispatched attempt. A known
rejection releases the pending key fence; capacity grants remain conservative
until the existing inventory reconciliation rebases them.

Expose mutually exclusive `if_match` and `if_none_match` on ObjectSignRequest.
Persist them inside the existing immutable URL capability, sign their branded
headers, and reject any removal, replacement, duplication or additional write
predicate. Existing URLs retain their exact previous request. New PostgreSQL
validation preserves protected writes, version-bound reads and unknown-field
rejection, and refuses rollback while conditional authority is persisted.
Old URL decoders reject the new descriptors; they cannot execute them without
their condition. Apply migrations before upgrading all API/gateway replicas.
Roll back dependent validators newest first.

Add CLI single-PUT upload flags `--if-match` and `--if-none-match`. Reject a
conditional transfer that would switch to multipart before creating an upload
session. Reject combining these flags with multipart resume. The CLI does not
silently weaken a condition to finish a large transfer.

## Multipart boundary

GCS conditional multipart completion continues to return 501. Enabling it
requires a separate durable staging/publication contract: private staging keys
excluded from customer access but included in provider inventory, peak capacity
admission, an immutable publication precondition and dispatch fence, exact
receipt recovery, and generation-bound cleanup before releasing reservations.
No HEAD plus unconditional completion fallback is allowed. That architecture
is a subsequent implementation, not a claim of this PUT capability.

## Validation

Provider wire fixtures cover concurrent create and replace, native signed
generation headers, metered XML observations, malformed/missing proof and
preserved receipts/encryption. Gateway and memory/PostgreSQL capability tests
cover immutable headers, durable round trips, budgets and migration validation.
CLI tests cover propagation, invalid combinations and refusal before multipart
admission. Local fixtures are not real-provider or production qualification.

References: [GCS PUT object](https://docs.cloud.google.com/storage/docs/xml-api/put-object),
[GCS request preconditions](https://docs.cloud.google.com/storage/docs/request-preconditions),
and [GCS multipart limitations](https://docs.cloud.google.com/storage/docs/multipart-uploads).
