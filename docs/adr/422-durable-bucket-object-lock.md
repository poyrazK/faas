# ADR-422: Durable owned bucket Object Lock

Date: 2026-10-04
Status: Proposed

## Context

ADR-421 provides native primitives. Customer enablement still needs durable
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
physical deletion has been settled. Accepted intent is still an internal service
contract at this checkpoint.

## Acceptance

Pending implementation and local memory/PostgreSQL/native HTTP/TLS qualification
through customer APIs and clients, including quota/drain, ownership, lost
acknowledgment, disabled-capability recovery, propagation/inventory, conflicting
suspension, lease races, raw SQL/rollback guards and generated client parity.

Per-version retention/legal hold management and protected deletion/lifecycle/
account cleanup remain part of the broader S3 goal. This bucket configuration
increment cannot establish automatic completion-proof retention by itself. No
real provider environment or deployment is required.

## References

- ADR-403: durable versioning and all-version accounting.
- ADR-421: native Object Lock protocol.
- [S3 irreversible enablement](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-lock-configure.html)
