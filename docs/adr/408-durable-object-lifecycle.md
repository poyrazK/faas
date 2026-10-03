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

The foundation supplies internal validated rules, eligibility calculations,
and memory/PostgreSQL scan persistence. The expiration service now binds actions
to the deletion journal and dispatches through the existing S3 adapter. Customer
surfaces, the discovery worker and rule-driven multipart cleanup remain required
before this ADR becomes Accepted. A completed scan records discovery progress;
it is never proof of deletion or reclaimed capacity.

Scans snapshot normalized rules and their revision. Only one scan per bucket
can remain active. A worker claims a bounded lease, checkpoints strictly
increasing keys and releases its lease between steps. Live leases block policy
replacement. Replacement after lease expiry cancels the previous scan; its old
token cannot checkpoint or reclaim work. Finished scans schedule another pass.
Progress and rule identity survive process reconstruction.

Each durable expiration action binds its scan, rule, action kind, native target
and original timestamp to an immutable private deletion receipt. The stores
atomically validate the scan lease and current revision when opening the journal
and before dispatch. PostgreSQL also guards the binding and dispatch transition.
Establish the mutation fence before the final exact-key history, age and tag
check. An old listing must never delete an intervening new current object.
Enumerate keys
without retaining a native version continuation identity across deletion.
Noncurrent age uses the successor's creation time; sole expired markers require
complete key history. Retained immutable versions use exact owned selectors.
Mutable-selector acknowledgment loss remains conservatively fenced.

Receipt IDs are stable for the same scan and target. Replay after reconstruction
preserves the original scan token and cannot authorize another dispatch. Before
dispatch, a cancelled scan fails preparation and releases only its reservation.
After dispatch, exact immutable deletion can recover under its existing journal
even if the rule is cancelled; mutable marker recovery uses the existing complete
baseline without issuing another DELETE. Equal version timestamps provide no
reliable cross-type order: only strictly newer noncurrent entries count toward
retention, and the next strictly newer timestamp bounds noncurrent age
conservatively. Complete key history remains bounded by the deletion limits.

Tag filters require every named tag and exact value, including empty values.
Tags are in-place metadata: asynchronous evaluation observes a snapshot and
does not imply ordered or exactly-once tag updates. Final evaluation reads tags
from the discovered exact native version; a removed required tag prevents
dispatch. Deletion acknowledgments never
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
The foundation and internal expiration service can roll out before customer
routes. No background lifecycle mutation starts until its discovery worker is
wired in. Worker and customer-surface acceptance remains outstanding.

Foundation verification passed locally: rule and eligibility tests, shared
memory/PostgreSQL persistence tests, competing lease claims, expired workers,
policy replacement, delayed retries, reconstructed stores, unchanged accounting
and migration/rollback guards. Focused race tests passed for these three Go
packages. Related object storage, provider and state regression tests passed.
Changed-file lint reported zero issues. SQLC's complete generated tree matches
isolated generation; all twelve lifecycle schema sections match a migrated
PostgreSQL database. Encoding, shell quoting, ADR uniqueness, runbook SQL and
Git whitespace checks passed. Provider interaction used local HTTP fixtures.

The expiration increment adds local S3 HTTP tests with memory and PostgreSQL
stores for current expiration, retained-version deletion, sole markers, changed
current objects, removed tags, page and request limits, lost acknowledgments,
receipt replay and immutable recovery after rule cancellation. Accounting tests
preserve baselines and reserve marker capacity before dispatch. Shared state and
migration tests cover stale scan tokens, revision cancellation, private detached
bindings, timestamp precision, immutable receipts and rollback refusal.

Protocol references:
[Lifecycle configuration elements](https://docs.aws.amazon.com/AmazonS3/latest/userguide/intro-lifecycle-rules.html),
[LifecycleExpiration](https://docs.aws.amazon.com/AmazonS3/latest/API/API_LifecycleExpiration.html),
[NoncurrentVersionExpiration](https://docs.aws.amazon.com/AmazonS3/latest/API/API_NoncurrentVersionExpiration.html).
