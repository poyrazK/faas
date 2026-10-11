# ADR-628: GCS tracked writes and native generations

Date: 2026-10-07
Status: accepted for local qualification

## Context

The GCS adapter supported basic transfers, but lacked the receipt contracts
required by durable uploads, copy grants, version controls and encryption.
GCS assigns generations to every object, including in unversioned buckets.
It archives generations on ordinary versioned deletion and has no S3 delete
markers. Treating its generations as S3 delete-marker history makes recovery
unsafe or permanently blocks deletion.

## Decision

Dispatch tracked XML PUT and COPY exactly once, with a reserved receipt in the
same mutation. A valid acknowledgment includes an ETag and a canonical positive
generation. A missing acknowledgment stays pending. Recovery reads the exact
receipt and size in current or retained generations; it never replays a write.
Bound historical cursors to bucket, key, receipt, size, multipart identity and
encryption intent. Keep native generation identities behind owned version IDs.
Confirm retained receipts with a separately metered XML HEAD of the exact
generation. GCS JSON and XML ETags can differ; recovered receipts must preserve
the ETag used by interoperable reads and copy preconditions.

Copy snapshots capture the native XML ETag, generation, metageneration, date,
portable metadata and tags. Full copies fence both source generations. GCS has
no native UploadPartCopy: use a bounded native GET streamed into one part PUT.
Reserve this extra source request and the source read length before GET in
gateway safety mode. Check exact Content-Range, generation, ETag and length.
The full reservation survives interruption; it is a safety budget, not an
exact delivered-byte billing meter.

Persist ordinary GCS deletion mode as `GCS_Enabled` or `GCS_Suspended` in the
private deletion journal. Before dispatch, capture one current generation in
the existing private 32-byte hexadecimal baseline. All-zero means the key was
absent. Do not reserve a nonexistent S3 marker. Delete without a generation
query and with `x-goog-if-generation-match` so native versioning archives the
captured generation. Recovery may repeat this same fenced DELETE: 404 or 412
proves that generation is no longer current and cannot delete a replacement.
This settles the captured intent, not a promise to erase a later replacement.
An absent-key capture never deletes a later object. Inventory alone reclaims
capacity. Exact-version deletion uses the immutable native generation query.
SQL constraints enforce the native journal shape and rollback refuses to
discard any captured generation fences.

Versioning patches touch only the native Boolean. GCS reports disabled
versioning as Suspended; it has no equivalent of S3's never-enabled status.
Use the existing cutover and complete all-generation inventory requirements.
Interoperable XML version listings preserve IsLatest and pagination without
synthesizing delete markers.

Explicit AES256 uses Google's managed encryption baseline. Require the stored
receipt, intent proof and generation to match, with no native CMEK, before
acknowledging an encrypted write. Clear bucket default CMEK only by matching the
observed key and bucket metageneration. Existing objects are untouched. Reject
CMEK, DSSE, bucket keys and contexts rather than advertise unsupported intent.
Native encrypted source reads fail closed for unhandled encryption headers.
Object Lock remains unsupported: existing-bucket enrollment, locked retention
and governance bypass differ from S3 and need a separate complete contract.

CLI file transfers use the branded capabilities and an unauthenticated client,
stream regular files, select multipart above the advertised single PUT limit,
and preserve pending session IDs. Downloads use a same-directory temporary
file and publish atomically only after success. Never follow transfer redirects
or expose signed URLs. Usage output identifies unavailable meters as unknown.
Multipart capability preparation validates the configured part limit separately
from the single PUT limit, so automatic multipart works when parts are larger
than ordinary PUTs. The ordinary PUT bound remains enforced.

## Qualification

Require memory/PostgreSQL journal parity, lost-ack recovery across store
replacement, replacement preservation, malformed native proofs and pagination,
source fences, budget rejection, range validation, encryption CAS, and atomic
CLI download tests. Qualify the native GCS wire protocol on disposable buckets
through a local process before claiming provider support. This does not enable
production storage, deploy a binary, or qualify provider cost billing.

Native contracts: [copy](https://docs.cloud.google.com/storage/docs/xml-api/put-object-copy),
[version listings](https://docs.cloud.google.com/storage/docs/xml-api/get-bucket-list),
[versioned deletion](https://docs.cloud.google.com/storage/docs/using-versioned-objects),
[Object Retention Lock](https://docs.cloud.google.com/storage/docs/object-lock).
