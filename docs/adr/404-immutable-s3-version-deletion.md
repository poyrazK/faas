# ADR-404: Permanent immutable S3 version deletion

Status: Accepted (2026-10-02)

ADR-406 adds durable dispatch and inventory coordination for immutable deletion.

## Context

ADRs 398–403 provide retained-version accounting, public version identities,
reads, copies, multipart completion receipts and bucket versioning configuration.
Customers need to permanently remove retained data and markers. A current-object
DELETE can create another marker; the mutable null selector can address a newer
object when retried. Those operations require durable admission and dispatch
coordination, unlike deletion of an immutable non-null version.

AWS permanently removes the exact version named by DeleteObject's versionId.
Deleting a marker can reveal an older data version. A DeleteObjects request has
individual successes/errors and optional quiet output. See
[DeleteObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObject.html)
and [DeleteObjects](https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObjects.html).

## Decision

Add the optional ObjectVersionDeleter capability for permanent immutable native
version deletion. The customer service accepts Gregale UUIDs and resolves the
account, bucket and exact key before any provider request. Native IDs remain
private. The null selector is explicitly unsupported in this increment.

Persisted public references survive deletion. A response lost after provider
commit, a gateway restart, or a repeated bulk entry continues to select the same
immutable version. No mutation journal or time-based inference is needed for
this operation: re-dispatch cannot remove a different version or create a marker.
An explicit NoSuchVersion provider response is idempotent success; missing buckets
and generic provider errors remain failures. Unowned public IDs fail before
provider contact. All provider attempts are recorded before dispatch. Request
recording failure prevents provider contact.

The S3 adapter sends one bounded SDK attempt and validates acknowledgment status,
identity and marker headers, including duplicate headers. Base ordinary DELETE
also sends one SDK attempt to avoid retry-created markers. Provider MFA, Object
Lock bypass and conditional-delete directives remain unsupported and cannot
silently degrade into unconditional deletion.

S3 DELETE with versionId returns the selected public x-amz-version-id and the
acknowledged delete-marker flag. DeleteObjects supports immutable selected data
and markers alongside individually rejected null/current operations in native
accounting mode. Errors retain their public selector; quiet output suppresses
only successes. Bounded XML rejects duplicate selectors, unknown elements,
nested selector content and unsupported directory-bucket conditions.

The control API adds DELETE /v1/apps/{slug}/buckets/{bucket}/objects/versions with
key and version_id query parameters, returning a typed result. It requires
storage write scope and bucket write access. Cleanup remains available with
data ingress disabled or safety budgets exhausted. Go, Node and Python clients
and gregale bucket version-delete expose the same operation.

DELETE never refunds quota directly. Verified fenced all-version inventory
reclaims storage only after admitted writes drain. Concurrent immutable deletion
can only decrease retained usage: an inventory that includes a subsequently
removed version is conservative. Public references and accounting observations
remain sticky; repeated deletion cannot reset native accounting.

Control and S3 current DELETE share the existing retained-version accounting
guard, and reject an observed configuration transition. This closes the control
API's bypass of the established native-accounting guard. It does not establish
atomic dispatch coordination for ordinary deletion: admission racing a later
versioning intent, marker creation, mutable null deletion and uncertain ordinary
DELETE recovery remain part of the next durable deletion increment.

## Acceptance

Local AWS SDK -> SigV4 gateway -> real S3 adapter -> HTTP provider fixtures use
both memory and PostgreSQL stores. They cover permanent data removal, marker
removal revealing prior data, exact private selectors, lost acknowledgment with
one provider attempt, reconstructed store retries, durable reference retention,
mixed/quiet bulk outcomes, tenant/key ownership, read/write permissions,
conditional header rejection, conservative quota and verified inventory refunds.

The typed control client crosses a reconstructed API server and PostgreSQL after
a lost acknowledgment; tests cover bucket grants, disabled data ingress and the
control native-accounting guard. Protocol tests cover malformed acknowledgment
headers, unavailable/denied/missing providers and failed request accounting.
Generated clients and CLI cover exact Unicode/punctuation selectors locally.
No real provider was contacted. This increment does not complete marker/null
admission, version tagging, lifecycle, events, encryption, retention, replication,
large-upload qualification or coordinated account deletion.
