# ADR-564: Durable owned bucket Object Lock

Date: 2026-10-04
Status: Accepted

## Context

ADR-563 provides native primitives. Customer enablement still needs durable
intent, immutable versioning protection, restart recovery and ownership. A
default retention change must not alter the policy of an accepted transfer.

## Decision

Persist an account/app/bucket-owned Object Lock journal. Enablement intent and
any native Enabled observation establish a permanent versioning requirement,
including when a future native policy cannot be interpreted. Native observations
and accepted desired defaults remain separate. Unknown policy reads fence writes
and never become empty defaults. Unenrolled native policies are adopted only
after verified versioning/inventory and do not become an automatic rewrite target.

Serialize state transitions under account, bucket and policy locks. Atomically
request Enabled versioning with accepted Object Lock enablement. Reuse its
propagation and verified all-version inventory cutover before native Object Lock
dispatch. Reject suspension from accepted intent onward, including old SQL
writers. A proven native Enabled observation may supersede a stale pending
Suspended intent: revoke its lease, preserve any inventory job and require a
fresh propagation interval. Physical Object Lock prevents that stale native
suspension from succeeding. No speculative enablement intent permits this
exception.

Capacity requests, deletion, lifecycle, notifications and encryption
configuration also acquire account locks before bucket locks. The database
admission guard takes bucket SHARE, so bucket-before-account mutation order
would deadlock with an account-locked admission despite a NO KEY UPDATE bucket
lock. PostgreSQL contention tests exercise each mutation against a raw grant
admission with the full guard set installed.

Fence new admissions while configuration is unresolved. Existing writes and
multipart sessions drain under the preceding native default before a new default
is dispatched; configuration cannot cancel them or change their quota. Deletion
and account/bucket cleanup must preserve these fences. Clearing the default keeps
Object Lock Enabled and leaves existing version protection intact.

Reconciliation uses durable bounded leases, revision checks and one native PUT
per attempt. Read native truth before every possible mutation and verify the
result afterwards. Recover lost acknowledgments by reading the accepted policy
without repeating a successful PUT. Meter all native requests. Existing intent
continues to reconcile after ingress or operator capability is disabled.

Declare Object Lock capability explicitly per S3 backend, with separate event
hold support. Require the full native versioning/history/lock contract, retain
immutable placement and reject unsupported combinations before accepting intent.
Expose standard S3 configuration, owned control management/progress, Go/Node/Python
SDKs and CLI. Enabling through S3 succeeds only after native verification; a
pending versioning cutover yields OperationAborted with durable idempotent intent.
The control API reports accepted progress. Signed/bounded XML and strict bounded
JSON reject unknown fields, duplicate fields and attempts to disable Object Lock.

## Implementation checkpoint

The durable journal and native reconciliation service are implemented locally.
Memory/PostgreSQL tests exercise ownership, immutable enablement, idempotency,
leases, revision fencing, accepted single/multipart write drain, raw SQL and
rollback guards. Native HTTP tests meter every request and reconstruct adapters
and stores after a successful PUT with a lost acknowledgment. A strict unknown
policy preserves its native-enabled hint. Verified absence on an unenrolled
bucket can recover without rewriting policy or draining unsafe legacy grants.
Superseded suspension inventory drains its existing worker, then requires a new
scan created after the current propagation interval; database guards also reject
reuse of the old scan. Bucket-parent cascades preserve history until a leased
physical deletion has been settled. The customer control API, standard S3 routes, bounded daemon worker,
capability enrollment, typed Go/Node/Python clients and CLI are now connected.
Control PUT reports 202 with durable progress; S3 PUT reports success only after
exact native readback. Existing progress can be read with enrollment disabled,
and accepted reconciliation ignores enrollment and ingress flags. Defaults
include fixed and separately enrolled event hold periods. Public per-version
management is not advertised by this bucket capability.

Only an exact native configuration-not-found 404 establishes absence. An empty
200 document or an empty Rule is unknown, preserves the admission fence and
never authorizes a default clear. The Python generator retains typed nested
DTOs by removing validation-only compositions from its temporary input; the
source OpenAPI schema and server enforce cross-field predicates.

## Acceptance

Local memory/PostgreSQL/native HTTP/TLS qualification passes through the owned
control client, standard AWS SDK gateway and bounded daemon worker. Tests cover
quota/drain, ownership, lost acknowledgment, reconstructed stores,
disabled-capability recovery, propagation/inventory, conflicting suspension,
lease races and raw SQL/rollback guards. Full API/provider/gateway and CLI suites,
object/bucket control regressions, focused races and Go/Node/Python clients pass.
Node and Python regeneration is deterministic; embedded OpenAPI parity and SDK
route coverage pass. OpenAPI lint passes with existing warnings. Changed-line
Go lint and the Python generator/client lint pass after correcting a wrapped
error assertion and removing an unused generator variable.

For the scoped pre-1.0 release, keep new Object Lock enrollment disabled by
omitting `object_lock` or setting `object_lock.enabled:false` on each backend.
Accepted intent must continue recovery even with enrollment disabled. Bucket
configuration is qualified locally; unrestricted protection management and
cleanup are not part of this release contract.

Per-version retention/legal hold management and protected deletion/lifecycle/
account cleanup remain part of the broader S3 goal. This bucket configuration
increment cannot establish automatic completion-proof retention by itself. No
real provider environment or deployment is required.

## References

- ADR-545: durable versioning and all-version accounting.
- ADR-563: native Object Lock protocol.
- [S3 irreversible enablement](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-lock-configure.html)
