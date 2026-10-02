# ADR-405: Durable ordinary and mutable null S3 deletion

Status: Accepted (2026-10-03)

ADR-406 extends this journal to immutable deletion and permits bounded paginated baselines.

## Context

An ordinary delete creates a new marker in an Enabled bucket and replaces the
mutable null entry with a null marker in a Suspended bucket. Retrying a null
version deletion can remove a later write. A check of accounting/configuration
before provider dispatch cannot serialize these operations with new writes or
versioning transitions. ADR-404 handles only immutable selected versions.

## Decision

Journal each ordinary or null deletion before contacting the provider. One
active intent per bucket excludes new write admissions, multipart initiation,
configuration transitions, inventory/reclamation and bucket deletion. Admission
uses the same account serialization and bucket lock ordering as configuration
and capacity intents. Public reference recording acquires the bucket before
the account to avoid a lock-order cycle with deletion/configuration admission.
Existing admitted writes and multipart activity must drain before deletion.
Unsafe legacy grants prevent native versioned admission. Ordinary cleanup of a
confirmed unversioned bucket remains available and retains every legacy grant;
its acknowledgment never refunds those grants. Native marker creation additionally
requires a ready configuration cutover and verified all-version accounting.

An ordinary versioned delete reserves one entry and the UTF-8 key bytes before
dispatch. Completion retains the reservation until a verified version inventory
replaces usage. A known pre-dispatch cancellation or definitive provider rejection
refunds that reservation. The S3 adapter recognizes only a parsed AccessDenied
error with HTTP 403 as rejection proof; status alone, transport failures and
timeouts remain uncertain. A rejected receipt remains durable and cannot dispatch
again; new writes can proceed once its fence is released.
Null deletion and unversioned ordinary deletion do not create entries and remain
available without requiring a fresh usage report or unspent safety budget.

Before an Enabled ordinary delete, record SHA-256 identities of every native
version/marker for the exact key in one complete provider response, at most 1000
entries. A truncated exact-key baseline fails before mutation and releases the
reservation; permanent immutable deletion can reduce history before a new
attempt. Permanent deletion can invalidate a continuation cursor, so a paginated
preparation cannot establish complete prior identity. Recovery may scan up to
eight pages and 4096 entries because the complete baseline already identifies
every old version. Persist the baseline and dispatched
phase atomically, then issue one provider attempt. Recovery never sends another
DELETE. It can settle an uncertain Enabled delete only after finding exactly one
immutable marker absent from the baseline. Every owned write is fenced during
this interval; provider-side external writers remain outside the ADR-398 trust
contract. Concurrent permanent version deletion can remove proof, which keeps
an uncertain intent pending; it cannot turn an old recorded marker into proof.

Successful provider acknowledgments settle ordinary Enabled/Suspended deletes,
unversioned deletes and explicit null deletes. Owned public marker IDs and the
receipt commit together. Invalid/missing acknowledgment identity is uncertainty,
not success. Provider IDs stay private. Stale/expired leases cannot dispatch or
settle. An abandoned prepared intent can be cancelled; a dispatched intent has
no time-based cancellation. A provider rejection settles it as failed only with
the adapter's explicit rejection proof. Absence never establishes completion.

A lost acknowledgment for null, Suspended ordinary or unversioned deletion
retains the bucket fence. These mutations have no immutable unique completion
identity. This increment exposes that uncertainty honestly; automatic recovery
for those cases still requires retained per-attempt proof or generation-isolated
physical keys and remains in the implementation ledger.

## Customer surfaces

S3 single and bulk deletes share the service. Signed `X-Gregale-Delete-Id` or
signed AWS SDK invocation IDs identify retries. Bulk entry IDs derive from the
request identity and entry position; replaying a different key/selector under
an existing identity conflicts. Clients without either header receive a generated
ID. Bulk responses return the batch ID in X-Gregale-Delete-Id; mutable-entry
receipt IDs are UUIDv5 of that namespace and `bulk-entry:N`, the zero-based
position. Retry the unchanged batch. Ordinary S3 responses return the public
marker identity; bulk responses use DeleteMarkerVersionId. Missing/quiet
successes follow S3 response shape.

The control API exposes POST objects/deletions with caller-supplied UUID, key and
optional null selector, plus GET objects/deletions/{id}. Responses are completed
200 or pending 202 receipts. Reuse the ID and payload to replay. Storage write
scope and the owned bucket write grant protect both operations. Existing DELETE
routes use the same journal and return X-Gregale-Delete-Id. Go, Node and Python
clients and `gregale bucket deletions start/status` expose persisted progress.
Recovery remains enabled when customer storage ingress is disabled.

## Verification

Local provider protocol fixtures and memory/PostgreSQL integration tests cover
single/bulk acknowledgments, marker accounting, lost response, persisted baseline,
store reconstruction, stable public identity, receipt replay, protected null
uncertainty, configuration/write/inventory fences, lease expiry and tenant grants.
They also prove that a truncated baseline cannot dispatch or retain a marker
reservation, definitive permission rejection releases the fence, and legacy
unversioned cleanup retains its conservative grants. Missing provider placements
cancel expired prepared intents and durably defer dispatched intents. Due-work
ordering prioritizes the oldest retry time so deferred work cannot starve other
buckets.

Focused race checks pass across state, provider, S3 gateway, control API, Go
client and CLI. Full provider/gateway race regression and the broader object
state/control regression pass; focused SDK/control race checks also pass after
the complete-baseline restriction. Migration round-trip/fence checks, typed SDK
route coverage, four Node and four Python client tests, scoped Python Ruff,
sqlc generation parity and Node generation determinism pass. Both OpenAPI copies
match and the spec linter reports zero errors. Changed-code golangci-lint 2.4.0
and repository text, quoting, ADR-number and runbook SQL gates pass.
No live provider qualification is part of this task.

AWS references: [DeleteObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObject.html),
[DeletedObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeletedObject.html),
[GetBucketVersioning](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketVersioning.html),
[ListObjectVersions](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectVersions.html),
[suspended deletion](https://docs.aws.amazon.com/AmazonS3/latest/userguide/DeletingObjectsfromVersioningSuspendedBuckets.html).
