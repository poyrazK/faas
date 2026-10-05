# ADR-542: Customer S3 version identities and reads

Date: 2026-10-02
Status: Accepted

## Context

ADRs 539–541 recover tracked writes and account for retained native versions.
Customers still cannot list that history or read a particular version. Passing
provider IDs through the branded endpoint would expose placement identities
and make customer IDs depend on the backing service. Native versions can also
first become visible through a read, rather than a tracked write receipt.

The [ListObjectVersions contract](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectVersions.html)
uses paired key/version markers. Its next version can be absent from the
returned entries. [GetObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html)
distinguishes a current delete marker (404) from an explicitly selected marker
(405, with its modification date).

## Decision

Persist one UUIDv4 for each logical bucket, object key and non-null native
version. The database enforces immutable identities and a unique native tuple.
Retries and process restarts return the same customer ID. Resolve a customer
ID with account, ready bucket and key ownership before any provider request.
Keep native IDs out of shared JSON DTOs, response XML, headers and errors.
The S3 `null` version remains the special mutable ID `null`, scoped by the
requested bucket and key; it is not an immutable restore point.

Add explicit optional provider capabilities for bounded version pages and
presigned exact-version GET/HEAD. The S3 adapter makes one list request without
SDK retries, requests URL-encoded keys, and validates data versions, markers,
metadata, result bounds and paired continuation. Page size stays within the
existing 1,000-result S3 limit; a persistence batch allows one additional next
version identity. Map that continuation even if it is absent from the page.
Preserve supported storage classes instead of labeling archived data STANDARD.

Expose ListObjectVersions with prefix, delimiter, key marker, customer version
marker, URL encoding and max-keys, including zero-result requests. GetObject
and HeadObject accept `versionId` with the credential's existing read permission.
Unknown IDs or IDs from another key/bucket/account fail before storage access.
Preserve ranges, metadata, checksums and read validators on the signed exact
provider version. A successful non-null version read must return the requested
native ID; missing, duplicate or mismatched identity headers fail before customer
body bytes. Mutable null reads also work when an unversioned backend omits
the version header. Conditional 304/412 responses may omit the identity and
still emit the already-resolved customer version ID. Normalize delete-marker errors and
never forward the provider error body.

Emit customer version IDs on current GET/HEAD and successful ordinary PUT and
CopyObject acknowledgments whenever the provider supplies a native ID. Convert
the identity before writing response body bytes. These observations survive
request cancellation using the existing bounded settlement deadline. Mapping
failure returns ServiceUnavailable; a completed tracked write retains its
existing receipt and conservative admission. Reads and lists use existing
authorization and provider-attempt accounting.

Any non-null native identity, or a null delete marker, durably latches native
version accounting. Include that observation in both state implementations,
inventory predicates and SQL admission/reclamation fences. New writes stop
until the existing reconciliation job establishes a verified all_versions
baseline. Ordinary null data alone does not activate the latch. Observations
are sticky, and mapping rows cannot be deleted while their bucket exists.
They cascade only when confirmed-deleted bucket metadata is finally pruned.

## Scope and rollback

Apply the version-reference migration before deploying the gateway. The schema
snapshot contains the affected live PostgreSQL definitions and committed sqlc
output. A populated mapping table prevents migration rollback from silently
discarding public IDs. Older binaries do not understand customer IDs or
read-driven activation.
Database fences still reject unsafe current-mode admissions and reclamation;
rolling back can leave writes unavailable and removes the new read/list APIs.
Retain the migration and the new state implementation while those buckets
transition to an all-version baseline.

This increment does not configure provider versioning. Multipart completion
version headers, customer version copy sources, version-specific tagging,
direct provider URLs, version deletion, marker creation/admission, restore,
retention and coordinated account deletion remain in the implementation ledger.
No GCS generation API is enabled by the S3-only optional capabilities.

## Validation

Local AWS SDK → SigV4 gateway → real S3 adapter → HTTP fixture coverage uses
memory and PostgreSQL stores. It covers PUT/copy version acknowledgments,
listing and paired continuation, older reads after overwrite, ranges, checksums,
current versus selected delete markers, conditional responses, invalid IDs,
credential permissions, mapping failures and provider identity mismatches.
State tests cover concurrent mapping retries, tenant/bucket/key isolation,
atomic invalid batches, restart, immutable database guards, null-marker
observations, cascade cleanup and activation of all-version inventory.
Migration and generated-source checks complement the affected package race
tests. Tests use local fixtures; no real provider is required or contacted.
