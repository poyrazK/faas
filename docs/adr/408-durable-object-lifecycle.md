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
to the deletion journal and dispatches through the existing S3 adapter. A durable
expiration worker and rule-driven multipart admission are wired into apid's
recovery loop. Customer surfaces remain required before this ADR becomes
Accepted. A completed scan records discovery progress;
it is never proof of deletion or reclaimed capacity.

Scans snapshot normalized rules and their revision. Only one scan per bucket
can remain active. A worker claims a bounded lease, checkpoints strictly
increasing keys and releases its lease between steps. Live leases block policy
replacement. Replacement after lease expiry cancels the previous scan; its old
token cannot checkpoint or reclaim work. Finished scans schedule another pass.
Progress and rule identity survive process reconstruction.

The expiration worker discovers one key with a key-only version listing, then
plans from bounded complete history. It never persists a native continuation
identity across mutation. Preliminary age/prefix checks only select potential
actions; final tags and history are revalidated by the expiration service. Each
step creates at most 32 new receipts. If a key has more actions, the worker leaves
its key checkpoint unchanged and resumes after a delayed retry. Terminal receipts
are skipped on replay, so failed tag checks do not starve later candidates or
overlapping rules. Completed receipts cannot redispatch after a lost checkpoint.
An unsettled bucket deletion pauses discovery before any provider request,
including when a lost acknowledgment leaves an empty provider listing.

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

Scans persist object and multipart phases separately. Mixed policies finish
object discovery before advancing to multipart discovery; abort-only policies
start with multipart discovery without listing provider versions. Older
abort-only scans advance their initial object phase without provider requests.
Multipart discovery reads at most 32 owned active sessions in UUID order,
excluding sessions created after the scan's immutable start time. Each step
admits or skips one session and commits its cursor in the same transaction.
An empty phase completes discovery, even when admitted aborts still need
provider recovery. Initiating sessions which become active behind the cursor
are eligible for a subsequent sweep.

Admission rechecks the live scan lease, current policy revision, prefix, age
and exact owned session, native upload ID and original session creation time.
It atomically changes an eligible active session to aborting and stores an
immutable private scan/rule/identity binding. Completion admitted first wins;
late part admission and URL publication fail once lifecycle admits the abort.
Neither admission nor discovery completion releases quota. Existing multipart
recovery owns abort, drain and verification, independently of later rule removal
or disabled S3 ingress. Lifecycle manages Gregale-owned sessions; discovery of
provider-only orphan uploads remains outside this scan.

PostgreSQL completion dispatch, finish, retry and rejection all lock bucket,
then account, then upload, matching lifecycle/configuration/inventory ordering.
A deterministic contested-bucket regression reproduced a deadlock in all four
paths before this correction. Memory multipart operations share the store clock
with lifecycle admission and recovery.

Fixed-size control-plane multipart signing also participates in the abort
fence, with a five-minute default URL lifetime and a fifteen-minute maximum.
Before publishing a provider URL, atomically recheck its owned session,
key and native upload identity, require the session to remain active, and
persist the maximum issued URL expiry plus the transfer and cleanup allowance.
Abort or completion admitted during signing withholds the URL. The stored
deadline is private, monotonic while active and frozen after mutation admission.
Upgrade backfills nonterminal legacy sessions conservatively through their
session expiry plus the maximum URL lifetime and cleanup allowance.

All multipart profiles use the durable abort executor: abort, wait for the
persisted deadline and tracked transfers, then verify a bounded empty part
listing or NoSuchUpload before marking the session aborted. The generic finish
method cannot terminate an abort. Failed verification or an uncertain abort
response keeps the journal retryable after reconstruction, including with new
S3 ingress disabled. Fixed-size legacy key grants remain untracked and reserved
after verified cleanup; neither elapsed time nor an abort acknowledgment
refunds them or rewrites the inventory baseline. A provider URL does not enforce
Gregale's transfer timeout on an external client. This deadline is a minimum
cleanup delay, not proof that every external transfer has stopped; direct-write
proof and reclamation remain separate outstanding work.

All limits live in pkg/api/limits.go: 1,000 rules, 255 Unicode characters per
ID, a 5 MiB normalized document, ten portable tags, up to 100 retained newer
noncurrent versions, a 32-policy batch, one key and 32 new object actions per step,
a 32-session multipart discovery page and one session checkpoint per step,
two-minute leases, 30-second operations and retries, five-second claim release,
and hourly sweeps. Rules and pointer/map fields are detached at
every memory-store boundary. PostgreSQL guards immutable scan identity,
monotonic progress and terminal history. Rollback refuses populated policy or
scan tables. Account removal cascades stored policy and scan state.

## Acceptance and rollout

Implementation acceptance uses local HTTP provider fixtures and disposable
PostgreSQL only, as requested. Rule boundaries, ownership, concurrent workers,
expired leases, replacement, restart, abort/completion races and accounting
must pass before advertising lifecycle. No real provider environment is needed.
The internal lifecycle components can roll out before customer routes.
Background expiration follows the S3 hot flag; disabling it pauses discovery and
new dispatch while the independent deletion recovery loop remains available.
Provider request recording must succeed before every attempt. Missing placements
are durably deferred so they cannot monopolize the next batch. Discovery and
the policy batch have bounded deadlines. Due policies use the active scan's
retry time as their scheduling priority; policies awaiting a new scan use their
sweep time. A repeatedly failing provider must yield to older waiting work once
its retry becomes due, even if it exhausts a sweep's deadline. Customer-surface
and rule-driven multipart acceptance remains outstanding.

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

Worker acceptance adds local S3 HTTP coverage with both stores for key-only
continuation after deletion, reconstructed stores, competing workers, lost
acknowledgments and checkpoints, cancelled provider requests with durable claim
release, single-attempt discovery failures, overlapping
tag rules and action limits with replayed failed receipts. Shared
memory/PostgreSQL tests cover retry fairness without changing persisted policy
schedules. PostgreSQL daemon integration covers
the disabled hot flag, missing placement backoff, expiration, reconstruction,
completed scan scheduling, provider attempt accounting and unchanged storage
baselines. Discovery completion never claims capacity reclamation.

Worker verification passed locally: 25 focused top-level race tests across API
rules, expiration, scan persistence and daemon integration, including separate
cancelled-discovery acceptance. The fairness regression failed against both
stores before the scheduling fix and passed afterward. Changed-file lint using
the pinned Go 1.25.13 and golangci-lint 2.4.0 reported zero issues. All four SQLC
files match isolated generation. Formatting, encoding, shell quoting, ADR
uniqueness, runbook SQL and Git whitespace checks passed. These results validate
the internal worker increment; customer lifecycle and rule-driven multipart
acceptance remain open.

The signing-fence prerequisite passed focused memory/PostgreSQL race tests,
local control API and S3 HTTP restart tests, existing gateway/provider multipart
regressions, and migration upgrade/rollback guards. These preserve legacy key
grants and inventory baselines. Pinned Go 1.25.13 and golangci-lint 2.4.0 reported
zero changed-file issues; lint compilation omitted debug information to reduce
temporary disk use while retaining all checks. All four SQLC files match isolated
generation, and the three changed schema sections match a fresh full migration
run. Formatting, encoding, shell quoting, ADR uniqueness, runbook SQL and Git
whitespace checks passed. Real provider tests were excluded as requested.

Rule-driven multipart acceptance passed local memory/PostgreSQL race tests for
tenant and immutable-identity checks, competing admission, bounded discovery,
the scan cutoff, completion winning admission and durable mixed-phase progress.
S3 SDK initiation and part writes through the gateway feed PostgreSQL lifecycle
admission and the existing S3 HTTP abort executor. Tests reconstruct the owner
between steps, remove rules, disable new ingress, lose both initial abort
responses, retain quota while parts remain, verify NoSuchUpload and replay
terminal history without another provider request. Migration acceptance covers
an existing object-phase scan, admission/identity/progress guards, recovery after
rule replacement, refusal to erase history and empty rollback/reapply. The four
deterministic completion lock regressions pass after reproducing deadlocks
before the fix. Existing multipart, lifecycle worker and daemon regressions
passed; repaired test fixtures have separate final acceptance logs. Go 1.25.13
race checks and pinned golangci-lint 2.4.0 passed with zero changed-line issues.
SQLC's four files match isolated generation and seven changed schema sections
match the fully migrated PostgreSQL schema. Customer routes and clients remain
open; no real provider was used.

Protocol references:
[Lifecycle configuration elements](https://docs.aws.amazon.com/AmazonS3/latest/userguide/intro-lifecycle-rules.html),
[LifecycleExpiration](https://docs.aws.amazon.com/AmazonS3/latest/API/API_LifecycleExpiration.html),
[NoncurrentVersionExpiration](https://docs.aws.amazon.com/AmazonS3/latest/API/API_NoncurrentVersionExpiration.html).
Multipart cleanup follows the
[AbortMultipartUpload verification guidance](https://docs.aws.amazon.com/AmazonS3/latest/API/API_AbortMultipartUpload.html).
External capabilities retain the
[presigned URL request-start expiration semantics](https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-presigned-url.html).
Key-only continuation follows
[ListVersionsRequest key-marker semantics](https://docs.aws.amazon.com/AWSJavaSDK/latest/javadoc/com/amazonaws/services/s3/model/ListVersionsRequest.html#getKeyMarker--).
