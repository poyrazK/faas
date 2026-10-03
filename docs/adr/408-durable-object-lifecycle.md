# ADR-408: Durable object lifecycle discovery and expiration

Status: Proposed (2026-10-03; implementation in progress)

## Context

Object storage supports owned retained versions and coordinated deletion, but
does not yet expose lifecycle policies. Native autonomous expiration would bypass
Gregale's deletion journal and all-version accounting fences. It could also
remove a version used as an inventory continuation identity.

## Decision

Implement lifecycle in the control plane using owned bucket policies, immutable
rule revisions, durable scans, bounded discovery and the existing deletion and
multipart journals. Rules support enabled/disabled status, prefix and tag
conjunctions, day/date expiration, noncurrent age with optional newer-version
retention, expired markers and prefix-filtered abandoned multipart cleanup.
Transitions and unsupported directives must fail explicitly.

The first increment supplies internal validated rules, eligibility calculations,
and memory/PostgreSQL scan persistence. It does not expose a customer lifecycle
endpoint or dispatch expiration. A completed scan records discovery progress;
it is never proof of deletion or reclaimed capacity. Customer surfaces and the
executor remain required before this ADR becomes Accepted.

Scans snapshot normalized rules and their revision. Only one scan per bucket
can remain active. A worker claims a bounded lease, checkpoints strictly
increasing keys and releases its lease between steps. Live leases block policy
replacement. Replacement after lease expiry cancels the previous scan; its old
token cannot checkpoint or reclaim work. Finished scans schedule another pass.
Progress and rule identity survive process reconstruction.

The executor must bind each durable action to its rule revision and atomically
validate the scan lease when opening the deletion journal and before dispatch.
Establish the
mutation fence before the final exact-key history, age and tag check. An old
listing must never delete an intervening new current object. Enumerate keys
without retaining a native version continuation identity across deletion.
Noncurrent age uses the successor's creation time; sole expired markers require
complete key history. Retained immutable versions use exact owned selectors.
Mutable-selector acknowledgment loss remains conservatively fenced.

Tag filters require every named tag and exact value, including empty values.
Tags are in-place metadata: asynchronous evaluation observes a snapshot and
does not imply ordered or exactly-once tag updates. The executor must define
and test that boundary before publication. Deletion acknowledgments never
refund storage; verified complete inventories remain authoritative. Multipart
cleanup must reuse existing durable abort and completion fences.

All limits live in pkg/api/limits.go: 1,000 rules, 255 Unicode characters per
ID, a 5 MiB normalized document, ten portable tags, up to 100 retained newer
noncurrent versions, a 32-policy batch, two-minute leases, 30-second operations
and retries, and hourly sweeps. Rules and pointer/map fields are detached at
every memory-store boundary. PostgreSQL guards immutable scan identity,
monotonic progress and terminal history. Rollback refuses populated policy or
scan tables. Account removal cascades stored policy and scan state.

## Acceptance and rollout

Implementation acceptance uses local HTTP provider fixtures and disposable
PostgreSQL only, as requested. Rule boundaries, ownership, concurrent workers,
expired leases, replacement, restart, abort/completion races and accounting
must pass before advertising lifecycle. No real provider environment is needed.
The foundation can roll out before customer routes because it dispatches no
provider mutations. Expiration requires a later executor increment and its
failure/restart evidence; this document must track that distinction.

Foundation verification passed locally: rule and eligibility tests, shared
memory/PostgreSQL persistence tests, competing lease claims, expired workers,
policy replacement, delayed retries, reconstructed stores, unchanged accounting
and migration/rollback guards. Focused race tests passed for these three Go
packages. Related object storage, provider and state regression tests passed.
Changed-file lint reported zero issues. SQLC's complete generated tree matches
isolated generation; all twelve lifecycle schema sections match a migrated
PostgreSQL database. Encoding, shell quoting, ADR uniqueness, runbook SQL and
Git whitespace checks passed. No provider was provisioned or contacted.

Protocol references:
[Lifecycle configuration elements](https://docs.aws.amazon.com/AmazonS3/latest/userguide/intro-lifecycle-rules.html),
[LifecycleExpiration](https://docs.aws.amazon.com/AmazonS3/latest/API/API_LifecycleExpiration.html),
[NoncurrentVersionExpiration](https://docs.aws.amazon.com/AmazonS3/latest/API/API_NoncurrentVersionExpiration.html).
