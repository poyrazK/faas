# Data API release review

This change completes the current core Data API development slice. It does not
publish artifacts or establish hosted staging acceptance.

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
