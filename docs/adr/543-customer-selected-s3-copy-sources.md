# ADR-543: Customer-selected S3 copy sources

Date: 2026-10-02
Status: Accepted

## Context

ADR-541 binds source admission and copy dispatch to the latest inspected native
version. ADR-542 exposes durable customer version IDs and exact reads, but the
gateway still rejects customer-selected copy sources. Customers therefore cannot
restore older data using standard S3 clients.

The [CopyObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_CopyObject.html)
and [UploadPartCopy](https://docs.aws.amazon.com/AmazonS3/latest/API/API_UploadPartCopy.html)
contracts accept `?versionId=ID` in the copy-source header and return a source
version header. A selected delete marker cannot serve as copy data.

## Decision

Parse the encoded bucket/key path separately from a single `versionId` query
parameter. Decode each once, preserving encoded question marks within keys.
Require the credential's source bucket and both read/write permissions. Resolve
the public ID against account, ready bucket and exact source key before any
provider request. Unknown and foreign IDs cannot reach storage.

Introduce explicit optional exact-version snapshot capabilities for ordinary
tracked and multipart copies. The S3 adapter signs the selected version into
HEAD, with one attempt and the existing size/metadata validation. Non-null
success must return that exact version; missing, duplicate or mismatched identity
headers fail before write admission. An unversioned provider may omit the null
header. Selected-marker HEAD errors are normalized to a safe bad request.
Adapters without these capabilities return NotImplemented. The GCS copy adapter
also explicitly rejects a supplied native selector rather than ignoring it.

Bind CopyObject and UploadPartCopy to the same selected native identity, including
an explicit `versionId=null`. Null retains the measured ETag fence and remains
mutable; independently restrictive dates remain unsupported under ADR-541.
Retained data versions preserve atomic date/ETag precedence, copied-byte admission,
metadata/tagging directives and multipart ranges. Check any provider source
version response header against the inspected source. A malformed acknowledgment
remains uncertain, with no automatic provider replay or reservation refund.

Persist the source's public reference before write admission. This also records
native observations from implicit latest-source copies and activates the existing
all-version accounting fence. Emit the customer source version ID only on a
successful copy response. Native identities never enter shared JSON or public
headers, XML or error details. Ordinary copies retain ADR-542's destination ID.

Restoration uses a same-key selected-version copy. With provider versioning
enabled it creates a new current version, keeps existing history and reserves
the full copied size plus one retained entry. Recovery uses the destination's
private receipt; it never repeats the source copy. Existing durable multipart
transfer fencing similarly survives process restart after an uncertain part copy.

## Scope and rollback

Reuse ADR-542's durable mappings and ADR-540's inventory/admission; no schema
change is needed. Copy credentials remain restricted to one logical bucket.
Cross-bucket customer copies, provider versioning configuration, version deletion,
version tagging and multipart completion version acknowledgments remain open.
Restoring data does not permanently remove a marker or implement version deletion.
Older binaries reject explicit version selectors but retain the same recovery
records, mappings and conservative accounting fences.

## Validation

Local AWS SDK → SigV4 gateway → S3 adapter → HTTP fixture tests cover same-key
restoration, exact older HEAD/copy selection, preserved data and metadata,
source/destination public IDs, retained history, multipart range/list/completion,
unknown/foreign selectors, credential permissions/revocation, null selection,
delete markers, provider identity failures, mapping failures and quota rejection.
Memory and PostgreSQL paths are exercised. PostgreSQL restart tests retain
dispatched receipts and part fences after lost/truncated/wrong-version responses
and verify exact destination receipt proof without another copy attempt.
Adapter tests verify malformed headers and private DTO serialization. Affected
package race suites and pinned lint supplement these protocol tests. No real
provider environment is used.
