# Data platform hardening

Assessment date: 2026-10-15. Starting source: `8b66d31d501b3fffb8428727f176e048e4e539d7`.
Capability promotion continues to follow `pkg/productcap/catalog.json` and
`docs/capabilities.md`. This plan enables no production gates.

## Work sequence

| Priority | Work | Completion evidence | State |
| --- | --- | --- | --- |
| P0 | Qualify one supported managed PostgreSQL/Data API/Auth journey | Current provider v8 report, matching native guest report and hosted customer canary, including rotation, migrations, park/restore and cleanup | Pending isolated provider, account and native-host setup |
| P1 | Bound existing realtime channel grants | Idle and buffered-delivery revocation, callback outage recovery, endpoint changes, focused tests and race checks | First implementation: ADR-1385; deployed qualification pending |
| P1 | Complete restore-to-serving activation | Writer/session drain evidence, atomic binding publication, snapshot invalidation, application checks and explicit recovery/backout procedure | Pending; keep customer activation unavailable |
| P1 | Establish production data capacity and health contracts | Aggregate connection budgets, pool/query timing, controlled SQL probes and concurrent app/database wake load tests | Data API admission, health, gateway and combined engine transaction timings, pool counts, response limits, per-user rate/concurrency limits, a local load/recovery verifier, read-only budgets and an opt-in qualified capacity observer and authenticated resident pool reports implemented; automated allocation sources, consumer coverage, budget enforcement, isolated SQL/pool-wait timing and deployed load qualification pending |
| P1 | Release supported client packages | Owner-approved SDK licensing, immutable versioned releases, provenance and tested CLI/runtime compatibility | Pending licensing and registry decisions |
| P1 | Reconcile complete provider spend | Provider allowances and branch charges, delayed corrections, deletion settlement and qualified budget cutoffs | Pending provider/exporter qualification |
| P2 | Reconcile capability discovery, docs and onboarding | Dedicated maturity entries, consistent customer guides and measured first verified transaction | Pending |
| P2 | Expand provider and change-feed coverage | Qualified operation matrix, orphan reconciliation, reviewed feed payload/latency limits and customer-driven backfill/partition support | Pending |

## Data API rotation checks

Local Data API rotation diagnostics now includes read-only owner preflight,
stable repair codes, and existing rollout/retirement metadata (ADR-1668).
Preflight does not approve drift or prove provider revocation; deployed
qualification remains part of the P0 journey above.

The SDK and note/order examples also provide read-only receipt recovery
(ADR-1669). Lost responses can be checked without replaying a mutation; missing
or purged receipts remain unresolved. Write retry stays explicit and subject to
the application's identity and retention contract.

## First slice: retained channel authorization

ADR-1385 refreshes the existing application callback for retained WebSocket
channels using SSE's 30-second positive grant lifetime. Idle polling and outbound
delivery enforce expiry; callback denials remove subscriptions while outages
close the connection for retry. Existing ticket and durable access controls keep
their own checks.

Required evidence includes concurrent refresh, policy/token replacement,
callback timeout/denial classification, idle route/presence removal, and queued
message/presence/signal/read-receipt/checkpoint rejection after expiry. Run the
full realtime package, race checks and applicable repository policy checks.

Local verification passed for the full realtime package with and without `-race`,
changed-line lint, documentation links, four live-docs harness tests, ADR number
uniqueness, runbook SQL, text encoding, shell quoting and the gosec rule baseline.
Earlier race attempts intermittently timed out in the existing
`TestManagerKeepsIdleConnectionAcrossPongs`; its original-source baseline passed
ten runs and the final full race run passed. No data race was reported.

## Second slice: session-specific retained channels

ADR-1332 hands off the verified JWT session and expiry to retained WebSocket and
SSE authorization. The Auth template now scopes its existing SQL authorization
to that exact session, and retained WebSockets refresh their JWT decision and
enforce expiry even while idle. The first slice is preserved in local commit
`77bd4cc6c`; the second slice is reviewed and verified separately.

Existing Auth apps require a template redeploy after the daemon upgrade and
client reconnection. Live Auth database/cross-node qualification and
direct-message/inbox session authorization remain open. No capability gate has
been promoted.

The Auth suite also exposed permissive private-key import under Node 24. Signing
key validation now checks the P-256 parameter widths and derives the public point
to reject invalid scalars and inconsistent public/private key pairs.

Local verification passed: the full `pkg/realtime` and `cmd/realtimed` suites,
the full realtime suite under `-race`, all 55 Auth template tests, changed-line
lint for both Go packages, documentation links and the repository policy checks.
The signing-key validator fix is independently committed as `9b5184376`.

## Release boundaries

The Data API now has a [local load and recovery verifier](../../tests/data-api/load/README.md)
which combines multi-user REST/RPC traffic, RLS, admission/rate pressure,
deadlines, disconnects, response limits, lost replies and engine/gateway restart.
Its bounded, redacted reports identify exact sources and retain client latency,
rejection, throughput, sampled connection and recovery observations. Real failure
and SIGTERM checks verify fixture cleanup. Acceptance CI is configured to retain
reports. Local completion does not establish production capacity or supervision;
the hosted adapter is now implemented with seven bounded public-edge scenarios,
explicit unsupported fault coverage and the parent's resource cleanup journal.
Its contracts and local SQL behavior are verified separately from live execution.
Read-only connection-budget reports now aggregate shared bindings/scopes,
resident/parked runtimes, lifecycle overlap and explicit planning headroom, and
appear in `data-api health`. An opt-in operator
[capacity observer](../runbooks/DataAPIConnectionCapacity.md) now combines
qualified allocation receipts with bounded direct SQL ceilings and persists
generation-bound evidence for at most 30 seconds. Budget reads remain cache-only;
missing or stale evidence leaves capacity unknown. Opt-in authenticated
[resident pool reports](../runbooks/DataAPIRuntimeConnections.md) now separate
configured connections from missing resident and parked observations. Complete
runtime/external-consumer coverage remains unverified, so capacity evidence alone does not make the
overall budget known.
Private engine headers now supply request-correlated combined transaction
timings in logs, CLI diagnostics and health, with unknown/absent coverage kept
separate ([ADR-1662](../adr/1662-data-api-database-timing-telemetry.md)).
Provider/native and live hosted qualification, isolated SQL/pool timing,
automated allocation sources and aggregate budget enforcement remain open.

Local fixtures and portable CI cannot substitute for live provider and native
acceptance. Reuse the existing managed PostgreSQL qualification tooling and Data
API staging harness. Record exact source/configuration identities, expiry and
cleanup evidence before promoting a capability. Do not infer readiness from a
healthy metadata read or an installed feature flag.

Public package publication, production provisioning and restoration cutover are
separate changes. Complete their reviewable implementation and required evidence
before changing their existing release gates.

## Third slice: restore activation preflight

[ADR-1333](../adr/1333-managed-postgres-activation-preflight.md) adds durable
preflight requests to staged restore cutovers. POST
`/v1/postgres/cutovers/{id}/preflight` queues inspection and GET returns findings.
The existing recovery worker evaluates pending requests after restart, including
when provisioning is disabled. Each completed retry gets a new revision.

A consistent catalog snapshot checks source and target identity/generation,
restore lineage, target binding absence, complete affected source bindings and
fresh staged SQL verification. Reports carry expiry and cutover timestamps;
GET recomputes staleness. Source binding IDs are returned without secret material.

Every report keeps activation disabled and identifies missing runtime safety,
source-session isolation, routing, snapshot invalidation and explicit resume
evidence. Preflight neither installs a fence nor publishes credentials. A later
slice must join the independent runtime authorities and implement the qualified
publication transaction, restore-point write-loss policy and controlled resume.

Local verification passed: the full portable managed PostgreSQL suite, focused
PostgreSQL preflight and migration rollback regressions, authenticated cutover
and preflight HTTP routes, preflight/domain view race checks, the Node SDK build,
four Python SDK contracts, SQLC regeneration, SDK route coverage, OpenAPI parity,
changed-line lint (zero issues), documentation links and repository policy checks.
The Go API client and generated
Node/Python clients expose the new endpoints; SDK publication remains gated.

The full PostgreSQL integration run reached its twenty-minute limit while cloning
an isolated database for `TestPostgresUsageFreshDatabaseDoesNotHideUnmeteredDatabase`.
No assertion failure preceded that timeout. Focused restore tests passed against
PostgreSQL, including failed report-write recovery and cancellation. Live provider
and native KVM qualification remain required before enabling customer activation.

## Fourth slice: preflight runtime drain reporting

Preflight now joins the existing scheduler drain plan and native retirement
receipts through the shared receipt evaluator. Reports include safe per-workload
findings and counts across captured deployments, scopes and modes. Missing fences,
missing plans, unsupported members and unconfirmed native retirement remain
distinct findings. Preflight reserves no drain work and makes no native calls.

One catalog snapshot covers the owner epoch, admission fence, immutable inventory
and receipts. GET compares current evidence with a private digest and marks saved
reports stale after authority or receipt changes. Retry creates a new observation;
restart recovery preserves pending requests and saved findings. Old reports without
runtime observations require a retry. Catalog retirement keeps all independent
writer-isolation and activation gates closed. Source SQL-session fencing is the
next independent integration; routing, snapshot invalidation and controlled
publication/resume remain open.

Local verification passed: focused PostgreSQL preflight recovery, snapshot,
ownership ABA, receipt progress, account isolation and migration rollback tests;
the existing PostgreSQL drain-plan regression suite; both portable managed
PostgreSQL/domain suites; the Node SDK build and Python preflight contract;
SQLC regeneration, OpenAPI parity, documentation links and repository policy
checks. Changed-line production and focused domain test lint reported zero issues.
These fixtures do not qualify native VM retirement or live provider
activation, and no release gate has been promoted.

## Fifth slice: durable source SQL connection fences

[ADR-1334](../adr/1334-managed-postgres-cutover-source-fences.md) joins the existing
provider connection-fence controller to cutover recovery. Explicit internal intent
requires retired native inventory, fresh credential verification, exact source
identity and exclusive source bindings. It reserves private durable ownership,
maintenance phases and database selection before dispatch. Clone and cutover
holds exclude one another for the same dataset.

Restart and lost replies recover the same operation. Cancellation fences stale
workers and requires an independently observed remote terminal tombstone after
possible close dispatch; never-dispatched work can release locally. Source pins
and admission remain until cleanup completes, including with provisioning closed.
Preflight adds safe historical SQL findings, evidence-change detection and expiry
without provider I/O. Restore lineage uses dataset identity when it differs from
the compute endpoint.

Selected-database drainage does not establish background-writer coverage or
catalog stability. Customer activation remains disabled. Complete isolation,
route convergence, snapshot invalidation, publication/resume and live/native
qualification remain open.

Local verification passed across focused PostgreSQL runs: lost close/release
replies, failed receipt writes, restart recovery, ownership ABA, cancellation
against stale workers, expired-verification backoff, concurrent clone exclusion,
private schema classification and migration rollback. The existing admission,
drain-plan and preflight regressions also passed across those runs. The full
portable managed PostgreSQL/domain suites, real SQL connection-fence contracts,
selection contracts, Node build, Python preflight contract, SQLC regeneration,
OpenAPI parity, SDK route coverage, documentation and repository policy checks
passed. A one-second fake-provider deadline failed under combined validation
load; the affected case passed again after using a 30-second fixture deadline.
Changed-line production and focused test lint reported zero issues.

## Sixth slice: source writer inventory and isolation evidence

[ADR-1335](../adr/1335-managed-postgres-source-writer-isolation.md) adds a complete
bounded native catalog fingerprint and client/background/replication/unknown
backend counts to SQL closure observations. Hidden backend activity remains
unknown. The first complete closed catalog is retained separately from mutable
observations; detected drift is permanently latched, including catalog changes
which later revert. Older workers cannot erase the dedicated baseline columns.

An optional read-only provider contract requires durable guards installed during
the original close dispatch and released by authenticated abandonment with a
permanent delayed-request tombstone. Receipts bind placement, generation,
maintenance ownership, closure time and the original catalog. Evidence expires
within five minutes; guard ownership does not expire with a receipt or lease.
Missing, mismatched, stale or failed evidence cannot yield positive isolation.

Preflight exposes safe historical writer findings and bounds report expiry by
provider evidence. GET detects evidence changes, and cancellation can complete
despite drift or failed evidence refresh. Neon has no qualified full guard and
remains unsupported for this contract. SQL observations alone do not promote it.
All activation gates remain closed. Live/native qualification, complete runtime
coverage, route convergence, snapshot invalidation and publication/resume remain
open.

Local verification passed: the full portable managed PostgreSQL/domain suites;
focused PostgreSQL source-fence, writer-evidence, preflight and migration rollback
regressions; real SQL catalog/settings drift, hidden-session visibility,
reconnect, prepared-transaction and abandonment contracts; the Node build and
Python preflight round trip; SQLC regeneration, OpenAPI parity, SDK route coverage,
documentation and repository policy checks. Production and focused test lint
reported zero issues. No deployed-provider or native qualification was claimed.

## Data API: bounded HTTP admission

The Data API runtime now admits two active authenticated requests (one with
coordinated refresh), retains at most eight FIFO waiters and expires queued work
after one second. Saturation returns `503 data_api_overloaded` and `Retry-After: 1`
before JWT verification or PostgREST dispatch. Queued cancellation removes work;
disconnect during verification retains its active slot until verification settles.
Response headers and partial streaming do not release a slot. Health and CORS
bypass admission; PostgREST readiness still reports an exhausted SQL pool as unready.

Safe request records and CLI diagnostics add admission coverage, wait percentiles,
observed active/queued peaks, full queues, expired waits and queued cancellations.
Older records remain readable. These samples do not measure SQL pool wait or
establish a global connection budget. SDK classification recognizes overload
codes without adding mutation retries. Existing applications need the updated
runtime; hosted load qualification remains open.

Verification covers bounded bursts and FIFO promotion, health/CORS access,
streaming, cancelled verification, expired/cancelled queued writes, refresh and
authentication rechecks, real PostgreSQL locks and PostgREST recovery with RLS,
and the existing runtime, client, CLI and staging-harness contracts.

## Data API: liveness and failure observations

The public `/livez` endpoint checks gateway responsiveness without SQL or JWT
work. `/healthz` preserves SQL readiness and reports independent engine TCP,
SQL, database-reachability evidence, refresh and admission observations. Failed
SQL readiness leaves database connectivity unknown, including during pool
saturation. Probes share bounded in-flight work using the existing engine pool
and reserved refresh reader. Process supervision and deployment health paths
remain unchanged.

`data-api health` combines current probes with safe recent retained request
diagnostics. Current findings and historical failures remain distinct; missing
runtime support and incomplete logs remain partial coverage. The report does not
claim fleet-wide health, durable metrics, global connection limits or automatic
database-outage diagnosis. Existing apps need the updated runtime. Hosted load
and native/provider qualification remain pending.

## Data API: total admitted request deadlines

The existing 15-second query budget now also limits total admitted elapsed time,
including authentication, refresh, signing, engine waits and response streaming.
Queue wait keeps its independent bound. Expiry prevents late dispatch and aborts
engine HTTP work; pending verification/control work remains counted until it
settles. Pre-header requests receive 504; incomplete streams close with their
original sent status. No automatic mutation retry or SQL rollback claim is added.
Safe deadline phases flow into retained-log diagnostics and SDK classification.
Provider/native and deployed qualification remain independent release work.

## Data API: request phase timings and slow shape ranking

Admitted request logs measure verification, refresh, schema readiness, signing,
engine HTTP response wait and streaming. Diagnostics expose per-phase sample
coverage, completed p50/p95/p99 and separate interrupted elapsed lower bounds.
Old logs and bypassed requests remain explicitly without timings. Query shapes
can be ranked by total gateway p95 or failure count without logging SQL or values.
These intervals do not isolate PostgreSQL execution or pool wait; SQL/pool
instrumentation, automated capacity qualification, complete consumer coverage,
aggregate budget enforcement and deployed load qualification remain open.

Engine transaction telemetry now supplements these gateway intervals. Versioned
request-correlated logs accept the pinned engine's five fixed timing phases and
report combined pool acquisition/session/SQL time. Diagnostics and health keep
observed, unknown and absent coverage separate and preserve completed engine
samples after downstream cancellation. Error responses, deadlines and engine
restarts cannot inherit old measurements. No additional SQL pool or probe is
created. SQL execution and pool wait remain individually unknown; see ADR-1662.

Local checks cover distinct verification/refresh/signing/engine/streaming delays,
cancelled and timed-out phases, late-callback stability, legacy/malformed logs,
redaction and compiled CLI ranking. Real PostgreSQL fixtures verify lock-timeout
timings, queued-write expiry/cancellation and RLS-preserving recovery. These
results do not establish hosted fleet coverage or a production latency guarantee.

## Data API: engine pool observations

Bounded private engine metrics reads add timestamped capacity, existing idle
connections, pending acquisitions and cumulative acquisition timeouts to runtime
health and the CLI. Metrics use the existing health time/size bounds, share only
in-flight reads and retain no stale counters. The pinned handler performs SQL
checks before metrics, reusing the engine pool; saturation can make observations
unknown. Pending work stays shared beyond the caller deadline. Unknown metrics
leave deployment readiness unchanged; the CLI preserves core readiness and marks
coverage partial. Pending acquisitions are current observations, while cumulative
timeouts and counter resets are separate from current/recent failures. Lazy-pool
availability cannot be subtracted from capacity to infer usage. Readiness probes
can themselves be waiters. Per-database budgets, SQL/pool-wait durations and hosted
load qualification remain open.

Local runtime checks cover strict metric parsing, redaction, shared reads after
caller timeout, the hard HTTP cap and recovery. Real PostgreSQL fixtures verify
unknown coverage under full saturation, observed timeout counters after recovery,
engine failure/restart and counter reset, stable connection bounds and RLS.
Compiled CLI checks exercise trusted TLS, public-probe credential isolation,
text/JSON reports, unsupported coverage, waiters and counter resets. These local
results do not establish hosted fleet or load qualification.

## Data API: response byte limits

Normal REST/RPC output now has a canonical 16 MiB body cap, enforced while
streaming with backpressure. Oversized declared lengths return 502 before
headers; unknown/chunked overflow closes the incomplete stream. Exact-cap bodies
succeed. HEAD/bodyless representation sizes are excluded, and the request/schema
and row caps remain independent. Logs retain only fixed source and byte-bound
metadata; diagnostics count aborted 200 streams and health separates recent
limit failures from current readiness. SDK classification advises reconciliation,
including when a committed mutation's output is rejected. No mutation retry or
engine-allocation bound is introduced.

Local runtime and SDK checks cover byte boundaries, pre-header/chunked overflow,
backpressure, interrupted timing, redaction and retry counts. A pinned-engine
PostgreSQL fixture verifies large Unicode JSON, a committed oversized RPC result,
request-ID correlation and RLS-preserving recovery. Hosted load and native/provider
qualification remain pending.

## Data API: optional per-user request rates

Owner environment settings now enable per-subject token buckets shared across
authenticated routes and JWT rotation. Rejections return 429 before data or
refresh/readiness SQL dispatch. Rates/bursts are bounded and configuration is
strict; both settings absent leaves the feature disabled. A fixed 4,096-subject
tracking bound keeps memory finite. Fully refilled buckets expire through lazy
bounded sweeps; new subjects receive a tracking-capacity rejection if no slot is
available, while depleted counters are retained. Logs exclude identity data,
diagnostics separate limits from overload, and SDK recovery advises waiting
without automatic replay. Health keeps these failures historical.

Local checks cover signed-token rotation, independent users/runtimes, Unicode
subject identity, refill boundaries, tracking saturation/expiry, redaction and
SDK retry counts. A real PostgreSQL fixture checks rejected REST/RPC writes,
including a nontransactional invocation counter, then RLS-preserving recovery.
This adds no distributed quota, per-user admission fairness or SQL-cost budget;
automated capacity qualification, complete consumer coverage, aggregate
connection-budget enforcement and hosted load qualification remain open.

## Data API: optional per-user concurrent requests

`DATA_API_USER_CONCURRENCY` now caps each verified user's concurrent authenticated
requests within one runtime. JWT rotation shares slots; global admission and rate
policies retain their bounds. Rejections precede refresh/readiness/engine SQL and
return a distinct 429 code with safe counts and advisory retry time. A slot covers
both handler and downstream work. Dispatched work with no completion acknowledgement
enters a 15-second cooldown after both settle, and pending handlers cannot be
evicted. Tracking is bounded at 4,096 subject digests, with no new SQL pool or probe.

SDK recovery and CLI diagnostics distinguish concurrency limits from request rates
and overload, and health treats retained rejections as historical. Tests exercise
real SQL locks, isolated users, rejected invocation counters, deadlines, cancelled
work, recovery, tracking pressure and redaction. This protects local HTTP admission;
SQL completion, fair queuing across identities, coordination across runtimes and
aggregate connection-budget enforcement remain separate. See
[ADR-1663](../adr/1663-data-api-user-concurrency-limits.md).

## Data API: database lock contention

New Data API SQL logins default to a five-second lock timeout alongside the
15-second statement timeout; local development matches. Credential reconciliation
upgrades only the exact legacy role policy and commits after ACL verification,
preserving credentials and refusing drift. Existing sessions need reconnecting;
cached credentials and a runtime rebuild alone do not upgrade the database login.
Disposable qualification checks effective defaults; RPC ACL tooling supports both
exact policies without changing them.

The SDK classifies original `55P03` lock conflicts and surfaced `40P01` deadlocks
with explicit wait guidance. It adds no mutation retries. Native PostgreSQL and
pinned PostgREST tests cover blocked REST/RPC rollback, independent user progress,
RLS, bounded pools and recovery. A real deadlock also confirms the pinned engine's
existing SQL-transaction retry behavior: two HTTP requests can succeed after one
victim is retried, with partial writes rolled back. Login defaults are per lock
acquisition and can be overridden by trusted SQL settings/functions; they do not
prove SQL cancellation or fleet-wide capacity. See
[ADR-1664](../adr/1664-data-api-lock-contention-policy.md).

## Data API: SQL deadlines across transaction retries

Optional `DATA_API_SQL_DEADLINE=1` passes one admission-time expiry into the pinned
engine's repeated SQL transaction attempts. Owner-installed, protected invoker
hooks validate the versioned internal claim, cap remaining statement/lock budgets
and compose around the existing refresh barrier. Startup and the read-only check
command verify the exact hook contract; no request probe or new pool is added.
SDK timeout guidance preserves explicit receipt reconciliation without replay.

New and rotated roles require setup/grants before adoption. Stable clocks,
unmodified trusted hooks/functions and normal timeout behavior are assumptions;
pool acquisition, arbitrary owner SQL and external effects stay outside the bound.
Native checks cover injected repeated serialization/deadlock faults, HTTP
cancellation, rollback, RLS, independent user progress, recovery and composed
refresh waits. See [ADR-1665](../adr/1665-data-api-sql-retry-deadlines.md).

## Data API: deadline grants during credential rotation

The stable managed schema owner can now explicitly enroll a Neon `data_api`
binding for automatic deadline-hook grants across credential generations. The
provider checks the private binding/resource approval and exact protected hooks,
then grants only the two deadline functions inside the candidate transaction,
before publishing a replacement secret. Drift rolls back preparation; existing
credentials remain available and owner repair can resume the same generation.
Rolling runtime refresh and previous-login retirement remain unchanged.

Real PostgreSQL checks cover active RLS traffic, both user scopes, private-table
and unapproved-RPC denial, drift rollback, replay, owner repair and retirement.
The actual owner command is tested for explicit enrollment, read-only checking
and removal. Other bindings retain manual grants. Databases with coordinated SQL
refresh gates use the shared-gate enrollment below to preserve pause/fingerprint
state across both serving generations. See
[ADR-1666](../adr/1666-data-api-deadline-grant-rotation.md).

## Data API: coordinated refresh during credential rotation

Owner-approved managed Neon `api` bindings can now share pause/resume protection
across credential generations. Per-login fingerprints preserve permission-filtered
contracts while old and replacement runtimes overlap. Enrollment drains legacy
admissions and removes the old gate row so queued legacy hook calls fail closed.
Existing owner commands route enrolled logins to the shared gate, and resume
verifies every member through role-filtered catalog inspection without assuming
serving roles.

Before publication, preparation validates pinned functions, private-table metadata,
source identity and a catalog/privilege witness, then grants only the approved
RPCs and fixed refresh/deadline functions. Drift rolls back the candidate; owner
verification permits retry. The existing rollout/retirement lifecycle removes
only the retired login's mapping and retains the shared pause. Native checks
exercise two runtimes, RLS, enrollment/pause races, stale fingerprints and failed
preparation recovery. See [ADR-1667](../adr/1667-data-api-coordinated-refresh-rotation.md).

## Data API: versioned replay expiry

The starter now has opt-in v2 note RPCs using UUIDv7 timestamps and a fixed
24-hour replay deadline enforced by PostgreSQL. Expired writes are rejected even
after receipt cleanup. Cleanup accepts `--contract v2`, removes only expired
snapshots and skips active operation locks. Live receipt deletion and snapshot
edits are guarded; v1 remains compatible with its existing retention behavior.

The SDK generates v2 keys and reports typed expiry as an unresolved operation.
Read-only lookup can still confirm a retained expired receipt. The browser
explicitly uses v2 and keeps the original pending operation on expiry. This is a
local implementation; owner migration, RPC grants, schema refresh and paired
client adoption are required for rollout. See
[ADR-1670](../adr/1670-data-api-versioned-replay-expiry.md).

## Data API: recoverable conditional updates and deletes

Opt-in v2 note RPCs now combine expected-version checks with immutable success
receipts. Update and delete share a subject/key namespace and check saved receipts
before current row versions. Identical replay returns the original post-update
or deleted snapshot; changed requests conflict. Read-only lookups can recover
committed results after lost responses and later edits/deletion. The SDK adds
typed classification without automatic retries or key/version replacement.

The existing v2 expiry contract also applies to these operations. Owner cleanup
selects `--contract v2 --receipt-kind mutation`, preserves live receipts and skips
active locks. Ordinary REST writes retain their existing semantics. This is a
local implementation requiring owner migration, approved RPC grants, schema
refresh and paired-client adoption. See
[ADR-1671](../adr/1671-data-api-versioned-update-delete-receipts.md).

## Data API: recoverable atomic note batches

The starter now provides an explicit v2 RPC for 1–100 distinct note updates and
deletes in one transaction. It locks IDs in stable order, requires every expected
version, and rolls back all mutations/cascades if any item or receipt fails.
One bounded ordered envelope can be replayed or recovered read-only after later
changes. Client validation checks every snapshot before acknowledgement; SDK
type-guard overloads preserve that validated result type.

The existing v2 expiry and owner controls apply, with a separate batch receipt
namespace and cleanup selector `--contract v2 --receipt-kind batch`. This local
implementation requires migration, owner RPC grants, runtime refresh, generated
types and paired-client adoption. See
[ADR-1672](../adr/1672-data-api-atomic-note-batches.md).

## Data API: v2 business transaction generation

`data-api transaction init --contract v2` now emits a separate order/inventory
package with fixed UUIDv7 replay deadlines, immutable original snapshots,
read-only recovery, typed context-bound client helpers and guarded owner cleanup.
The writer commits business changes and its receipt atomically. Matching replay
recovers original prices/results after later changes, while expired keys remain
refused after purge. Default/explicit v1 generation retains its current contract.

Generated packages remain local review artifacts. Adoption requires owner
migration, approved RPC grants, runtime refresh, schema validators and the paired
SDK. Business authorization and external effects remain application decisions.
See [ADR-1673](../adr/1673-data-api-v2-transaction-generator.md).

## Data API: durable client recovery

The SDK now offers opt-in bounded pending-operation journals and an IndexedDB
adapter with atomic cross-tab claims. Prepared operations retain their original
API/identity/contract/key/payload through reloads. Dispatch persists uncertainty
before its first request; recovery explicitly reads receipts. Unresolved and
expired results remain stored, and local marker failures preserve proven server
receipts. Strict restore validation rejects corruption, context changes and
lossy parsers without replacing work.

The v2 transaction generator exposes `persistentTransactionClient` using this
protocol. Applications explicitly choose storage and stable slots; v1 and SQL
contracts remain unchanged. See
[ADR-1674](../adr/1674-data-api-durable-mutation-journal.md).

## Data API: receipt storage diagnostics

The note starter and generated v2 transaction packages now offer owner-only
read-only JSON reports of active/expired receipt counts, oldest expired deadline
and backlog age, and physical table/index storage. Exact ownership checks and
bounded queries keep application sessions from cross-user aggregates. Missing,
unsupported and timed-out tables remain explicitly unavailable with a nonzero
exit; successful contract measurements are preserved. The reporter shares one
implementation across both packages and leaves cleanup explicit. See
[ADR-1675](../adr/1675-data-api-receipt-storage-diagnostics.md).

## Data API: incremental note change feed

The starter now captures note insert/update/delete metadata atomically with
writes. Per-subject transactional counters preserve commit ordering and roll
back with failed work; a bounded retained suffix and durable watermark make
expired cursors explicitly require resync. Read-only RLS-protected checkpoint
and polling RPCs support an initial row scan followed by invalidation catch-up.
Typed helpers validate opaque cursors, ordered events and continuation before
applications persist progress. The feed stores no historical note bodies and
keeps client polling/resync explicit. See
[ADR-1676](../adr/1676-data-api-incremental-note-change-feed.md).

## Data API: ranked note search

The starter now provides English AND-word search with relevance ranking and
query/subject-bound keyset cursors. A subject/lexeme posting index remains
indexable under serving-role RLS; every candidate is rechecked against current
note visibility. Atomic trigger maintenance and migration backfill cover normal
writes and existing rows. Strict client parsers, bounded inputs, native isolation
checks and serving-role query-plan verification cover the contract. See
[ADR-1677](../adr/1677-data-api-ranked-note-search.md).

## Data API: exact team order summaries

The teams starter now offers a read-only business-data aggregate over all
matching visible orders. Current membership and order RLS protect overall and
per-status counts and whole-cent totals. Decimal strings preserve large values;
the paired client validates context, filters and bucket consistency. Native
acceptance covers revoked memberships, rows beyond page caps, read-only calls,
5,000,001 synthetic orders with an odd total above 2^53, and statement cancellation.
See [ADR-1678](../adr/1678-data-api-team-order-aggregation.md).
