# Data API release review

This change completes the current core Data API development slice. It does not
publish artifacts or establish hosted staging acceptance.

## Public reads, server keys, aggregates, single-app projects and Python/Go clients

[ADR 20261018170742](adr/20261018170742-data-api-access-modes-and-clients.md)
closes five product gaps, all opt-in:

- **Public reads.** `--public-read` lists relations that anonymous `GET`/`HEAD`
  requests may read. Startup requires RLS tables or security-invoker views.
  Embeddings must also be listed, and every anonymous client is rate limited.
- **Public RPCs.** `--public-rpc` lists approved `STABLE` or `IMMUTABLE`
  functions that anonymous callers may call by `GET`, which PostgREST runs
  read-only. An anonymous `POST` returns `405 public_rpc_requires_get`.
  Embeddings must be publicly readable, and the shared anonymous rate limit
  and edge cache apply.
- **Anonymous SDK clients.** `createDataClient({ url, anonymous: true })` sends
  no `Authorization` header when there is no token, so `@gregale/data` serves
  public reads and `rpc(name, args, { get: true })` public RPCs with generated
  types. Without the option, a missing token still throws.
- **Usage and monthly caps.** `data-api usage` shows requests by access kind,
  rejections, errors, rows and bytes per hour or day. `configure
  --monthly-caps requests=N,anonymous=N` refuses requests past an owner cap
  with `429 data_api_monthly_cap_reached` until the next UTC month. The runtime
  reports under workload identity, and report ids keep snapshot restores from
  double counting.
- **Python and Go client parity.** Generated clients opt in to anonymous
  access explicitly (`anonymous=True`, `Anonymous: true`), call public RPCs
  with GET (`get=True`, `WithGET()`) and page with keysets (`page`/`Page`,
  `cursor_from`/`CursorFrom`). Go updates set columns to NULL with `SetNull`
  and decode embedded relations with `GetInto`. Aggregate selects keep their
  types in the TypeScript SDK.
- **Edge-cached public reads.** `configure --public-read-cache 30s` lets the
  gateway cache serve canonical anonymous reads. Responses are tagged per
  relation so `gregale cache purge --tag data-api:api.products` invalidates
  precisely. Every other response stays `no-store`.
- **Server keys.** `data-api server-key create|list|revoke` manages two
  hash-only rotation slots for trusted backends. RLS still applies, keys are
  refused when sent with an `Origin` header, and `X-Gregale-Subject` acts for
  one user.
- **Scoped server keys.** `server-key create --read-only --relations ...
  --rpcs ... --no-subjects` restricts a key. Anything outside its permissions
  returns `403 server_key_permission_denied` before SQL, and RLS still applies.
- **Aggregates.** `--aggregates` enables PostgREST aggregates and requires the
  SQL deadline, so expensive queries are cancelled inside PostgreSQL.
- **Single-app projects.** `--migrations` and `data-api deploy` run migrations
  as the Data API app's own release task, using a release-only
  `MIGRATION_DATABASE_URL`.
- **Python and Go clients.** `data-api types --language python|go` renders
  standard-library clients from the fingerprinted contract, offline with
  `--snapshot-file`.

- **Live reads.** `gregale data-api live enable` connects a Data API to
  realtime. `@gregale/data/live` then keeps a read current for signed-in
  users, visitors on public relations and server keys, with RLS deciding each
  row. Live reads verify tokens exactly as the Data API does. Accounts get
  them through the `live_queries` platform flag.
- **File storage.** `gregale data-api storage enable` binds a bucket and turns
  storage on. Files stream through the Data API, and RLS policies on
  `storage_objects` decide every upload, download and removal, so file lists
  are ordinary queries and live reads. `@gregale/data` adds
  `createStorageClient`.

Every request now carries a gateway-owned `gregale_access` claim (`user`,
`anonymous` or `server`) for policies. Logs and `data-api diagnostics` report
access kinds and the server-key slot, never subjects, addresses or keys.
Runtime tests cover anonymous, server-key and claim-spoofing paths. Clientgen
tests type-check the generated Go and run both clients end to end against a
fake API. Hosted acceptance remains open, as for the rest of the Data API.

## Database connection-budget preflight

New read-only management endpoints report a database-wide canonical connection
model and the shared databases used by an app's Data API bindings. `data-api
health` includes those reports alongside SQL readiness and engine observations.
The model covers resident/waking and parked instances, observed deployment
overlap, credential rotation, Auth pools and explicit control/migration headroom.
Duplicate bindings do not duplicate physical instance counts or modeled pools.

Reports distinguish `within_budget`, `over_budget` and `unknown`; the first two
require complete verified evidence for the exact database generation/placement.
Current provider metadata supplies no verified usable-capacity allocation, and
the catalog cannot attest deployed pool configurations or external clients, so
current reports remain `unknown` with estimates and coverage reasons. An unknown
budget makes CLI health partial without changing valid SQL readiness. This slice
does not reserve connections, change quotas, wake SQL or enforce admission.

## Concurrent load and recovery verification

The manual staging canary now includes a hosted adapter with seven bounded
REST/RPC, RLS/tenant, pressure, lost-reply, managed-restart and park/wake scenarios.
It uses the shared runner and installed SDK, verifies exact write receipts and
records source/deployment identities with private redacted measurements.
Unsupported private faults and SQL instrumentation remain explicit. Client
cleanup is separate from the parent's resource journal. Portable contracts and
real local SQL verification do not establish hosted acceptance; running the live
controls still requires the configured isolated staging target and existing
provider/native qualification. See the [hosted guide](../tests/data-api/staging/README.md#hosted-load-and-recovery).

`make data-api-load-verify` runs nine bounded scenarios against disposable local
PostgreSQL 16 and checksum-pinned PostgREST 14.3. Concurrent signed-user REST/RPC
traffic checks RLS alongside admission/rate pressure, deadlines, disconnects,
oversized responses, lost replies and an embedded engine/gateway restart. Uncertain
writes settle and reconcile receipts before any explicit same-key replay.

Acceptance CI retains a redacted, private JSON report with client latency
percentiles, success/rejection counts, throughput, sampled serving connections,
recovery time and source identities. Failure and SIGTERM checks verify owned
resource cleanup. These local observations do not qualify hosted capacity or
production supervision. See the [runner guide](../tests/data-api/load/README.md).

## Optional per-user request rates

Updated runtimes support `DATA_API_RATE_LIMIT_PER_SECOND` and
`DATA_API_RATE_LIMIT_BURST`. Either enables per-subject token buckets with omitted
defaults of 10 requests/second and burst 20; both accept integers from 1 to 1,000.
Remove both to disable. Policies take effect after a fresh runtime start.
Verified subjects share credit across tokens and authenticated routes. Rejections
return `429 data_api_rate_limited` and `Retry-After: 1` before SQL dispatch.
Generated OpenAPI describes that response and header.
Health and CORS bypass this policy; global admission remains separate.

Tracking is capped at 4,096 hashed subjects. Only fully refilled entries expire;
tracking pressure rejects new subjects without resetting depleted counters.
Safe diagnostics retain policy/reason metadata, classify rate limits separately
from overload, and keep recent findings separate from current health. The SDK
adds `rate_limited` / `wait` classification without automatic replay. Generic
429 does not establish rollback. Local checks cover signed JWT rotation, user
isolation, capacity pressure, expiry, rejected SQL writes and recovery. Budgets
are per runtime, not shared quotas or per-user admission fairness. Cold process
starts reset them; snapshots may retain state. Hosted load qualification is open.

## REST/RPC response byte limits

The gateway now streams normal response bodies with a 16 MiB byte cap and
downstream backpressure. Known oversized lengths return `502 response_too_large`
before headers; overflow after headers closes the incomplete stream, potentially
leaving a sent 200 status. Exactly 16 MiB is allowed; HEAD/bodyless representation
lengths are excluded. The existing request/schema and row bounds remain separate.

Safe logs attach fixed response-limit metadata, CLI diagnostics count failures
including aborted 200 streams, and health distinguishes recent limits from
current readiness. The SDK recognizes the new code with `reconcile` guidance;
interrupted bodies can instead surface as `no_response`. Writes can commit
before rejection, so reconciliation must precede replay. No mutation retries
are added. Local checks cover byte boundaries, chunked/error/RPC responses,
backpressure, redaction, real PostgreSQL commits, RLS and recovery. Update deployed
runtimes and the client SDK; hosted qualification remains pending.

## Engine connection-pool observations

`gregale data-api health NAME` now includes timestamped engine pool capacity,
existing idle connections, pending acquisitions and cumulative acquisition
timeouts. Two-second callers share bounded private reads with an 8 KiB response
cap and a 15-second hard HTTP timeout. The pinned handler performs SQL checks
before metrics, so saturation may produce unknown coverage; reads reuse the
engine pool and retain pending work after caller timeout. Pool waiters are a current finding;
cumulative timeout counts/reset do not imply recent or current failure. Available
connections in the lazy pool cannot be subtracted from capacity to derive usage.
Metrics failures preserve SQL readiness and the deployment HTTP status. The CLI
keeps valid readiness while flagging unknown, malformed or absent pool coverage
as partial. Existing runtimes need an update. Local verification is separate from
hosted qualification; the changes do not deploy or publish artifacts.

## Request phase timings and slow-query-shape diagnostics

Admitted request logs now separate verification, refresh, schema readiness,
signing, engine response wait and response streaming. CLI diagnostics report
phase coverage and completed p50/p95/p99, with interrupted elapsed lower bounds
kept separate in JSON. Legacy/bypassed requests retain explicit missing coverage.
With query-shape metadata enabled, `--sort p95` and `--sort errors` rank shapes
by total gateway-request p95 or failure count; request count remains the default.
Engine wait does not isolate SQL execution or pool wait. Existing apps need the
updated runtime; changes remain local and no deployment is performed here.

## Total admitted request deadlines

Authenticated Data API work now shares a total 15-second elapsed-time deadline
across verification, refresh, signing, upstream work and response streaming.
Continuous chunks cannot retain an active slot indefinitely. Expiry prevents late
dispatch, sends `504 request_deadline_exceeded` before headers or closes an
incomplete stream. Pending async verification/control work stays bounded until
settlement. Safe timeout-phase diagnostics distinguish where expiry was observed;
SDK errors retain timeout/reconciliation guidance without mutation retries.
SQL rollback is not inferred from HTTP cancellation. Existing apps and clients
need the updated runtime/SDK; no deployment or publication is performed here.

## Data API health observations

`gregale data-api health NAME` combines independent gateway liveness, engine TCP
reachability, SQL readiness, refresh and admission observations with recent safe
request diagnostics. `/livez` performs no SQL; `/healthz` remains the deployment
readiness check. Failed SQL readiness leaves database connectivity unknown, so
pool saturation does not become an invented database-outage diagnosis. Bounded
probes share in-flight work and reuse existing SQL pools. Partial logs and older
runtime support remain explicit; recent failures do not override recovered health.
Existing apps need the updated runtime; no deployment or hosted qualification is
performed by this local implementation.

## Bounded runtime admission

The Data API gateway now limits active work to its engine pool size (two, or one
with coordinated refresh), with eight FIFO waiters and a one-second queue
deadline. Full and expired queues return `503 data_api_overloaded` with
`Retry-After: 1` before PostgREST dispatch. Health/CORS remain outside this queue.
Cancelled queued writes cannot dispatch; disconnected verifiers retain their
slot until verification settles. Active timeout/disconnect outcomes still require
application reconciliation. No automatic write retry is added.

Safe request logs and CLI diagnostics distinguish admission waits, queue saturation
and queue timeouts from query timeouts and engine failures. Old log samples remain
readable with explicit admission coverage. SDK error classification recognizes
overload codes, including thrown errors without an HTTP status. Existing deployed
Data API applications must adopt the updated runtime to receive these bounds;
hosted acceptance remains a separate qualification step.

## Guided onboarding follow-up

`gregale data-api onboard --path DIR --bundle MANIFEST` composes starter creation,
verified SDK installation and disposable client/CRUD/RPC/isolation checks. It
reports stage durations, retains projects on failure and prints recovery commands.
The starter browser's port 3030 is now allowed by local dev CORS. Follow the
[first-application journey](data-api-onboarding.md) for schema changes and pilot
criteria. These are local checks; hosted qualification remains pending.

## Team orders follow-up

The embedded `data-api-teams` template adds a typed shared-team dashboard,
SQL membership roles, two-tenant isolation, filtered UUID pagination and
idempotent version-checked saves with immutable receipts. Onboarding accepts
`--template data-api-teams`. Required scenarios, client tests, live catalog
freshness/compatibility checks and Chromium recovery acceptance cover the
workflow; packaged qualification includes the shipped template. Initial owners and verified identity-provider IDs remain owner-provisioned;
current owners manage membership through approved RPCs. Receipt retention
remains owner-managed. Hosted qualification
and public SDK publication remain pending.

## Team administration follow-up

The team's append-only `0002_team_administration.sql` migration adds a trusted
user-ID registry, UUID membership revisions, owner-only member pages and changes,
atomic ownership transfer, last-owner protection and immutable audit receipts.
The dashboard supplies administration/history controls and exact-key recovery
that can confirm a transfer after its actor becomes an admin. Required local
verification now includes 100 request assertions. SQL/browser acceptance covers
concurrent role changes, owner loss, stale revisions, revocation and lost-response
recovery. Identity-provider account creation, email delivery and hosted qualification remain
outside this starter. Self-service invitations now register the verified accepting
subject; initial owners still use trusted bootstrap provisioning.

## Self-service invitations follow-up

The team's third migration adds expiring single-use member/admin invitation links,
verified-subject acceptance, owner lists/revocation and immutable invitation audit.
The browser retains random bearer secrets only in fragments and action memory;
the database stores hashes. Explicit recovery binds to the original authenticated
actor and cannot re-grant subsequently removed access. Required saved scenarios,
strict client checks and real PostgreSQL/PostgREST/Chromium acceptance cover the
flow. Links are bearer credentials, not email-bound invitations; hosted acceptance
and email delivery remain pending.

## Customer CSV workflow follow-up

Team orders now includes local CSV mapping, full validation and explicit approval,
create-only imports with duplicate skipping, atomic 20-row batches and persisted
actor-bound progress. History permits resume after reload; explicit recovery
replays the original cursor without processing another batch. Owners see team
history, admins their own, and members cannot import. Filtered CSV export respects
RLS and escapes spreadsheet formula prefixes. Required scenarios, strict client
checks and PostgreSQL/PostgREST/Chromium acceptance cover the workflow. General
schema import, background processing and hosted qualification remain future work.

## Reviewed order workflow follow-up

Ordinary creates are draft-only and only draft totals remain editable. A reviewed
version-checked RPC provides submit and fulfil actions, with immutable receipts,
atomic edit/transition history and explicit original-user recovery. Database
triggers prevent ordinary-save bypass and reject reverse/skipped transitions;
imports may retain historical initial states. The dashboard adds confirmed actions
and member-readable tenant-scoped history. This intentionally changes clients
that previously saved arbitrary statuses; they must adopt the action RPC. Local
scenario, SQL and browser checks cover concurrency, denied actions and lost
responses. Hosted qualification remains pending.

## Signed order webhook follow-up

Reviewed submit/fulfil events now atomically enqueue immutable delivery snapshots.
A runnable separate worker signs requests with HMAC, bounds HTTP calls, rejects
redirects and uses leased claims with finite retries and dead letters. Owner
controls show deliveries/attempts and replay checked generations, with read-only
reconciliation after a lost replay response. Worker claims are tenant-scoped
trusted JWT capabilities; signing keys and destinations remain in the worker
environment. Delivery is at least once and requires receiver deduplication.
Local tests include real HTTP signature verification, crash redelivery, stale
leases, retry exhaustion and browser replay. Scheduling, credentials, hosted
qualification and real destination configuration require operator setup.

## Tenant delivery controls follow-up

Owners can pause/resume new claims with settings revisions, inspect queue counts
and enqueue age, preview up to 20 eligible failures and atomically replay only the
reviewed generations. Active leases can finish while paused. Immutable operation
history records actor/action/time; exact-key recovery can confirm an operation
after its actor loses ownership. SQL/browser coverage includes stale batches,
revision conflicts, permissions and response loss. These are customer application
controls; worker scheduling, retention and hosted qualification remain operator
responsibilities.

## Included

- Generic local projects, project-owned authorization scenarios, and owner tooling.
- PostgreSQL composite types, view relationships, computed relationships, and
  relation-set RPC projections in generated contracts.
- Nullable JSON projections and application-owned runtime parsers.
- SDK declarations checked with strict library checking; the client factory keeps
  its declared PostgREST return type instead of expanding dependent generic defaults.
- Typed composite cursor pagination, adopted by the shipped notes starter.
- Local CI type drift checks without overwriting committed types.
- Structural compatibility checks with fingerprint-bound approvals for intentional breaks.
- Optional SDK/runtime contract guards that reject mismatches before data access,
  with serving-bound owner policies for structurally compatible older clients,
  local policy watching and pinned multi-client replay.
- Reproducible CLI/SDK bundles, packaged custom-project verification, and
  checksum-bound release-candidate reports covering mixed clients, RLS, rejection,
  revocation, stale policies and cleanup for both the starter and a tenant-scoped
  custom orders project.

## Upgrade

1. Install a matching CLI/SDK bundle using the verified artifact workflow in
   [Data API packaging](data-api-packaging.md). The SDK package version remains
   0.1.0; pin artifact integrity rather than relying on the version alone.
2. Regenerate application types. New snapshots use format version 3; readers
   accept earlier baselines. Review generated contract changes and compile the
   application before adopting the bundle.
3. Approve computed relationships explicitly using `@gregale:rpc`; executable
   unapproved computed relationships block inspection. Review permissions and
   refresh the serving contract after owner DDL or ACL changes.
4. Run `gregale data-api dev --port 0 --once --check-types --check` in local CI
   with Docker, Node 22+, client dependencies, and committed current types.
5. Upgrade the starter alongside the SDK: its cursor implementation now imports
   SDK helpers while retaining its existing cursor interface and validation.

`validateJson` uses synchronous application parsers. Cursor pagination requires
a unique, non-null final key and consistent filters/identity between requests;
it does not provide a snapshot across pages. The full guide covers these bounds.

## Comparison with Supabase's core Data API

| Capability | Gregale in this slice | Remaining gap or boundary |
| --- | --- | --- |
| Schema-generated REST and TypeScript | Supported, with CRUD, joins and approved RPCs | SDK is packaged through verified bundles; no public registry release |
| Local database/API development | Disposable migrations, watch reloads, inspector and authorization scenarios | Requires Docker; no hosted development acceptance in this environment |
| Query typing | Tables, views, composites, computed relationships and RPC projections | Restricted eligible relationships; arbitrary JSON shape stays application-owned |
| Runtime validation | Optional parser hook, including Zod-compatible parsers | Synchronous parsers only |
| Pagination | Typed keyset helper and native offset pagination | Unique keys and stable ordering are caller-owned |
| Schema evolution | Hosted/local type checks, structural compatibility reports and reviewed breaking-change approvals | Policies, function bodies, defaults and data-dependent behavior need application tests |
| Runtime contract pinning | Optional exact fingerprint guard with owner-approved compatible older clients | Requires an upgraded runtime; matches the captured serving contract, not live DDL |
| Hosted tooling/distribution | Owner tooling, contract refresh and artifact pin/restore | Public distribution and staging canary still need a configured destination |

GraphQL and Realtime remain outside the agreed core Data API scope. This is a
capability comparison, not a claim of complete Supabase/PostgREST parity.

## Validation

Run `scripts/test-data-api.sh --integration` with PostgreSQL, pinned PostgREST
14.3, Chromium and Docker, then `scripts/test-data-api-packaging.sh` sequentially.
The packaging gate rebuilds the committed source twice, compares checksums, and
verifies installation, restoration, generated starters, browser builds, replay
and contract rollouts, then writes a checksum-bound verification report.
A hosted canary remains unverified because no staging endpoint or credentials
are configured. GitHub CI results belong to the PR and must be checked separately.

The SDK's `cursorPage` now accepts a typed `before` cursor for previous-page
navigation. It reverses sort direction and null placement for each composite
ordering column, returns the nearest preceding rows in reverse traversal order,
and rejects simultaneous `after`/`before` cursors before changing the query.
PostgreSQL/PostgREST acceptance covers both directions, null partitions,
deleted cursor rows and RLS isolation. See the SDK README for display ordering.

The SDK adds `dataRequest(query, { timeoutMs, signal })`, preserving selected
result types and returning distinct `request_timeout` and `request_cancelled`
codes for client-side failures. Deadlines include token lookup and response-body
consumption, and automatic retries are disabled. Existing server errors remain
unchanged. HTTP cancellation does not attest mutation rollback or immediate SQL
cancellation; PostgreSQL's existing 15-second role statement timeout bounds
running work. Acceptance verifies connection recovery after concurrent aborted
RPCs.

`optimisticMutation` adds typed `applied`, `conflict` and `error` outcomes for
single-row conditional edits/deletes, without automatically replaying writes.
The concurrency example includes a database-managed version trigger, complete
primary-key/version predicates and tenant RLS. PostgreSQL/PostgREST acceptance
compiles the example against generated types and verifies competing updates,
update/delete races, stale versions, deleted rows and same-subject tenant
isolation. Unconditional writes remain an application choice.

The idempotent order example adds an approved SECURITY INVOKER RPC, authenticated
subject/tenant-scoped UUID keys, payload-conflict detection and immutable stored
responses. Its typed client uses generated RPC arguments and request deadlines.
PostgreSQL/PostgREST acceptance verifies concurrent identical and conflicting
requests, identity isolation, failed-validation recovery, receipt immutability,
and replay after a response is lost following commit. Receipt retention is
explicit and the guarantee covers database work inside the RPC transaction.

The transactional outbox example creates an order and one pending event in the
same transaction. Typed worker RPCs use tenant/subject-scoped claims,
`FOR UPDATE SKIP LOCKED`, 30-second leases, token-fenced acknowledgements and
capped exponential retry backoff. Acceptance checks rollback, idempotent event
creation, competing workers, crash recovery, stale acknowledgements, tenant
isolation and typed dispatcher retries. Delivery is at least once; consumers
must deduplicate stable event identities. Scheduling, retention and external
provider calls remain application-owned.

The outbox example now includes configurable per-event retry budgets,
dead-letter state and worker-authorized replay using an expected generation.
Replay preserves the consumer's event identity and lifetime attempt count while
resetting the delivery-cycle budget. A SECURITY INVOKER queue health view counts
pending, leased, delivered and dead-lettered work under subject/tenant RLS.
Acceptance covers exhausted retries, final-attempt crashes, concurrent and stale
replays, active-lease protection, completed-event protection and tenant isolation.

`bulkMutations` adds typed per-item reports, configurable scheduling windows,
bounded request concurrency, deadlines, cancellation and progress counts. Each
item is a separate transaction; windows are not atomic. Partial validation
failures preserve successful writes, while transport/5xx/cancellation failures
are marked uncertain and never automatically replayed. Acceptance verifies
per-item isolation, tenant RLS, version conflicts, cancellation and the distinct
atomic behavior of a single PostgREST array insert.

The import preview example adds an approved STABLE read-only RPC with per-row
validation, duplicate-key errors, insert/update/unchanged actions and a typed
review summary. Results respect subject/tenant RLS. A separate commit RPC
revalidates and upserts atomically; review does not lock records or guarantee
that every future database constraint will pass. PostgreSQL acceptance verifies
read-only execution, unchanged identity sequences, hidden-key isolation and
whole-call rollback when commit-time constraints reject a row.

Import review now binds commits to the previewed row ID/version and authenticated
subject/tenant. Expected new keys must remain absent. Changed or deleted/recreated
rows reject the whole import with a review conflict, rolling back earlier writes
in the same transaction. The unguarded RPC signature is removed. Acceptance
covers concurrent commits, new-key races, deleted/recreated keys, ordinary REST
version advancement, identity changes and whole-import rollback.

The consolidated release-candidate gate now requires 22 passing checks. It runs
current SDK feature tests against the installed tarball and installs all five
application workflow examples with the shipped CLI/runtime, compiling their
generated clients and replaying required scenarios. The checksum-bound report
records each example's schema fingerprint. This local qualification remains
separate from hosted staging acceptance and release publication.

## Coordinated hosted schema refresh

Opt-in runtimes can use a durable per-binding PostgreSQL admission barrier.
Coordinated sync drains admitted transactions before migrations, refreshes while
ordinary requests remain paused, rechecks compatibility approvals, and requires
client and authorization checks before release. Release re-inspects the live
role-filtered schema; stale runtimes and missing control state fail closed.
`--resume-only` repeats verification after a failure without repeating migrations.
The owner-installed control schema stays private. See ADR-981 and the
coordinated hosted schema updates section in `docs/data-api.md` for setup,
recovery, connection budgeting and the boundary around out-of-band owner DDL.


Coordinated refresh now supports a read-only `coordination.setup_check` command
and `data-api sync --check`, including checks of an owned recovery. The owner
helper verifies barrier installation, privileges, serving-role restrictions and
role-filtered inspection without changing control state. `data-api status`
reports admission state, fingerprints, optional baseline comparison and recovery
guidance without exposing credentials. Sync rejects unsupported runtimes before
attempting pause. Existing configurations remain accepted; add `setup_check`
to enable the additional owner prerequisite check.


`data-api replay` runs saved authorization scenarios against the hosted app
using environment-mapped application JWTs. It checks missing/invalid auth and
selected identity acceptance, reuses capture/status/JSON assertions, and emits
redacted reports. Native `coordination.authorization_replay` validates inputs
before pause and runs required scenarios pinned to the verified candidate before
resume; failed replay retains the pause and recovery repeats verification.
Command-based authorization checks remain supported. Saved writes execute on the
hosted app and are not automatically rolled back or retried. Live staging
qualification remains pending.

`gregale data-api check` combines schema compatibility and exact committed-type
freshness in one read-only CI report. Hosted checks inspect once and render from
that snapshot; `--current FILE` supports offline checks without account or
database credentials. Unapproved breaking changes and stale, missing or oversized
types fail without rewriting files. Existing fingerprint-bound approvals apply
only to compatibility. Node.js 22 or newer runs the embedded shared generator;
no hosted runtime upgrade or npm installation is required for the check.

`gregale data-api diagnostics` summarizes retained safe request logs, including
volume, HTTP errors, aborted requests, nearest-rank latency percentiles, fixed
route categories and gateway failure counts. Request-ID lookup and offline
NDJSON/envelope analysis require no runtime upgrade. Reports discard arbitrary
log content, distinguish REST/RPC from internal routes, and flag gaps, read
failures and bounded/truncated samples with a failing exit. Coverage remains a
retained/provided sample rather than durable fleet-wide metrics.

## SDK error classification

`classifyDataError()` adds typed application categories and recovery guidance for
common SQL constraints, authentication/permissions, contract mismatches,
timeouts/cancellation and service availability. It preserves original errors,
returns null for successful null errors and does not retry requests. Recovery
hints do not establish that failed writes rolled back. Strict typing and actual
client result, exception, deadline and cancellation paths are covered locally.

## Runtime RPC argument validation

`validatedRpc()` accepts untrusted input and an application-owned synchronous
argument parser before constructing the typed RPC query. Invalid input returns a
redacted `RPC_ARGUMENT_VALIDATION_FAILED` error without sending a request. Valid
input retains generated argument/projection types and the original query result;
request exceptions and retry settings are unchanged. Tests cover transformations,
defaults, zero-argument RPCs, invalid/async parsers, server failures and deadlines.

## Validated RPC acceptance

The integration runner now includes `tests/data-api/validated-rpc.test.mjs`.
It compiles and executes a TypeScript client against a contract generated from a
disposable PostgreSQL database, through the real JWT gateway and PostgREST 14.3.
Coverage includes scalar and projected relation results, default/nullable
arguments, parser rejection before token lookup or transport, two-subject RLS,
SQL privilege failures, unique/foreign-key SQLSTATE classification, authentication
and contract guards, and an HTTP 503 with retries explicitly disabled.
A nontransactional sequence independently checks mutation invocation counts even
when SQL rolls back. A response lost after commit persists one row and is
reconciled by a read without replay. This is local acceptance, not hosted staging
qualification.

## Validated RPC starter form

The notes browser starter now demonstrates runtime argument validation and typed
RPC results using the existing approved idempotent note-creation function.
It provides inline body/tag feedback and curated SQL/authentication/permission
messages. Deadlines disable automatic retries; uncertain writes retain an
immutable key/payload and block new submissions until explicit same-user recovery.
Recovery is same-key RPC replay within the owner's receipt-retention window,
not a read-only check. Pending state is in memory and is lost on reload.
Client, real PostgreSQL/PostgREST and required Chromium tests cover this path,
including response loss after commit without duplicate creation.

## RPC schema-drift CI acceptance

The integration runner now checks RPC evolution against real restricted-role
PostgreSQL catalog snapshots and the native `data-api check` command. Fifteen
scenarios cover required/optional argument additions, removals and renames,
argument and result type changes, scalar/set-returning changes, default presence,
approval annotation removal, execution revocation and an unapproved overload.
Generated nullable arguments compile and run against PostgreSQL. Stale declarations
fail even for compatible additions and fingerprint-approved breaking changes;
regenerating declarations alone does not approve a break. Reports and exit codes
are asserted, and check inputs remain unchanged.
Default expression changes, including a default changing to NULL, intentionally
retain the same structural fingerprint. Tests demonstrate their changed SQL
behavior and the passing structural check; application replay tests remain
necessary for default values and function bodies.

## Whole RPC response validation

The SDK exports `validateRpcResponse()` and `RpcResponseValidationError` for
opt-in, synchronous application-owned validation of scalar, JSON and row results.
Successful outcomes retain response metadata with typed parser output; request
failures remain original results, and safe validation failures omit rejected data
and parser exceptions. No requests or retries occur. Strict types, packaged
consumer coverage and real PostgreSQL/PostgREST acceptance cover this behavior,
including response-parser rejection after a write commits without replay.

The team starter now supports named webhook subscriptions for submitted and fulfilled
order events. Matching transitions fan out atomically into independent queues, with
owner metadata controls, per-subscription pause/health and individual replay. Queued
configuration references and versions remain pinned across edits; private workers
resolve approved references to URLs and signing keys. Existing teams with no named
subscriptions retain the original queue. Named subscriptions are customer-owned
starter functionality, with local acceptance coverage; hosted operation is not yet
qualified.

The team starter adds owner-approved signed `webhook.test` sample deliveries, with
revision-pinned destinations, idempotent receipts and no order mutations. Owners can
inspect paginated attempt and subscription-change history, retry eligibility and safe
failure categories for missing configuration, network errors and HTTP errors. Tests
respect pause and use the customer-scheduled worker; HTTP acceptance confirms only
the pinned destination at the attempt time. Private URLs, keys and provider error
bodies remain outside operational metadata. Existing named acknowledgement callers
can omit the new optional failure reason.

The team starter adds personal saved order views and atomic bulk fulfilment. Views
retain status/total filters and sorting with stable UUID tie breakers, and remain
private to the verified user/team. Owners and admins preview up to 20 selected
submitted orders, approve their reviewed versions, and fulfil the whole selection.
A stale row rolls back all updates, workflow events, webhook fan-out and receipts.
Immutable operation keys support exact recovery after writer access is lost; batch
history remains available to current team members. The UI clears selections when
filters or pages change and requires another preview after a known conflict.

The team starter adds shared order views. Owners and admins publish and revise named
status/total filters and sorting; members apply them through the existing query,
export and bulk-review controls. Members can create independent personal copies of
a reviewed shared revision. Stale sources and existing personal destinations are
refused atomically. Publication history records actors and versions, while copy names
and personal snapshots remain private to the copying actor. Immutable request keys
support exact recovery after access loss. Shared presets do not grant write access
or change orders.

Data API schema diffs now accept `--dependencies FILE` to report affected
customer-declared saved views and RPCs. JSON reports retain schema compatibility
results and add consumer impacts with matching changes and approval status.
Coverage is explicit and does not infer SQL lineage; existing breaking-change
checks retain their semantics.

`gregale data-api migration preview --baseline FILE` rehearses the configured
migration history in a disposable local database and emits a consolidated report
for schema compatibility, declared consumer impacts, committed types, client
checks, authorization, optional saved-request replay, and cleanup. Failures retain
independent check results and return a failing CI exit. Baselines and types remain
read-only. This clean rebuild uses local seed data and does not clone production.

Migration preview now supports `--from-version N` for fixture-based upgrade
rehearsals. It verifies the source API against the baseline, loads an older-schema
SQL fixture (`--fixture`, or the configured seed), and applies only pending
migrations. `--data-check FILE` adds read-only SQL assertions for preservation and
backfills, with results in the consolidated report. Source mismatches, fixture
errors, existing-row migration failures, and failed assertions return failing CI
exits while preserving input files and cleaning up disposable services.

`gregale data-api fixture build` exports representative local/test database rows
using explicit table/column allowlists, equality filters, replacement/null masks,
and consistent grouped text hashes. Declared relationships are included without
silent truncation. A read-only source transaction produces typed SQL, basic key
and reference assertions, an API contract, and metadata in a new private package.
Upgrade preview checks generated fixture payloads and source fingerprints before
loading them. Initial support requires primary keys and acyclic table dependencies.

- Added local `gregale data-api explain`: identity-aware estimated plans for saved
  reads, redacted scan/sort reports, reviewable index candidates, and baseline cost
  comparison with disposable database cleanup. No hosted credentials required.

- `data-api explain --check --baseline FILE --budget FILE` now enforces per-query
  estimated-cost, cost-growth and approximate scan-row budgets. Checked runs fail
  on missing coverage and report violations after disposable service cleanup.

- Added `data-api verify --checks FILE`: reusable named read-only data assertions
  and project authorization verification against disposable local seed/fixture
  data, with CI exit codes, redacted reports and cleanup on failure.

- `data-api verify --access-checks FILE` now runs reusable identity-aware request
  assertions, including ordered access transitions, with named redacted results
  and CI failure handling. SQL checks can be combined or omitted.

- Added offline `data-api verify init` to generate editable integrity/access
  templates, optional baseline query budgets and a coverage review guide. It
  preserves existing packages and leaves customer-specific access rules pending.

- Added offline `data-api verify coverage --check` for explicit relation-to-check
  mappings, required integrity/access/performance categories, matching report
  evidence, and coverage gaps on newly added schema relations.

- Added `data-api verify release --config FILE`: one local CI entrypoint for
  migration preview, data/access checks, query budgets and coverage, using fresh
  schema-matched evidence and a consolidated report with blocked stages.

- Added offline `data-api migration guide` with before/after contract shapes,
  reviewable TypeScript suggestions, conservative saved-query impacts, declared
  consumer impacts and verification-review mappings. Application files stay unchanged.

- Added typed server-side `createCursorPager` with encrypted, expiring continuation
  tokens bound to actual queries, ordering, page size, schema and trusted identity
  scope. Existing raw keyset helpers remain available; RLS tests cover both directions.

- Added opt-in `DATA_API_QUERY_SHAPES=1` runtime logging with catalog-based redacted
  shapes. Diagnostics aggregates operation/shape counts, errors, percentiles,
  serving-schema groups and sample request IDs without query values or identities.

- Added `data-api transaction init`: a reviewable tenant-scoped order/inventory RPC
  starter with schema-derived client types, input validation, row locks, idempotent
  receipts and generated rollback/isolation verification scenarios.

- `data-api types --validators` generates dependency-free, typed runtime insert/update
  parsers alongside schema types. It checks writable fields, presence/nullability,
  primitives, enums, arrays/composites, scalar varchar lengths and intrinsic integer
  bounds, and exports review warnings for domain/CHECK/unsupported type rules.
  `--check` compares the complete output; existing contract fingerprints stay stable.

- Local `data-api dev --validators` refreshes runtime parsers and types on startup
  and successful migration reloads. `--validators --check-types --once` compares
  the complete output without rewriting stale files. The notes starter form uses
  generated insert rules before token lookup or transport when opted in.

- Generated input errors now expose immutable field paths and stable issue codes,
  preserving the top-level `invalid_input` code. Parsers report the first failure
  without copying submitted values, unknown field names or arbitrary JSON keys.
  The notes starter maps body length/type/presence issues to field feedback.

- Validator generation now includes `rpcValidators.<schema>.<function>.parse`,
  with schema-generated argument types, required/defaulted presence, explicit
  nulls and nested primitive/enum/array/composite/row checks. Parsers plug directly
  into `validatedRpc` without losing result inference. The transaction starter
  consumes generated checks while retaining SQL-backed business validation.

- Generated `validateInputRows` validates up to 1,000 rows with complete counts,
  bounded row/field reports and no partial payload on failure. The team CSV preview
  consumes generated insert rules when available, and the notes starter adds a
  pure `previewNotesCsv` example with context-derived ownership and typed payloads.

- Validator generation now exports writable-field `formMetadata` for insert/update
  forms, including presence/nullability, control hints, enum options and supported
  limits. The notes browser uses this metadata for create/edit fields, shows schema
  validation feedback and performs version-checked edits with conflict handling.

- The local inspector now browses exposed tables/views under the selected test
  identity, with filters and bounded pages, and builds validated insert forms from
  schema rules. Writes require payload review and explicit confirmation, invalidate
  stale review context and remain subject to RLS through the regular gateway.

- Snapshot version 4 exports primary/composite keys and explicitly declared
  database-managed integer version columns; older baselines remain supported.
  Generated `tableMetadata` and compatibility checks cover those rules. The inspector
  now validates/reviews keyed edits and rejects stale-version writes without retry.

The local Data API inspector offers Parent and Children actions for exposed foreign keys, including composite keys. Each action reads related rows with every key component under the selected identity and database RLS. Nullable, incomplete, unsupported or unsafe keys have no navigation action. The Back trail restores filters and pagination and re-fetches rows; changing identity or schema clears it. Navigation sends reads only and does not populate write forms.

The local inspector can save relationship workflows in `data-api.views.json`
for Git sharing. Each step retains visible columns, scalar filters and sorting;
reopening starts at the root table under the selected identity and guides fresh
row selection through named foreign keys. Definitions exclude clicked-row keys,
row responses and credentials. Atomic revision-checked saves protect concurrent
edits, while schema fingerprints flag stale definitions and pin reopened reads.

Saved relationship views now generate guided RLS-check scaffolds in the local
inspector. Named synthetic fixture bindings and explicit per-identity counts
run both in the inspector and through `data-api verify --access-checks` as a
version 3 manifest. Composite-key validation and positive expectations prevent
unscoped or vacuously passing denial checks. Every identity/step runs independently,
with failures naming the hop and redacted count-only reports. Existing request
access assertions remain compatible.

Version 4 guided RLS manifests add disposable CRUD and ownership-transfer tests.
The inspector exports unreviewed mutation scaffolds and continues to execute
read checks only. CLI verification requires passing read baselines, scoped named
fixtures, explicit payloads and reviewed outcomes. A private read-only observer
checks denied writes against complete exposed-table row state, including fields
hidden by RLS. Minimal write responses avoid masking ownership-transfer gaps.
Failures or uncertain outcomes stop later writes; reports remain redacted and
normal disposable-service cleanup still runs.

Version 5 guided authorization workflows interleave reads and writes to test
membership removal and role downgrades using JWTs issued before the change.
The inspector exports an unreviewed lifecycle scaffold with member and peer
checkpoints; disposable CLI verification executes it in order. Checkpoints
require their data fixture to remain present and unchanged, preventing deleted
fixtures from masking revocation failures. Failures identify the checkpoint and
skip remaining steps; reports stay redacted and cleanup runs normally.

The local inspector now imports CSV into schema-described writable tables with
header mapping, explicit blank/default/NULL handling, generated-model validation
and paged payload approval. Selected-identity inserts use pinned contracts and
atomic batches of up to 25 rows/64 KiB. Exact affected-row counts gate confirmation,
while transaction refusals and uncertain outcomes produce per-record reports.
Follow-up previews exclude confirmed and uncertain records, and no automatic
retry occurs. Report downloads omit CSV values and credentials.

- Inspector CSV export now preserves the selected projection, filters, sorting,
  relationship scope, and identity/RLS across bounded live page reads. It offers
  progress, cancellation, row limits, spreadsheet formula protection, and
  JSON-encoded cells for NULL-aware, exact-numeric downloads and reviewed import.

- Inspector users can review and approve bulk updates or deletes for up to 50
  selected rows, with primary-key/version guards, identity/RLS enforcement,
  cancellation, per-row outcomes, and reviewed retries for refused or unsent rows.

- Bulk updates and deletes can now preview all rows matching an inspector view,
  across pages, up to 1,000 rows. Matching previews preserve filters and
  relationship/RLS scope, reject truncated results, and freeze reviewed keys and
  versions so later matches are excluded from approved writes.

- Bulk operations now offer downloadable redacted JSON reports with run metadata,
  timestamps, outcome totals, and reconciliation lists for conflicting or
  uncertain rows. Reviewed retries link their indexes to the previous run;
  reports omit keys, row contents, filters, and credentials.

- Bulk write previews now require read-only readiness checks before approval.
  Checks validate pinned metadata, current row visibility and reviewed versions,
  expire after 60 seconds, and require a fresh preview after failure or cancellation.
  Database write policies, constraints and triggers remain enforced during execution.

- Bulk-operation reconciliation now re-reads conflicting and uncertain rows under
  the original identity/RLS, compares current versions and values, and offers
  explicit fresh previews with preflight and approval. Matching values and hidden
  rows never resolve uncertain outcomes automatically; follow-up reports retain
  distinct reconciliation lineage.

- Inspector bulk-operation history now persists bounded redacted reports in the
  project, with report downloads, lineage details, retention and explicit clearing.
  Bounded session-only snapshots can reopen prior unresolved runs under their
  original identity and matching schema, with fresh reads and approval gates.

- Read-only support identities and saved support views now offer focused browsing,
  case-insensitive text filters, relationship navigation and CSV export. Signed
  read-only tokens reject mutating table requests and RPC calls at the gateway;
  a private-table example enforces safe column grants and tenant RLS through
  security-invoker views. Private base relations remain outside generated types.

- Added the data-api-support-console starter with customer staff OIDC sign-in,
  server-held read-only sessions, approved tenant-scoped views, search,
  relationship browsing and bounded CSV export. A synthetic two-tenant demo,
  authorization tests, deployment files and real-provider configuration guidance
  are included; the development inspector capability is not used by the console.
