# ADR-545: Durable bucket versioning configuration

Status: Accepted (2026-10-02)

## Context

ADRs 540–544 provide retained-version inventories, public version identities and
tracked writes, but Gregale cannot safely enable or suspend bucket versioning.
An enable request can succeed without its acknowledgment reaching the caller;
current-object accounting then undercounts retained objects. Suspension does
not remove older versions. Existing direct write grants cannot be presumed safe
merely because their signed URL expired.

AWS exposes Enabled and Suspended, with an empty initial GET configuration.
Existing objects keep their null version. Initial enablement has a propagation
period; AWS recommends waiting fifteen minutes before object writes/deletes.
See [PutBucketVersioning](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketVersioning.html)
and [versioning workflows](https://docs.aws.amazon.com/AmazonS3/latest/userguide/versioning-workflows.html).

## Decision

Persist one account/bucket-owned configuration journal in `object_bucket_versioning`.
Separate desired intent from observed provider status. States are waiting,
propagating, inventory and ready. Requests for the same active target are
idempotent. An opposite target conflicts until the previous cutover finishes;
there is no cancellation of a possibly dispatched configuration change.

A request fences fresh writes, multipart reservations, ordinary inventories and
bucket deletion atomically with its intent. Already admitted writes and existing
multipart cleanup may finish. Pending writes or multipart sessions defer provider
configuration. Unsafe legacy grants reject explicit configuration requests before
provider mutation; they are never refunded using a timer.

Workers claim expiring leases, read configuration before each possible PUT, persist
dispatch and the accounting observation before sending one provider attempt,
and verify with another GET. Lost acknowledgments retain the fence and retry the
read before considering another PUT. A stale lease cannot advance a cutover.
Provider requests are counted before dispatch. Recovery continues with object
data ingress disabled.

Every dispatch holds the fence for at least fifteen minutes. Discovery of native
configuration also starts this gate, including on a previously empty bucket.
After propagation, the journal atomically creates a linked all-version capacity
job. Only a complete verified inventory and a final matching provider GET make
the journal ready. Cancelled, blocked or failed inventories retain the versioning
fence; recovery creates a fresh bounded inventory. An unrelated inventory already
running when native configuration is discovered cannot rebase usage; it waits
and eventually expires before adoption can continue.

`versions_required` is sticky. Suspension and a later empty provider response
cannot restore current-object accounting. Database triggers also protect leases'
transition identity, reject current or unrelated inventory during cutover, and
prevent readiness without a propagated, verified all-version job. Memory-store
behavior mirrors these guards. Native inventory remains valid only with the
managed-writer assumption from ADR-540: independently configured external writers,
lifecycle and replication require coordination before qualification.

The S3 data plane adds GET/PUT `?versioning`. GET returns provider truth. PUT
returns 200 after observing the desired provider status; object writes remain
fenced during the ensuing propagation/inventory. A durable accepted request can
continue after a 503 caused by an uncertain acknowledgment. The control API adds
GET/PUT `/v1/apps/{slug}/buckets/{bucket}/versioning`, with 202 for durable intent
and explicit progress. Control changes require storage manage scope and bucket
write access; S3 changes require bucket write credentials. Configuration bodies
are bounded and signed payload/checksum verification precedes intent. Duplicate,
unknown and unsupported fields fail explicitly. Provider GET XML is bounded and
validated before interpreting an empty configuration.

Go, Node and Python typed clients and the CLI expose configuration/progress.
The CLI supports `bucket versioning status|enable|suspend`. GCS and unsupported
S3 endpoints fail explicitly. MFA Delete changes are unsupported; an existing
MFA-protected configuration can be observed/adopted, but cannot be changed.

## Acceptance

Local fixtures exercise the real AWS SDK, SigV4 gateway, S3 adapter and PostgreSQL
worker: lost configuration acknowledgment, paged inventory failure, reconstructed
worker, enable/suspend, public write identity and per-version quota after cutover.
Memory/PostgreSQL tests cover owned intent, retries, draining, leases, stale
workers, cancelled inventory replacement, sticky accounting and legacy grants.
Protocol tests cover unsupported providers, malformed/duplicate/oversized XML,
MFA directives, permission failure and single provider attempts. Generated clients
are regenerated from the same OpenAPI contract. No live provider was contacted.

Version deletion, delete-marker admission, version tagging, direct-write replay,
Object Lock, lifecycle, notifications and replication remain separate tracked
increments. This change does not establish production provider qualification.
