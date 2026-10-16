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
| P1 | Establish production data capacity and health contracts | Aggregate connection budgets, pool/query timing, controlled SQL probes and concurrent app/database wake load tests | Data API admission, health, gateway timings, pool counts, response limits, per-user rates, a local load/recovery verifier and read-only connection-budget estimates implemented; verified capacity, budget enforcement, SQL/pool-wait timing and deployed load qualification pending |
| P1 | Release supported client packages | Owner-approved SDK licensing, immutable versioned releases, provenance and tested CLI/runtime compatibility | Pending licensing and registry decisions |
| P1 | Reconcile complete provider spend | Provider allowances and branch charges, delayed corrections, deletion settlement and qualified budget cutoffs | Pending provider/exporter qualification |
| P2 | Reconcile capability discovery, docs and onboarding | Dedicated maturity entries, consistent customer guides and measured first verified transaction | Pending |
| P2 | Expand provider and change-feed coverage | Qualified operation matrix, orphan reconciliation, reviewed feed payload/latency limits and customer-driven backfill/partition support | Pending |

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
appear in `data-api health`. Current adapters lack verified usable capacity and
runtime/external-consumer coverage, so the estimates remain explicitly unknown.
Provider/native and live hosted qualification, SQL/pool timing, a qualified
capacity observer and aggregate budget enforcement remain open.

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
instrumentation, verified capacity, aggregate budget enforcement and deployed
load qualification remain open.

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
verified capacity, aggregate connection-budget enforcement and hosted load
qualification remain open.
