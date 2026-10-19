# ADR-650 · Schema-generated Data APIs and application clients

- **Status:** accepted
- **Date:** 2026-10-07
- **Decision:** Package pinned PostgREST with a JWT-verifying Node gateway as an ordinary Gregale app. Add the portable `data_api` managed PostgreSQL credential mode, a private catalog-based TypeScript generator, `gregale data-api create|types|refresh|sync`, and `@gregale/data`.
- **Why:** Application table endpoints and their TypeScript definitions currently duplicate database schema knowledge. PostgREST supplies schema-driven CRUD and OpenAPI while Gregale's existing app, binding, build, deployment and task lifecycles supply ownership and execution.
- **Consequences:** An explicitly opted-in and newly qualified backend can provision schema-restricted bindings. No additional resource owner, deployment path, metering model or plan quota is introduced. Applications and bindings use existing quota and egress admission. Runtime and export limits are canonical in `pkg/api/limits.go` with an embedded-runtime parity test.
- **Rejected alternatives:** Generating SQL endpoints inside apid would move customer query execution into the control plane. Sending database passwords to client machines would weaken binding custody. Classic PostgREST administrator role membership would allow a JWT role-selection mistake to become DDL authority. Public schema-introspection endpoints would expose an owner development tool to application clients.

The `api` schema belongs to the stable migration owner. A Data API login has
no memberships, ownership, DDL, role creation or RLS bypass, and receives only
schema usage, table DML and sequence privileges in `api`, including future
objects created by that migration owner. Provisioning rejects conflicting schema
ownership, missing grants, public-table access and executable security-definer
functions. Migration bindings retain their release-task-only delivery boundary.
Credential rotation and retirement use the existing binding generation workflow.

ADR-1733 adds a five-second lock-wait default alongside the 15-second statement
timeout. Issuance upgrades only the exact legacy login policy, preserving its
password and rejecting drift; new SQL sessions are required to observe changed
defaults. Updated RPC ACL tooling accepts both exact policies. SDK lock-conflict
and surfaced-deadlock guidance adds no HTTP retries; pinned PostgREST retains its
existing SQL-transaction deadlock retry behavior.

ADR-1734 adds opt-in owner-installed SQL deadline hooks. A gateway-owned admission
expiry survives internal transaction retries, composes around the refresh barrier
and caps subsequent statement/lock timeouts. Startup checks the pinned engine and
exact protected hook contract. Installation/grants remain explicit for each serving
login, including rotation. No extra serving pool, automatic SDK retries or universal
SQL cancellation guarantee is introduced.

The gateway validates application JWTs against an explicit HTTPS issuer/JWKS,
audience and RS256/ES256 algorithm allowlist. It preserves `sub` for RLS and
overwrites the SQL role with the exact binding login. An internal random secret
signs tokens used only on the loopback PostgREST port. Application account keys
do not authenticate data access. V1 exposes relation CRUD and authenticated
OpenAPI, and excludes RPC routes. Customer migrations must define RLS and safe
views; PostgreSQL permissions and policies are the authorization authority.

Type generation runs as an owner-authorized, deployment-attached manual task
using the serving binding. Fixed, parameterized PostgreSQL catalog queries run
inside the customer workload under a read-only repeatable-read transaction,
never against Gregale's catalog or inside apid. This customer-database exception
to the catalog sqlc rule follows the existing provider credential utility SQL
boundary. Output contains only the exposed relation contract and a deterministic
fingerprint, bounded to the existing task output limit. It contains no DSN or
credential. Type export fails on incomplete output and writes atomically.

The schema-cache listener is disabled so an idle app does not keep a managed
database awake through LISTEN. Owners request a fresh app restart after schema
migrations; a snapshot restart could preserve a stale schema cache. SIGUSR1
reload is also forwarded for operator diagnostics. Generated types come from
the current database and `--check` detects drift; exporting does not itself
refresh a serving cache. A failed release does not undo database changes.

The optional CLI `sync` workflow composes an explicit local migration command,
fresh-restart completion and readiness, the existing private type-export task,
and an explicit local application check. Managed migrations continue to run in
deployment release tasks. Commands use argument arrays without an implicit
shell and must wait for completion. A failed step prevents later work; generated
types remain available after a failed client check. Completed steps and accepted
remote work are not rolled back when the local workflow fails or stops. This
adds no server-side orchestration or credential delivery path. Application CI
retains the independent, read-only `types --check` drift check.

Neon advertises this mode only with `data_api_enabled: true`.
Changing this rollout flag requires a fresh qualification report whose
credential capabilities match the advertised contract. It does not change the
placement fingerprint or invalidate existing databases. Advertising the mode
requires live schema isolation,
JWT-sub RLS semantics, password recovery, rotation preservation and revocation
evidence. Existing qualification reports without Data API capability remain
valid for their smaller contract; no production provisioning gate is relaxed.

Acceptance runs real PostgREST and PostgreSQL with JWTs and the TypeScript client.
Native builder/VM deployment and live managed-provider qualification remain
operator rollout checks. The client follows the repository's existing restricted
SDK publication/license policy and is not automatically published.

### Bounded HTTP admission

The existing runtime bounds include per-server HTTP admission before asynchronous
JWT verification: active requests match the engine pool (two, or one with the
coordinated-refresh reader), with eight FIFO waiters and a one-second deadline.
Authenticated metadata shares admission; health and CORS preflight bypass it.
Expired waits are checked again on promotion even if their timer has not run.
Cancelled waiters are removed, and active verification holds its slot until it
settles despite disconnect. An admitted slot covers handler work and response
completion. Full/expired queues return `503 data_api_overloaded` and
`Retry-After: 1` before upstream dispatch. This avoids making overload a PostgREST
engine-recovery event. It adds no account rate quota, global connection budget,
identity policy or automatic write retry. Already-dispatched mutations retain
their existing uncertain-outcome reconciliation requirements.

Logs expose only fixed admission outcomes, wait duration and bounded active/queued
counts. CLI diagnostics validates these fields and reports sampled wait
percentiles, overload counts and observed peaks; older logs have explicit zero
admission observations. These fields measure HTTP admission, not SQL pool wait
or execution. Runtime constants remain canonical in `pkg/api/limits.go`.

An optional owner deployment policy applies token buckets after verified subject
and claim validation, before refresh/readiness SQL or engine dispatch. Setting
`DATA_API_RATE_LIMIT_PER_SECOND` and/or `DATA_API_RATE_LIMIT_BURST` enables it;
omitted values default to 10/20 and each is an integer in 1..1,000. Removing both
disables it. REST/RPC and authenticated metadata share credit across JWT rotation.
Public health and CORS bypass it. Rejections return `429 data_api_rate_limited`,
`Retry-After: 1` and a request ID without SQL dispatch. The existing pre-verification
admission bound remains; this adds no queue-fairness or distributed quota claim.

Each runtime retains at most 4,096 hashed subjects, using monotonic refill and a
lazy sweep no more than once per second. Only fully refilled buckets may expire;
full tracking rejects new subjects rather than evicting depleted counters.
Existing subjects retain access to available credit. No timer, SQL pool or
external coordinator is added. Cold process starts reset budgets; snapshots
may retain them. Canonical defaults and bounds live in `pkg/api/limits.go`.
Logs contain fixed policy/reason/retry metadata without subject keys or hashes.
Diagnostics distinguish rate limits from global overload, and health treats
them as recent findings. SDK category `rate_limited` advises `wait` without
automatic replay; generic HTTP 429 does not establish pre-SQL rejection or rollback.

The canonical 15-second query budget also bounds total admitted elapsed time,
including verification, refresh, signing and response streaming. Queue wait keeps
its independent one-second budget. Monotonic checks after async work and before
dispatch prevent late work from reaching the engine. A timer aborts engine HTTP
work and returns `504 request_deadline_exceeded` before headers or closes an
already-started response. Pending verification/control work retains its active
slot until settlement. Health/CORS retain separate bounds; no SQL connection pool
or statement-timeout policy changes. Timers are not preemptive CPU limits, and
HTTP cancellation does not establish SQL rollback. Existing uncertain-write
reconciliation and no-automatic-mutation-retry semantics remain in force.

Safe request logs attach fixed deadline phases, admitted elapsed time and data
engine HTTP dispatch state. CLI diagnostics validate and aggregate these fields,
retain explicit coverage for older logs, and count incomplete timed-out streams
even when their sent status was 200. SDK classification recognizes the stable
deadline code as timeout/reconcile without relying on HTTP status or error text.

Normal REST/RPC bodies, including engine errors, are streamed with backpressure
and a canonical 16 MiB byte cap. Exact-cap results succeed. An oversized declared
length returns `502 response_too_large` before headers; an unknown/chunked length
is checked before each chunk is forwarded, and overflow closes a started stream.
HEAD/bodyless representation lengths are excluded. Request/schema byte and row
caps remain separate. The gateway does not buffer full normal responses or claim
to bound the engine's result allocation. Safe logs retain only the fixed source,
limit and a one-byte-over lower bound; active streaming timings are incomplete.
CLI diagnostics count aborted 200 streams as failures and retain safe metadata
in request-ID lookup. Health treats these as recent findings. SDK classification
uses `response_too_large`/`reconcile`; generic interrupted bodies stay
`no_response`. HTTP failure cannot establish SQL rollback, including a pre-header
502: a mutation may already have committed. No automatic mutation retries are
added. Local real-SQL fixtures verify this committed-write case and RLS recovery.

Public `/livez` checks only gateway responsiveness; deployment `/healthz` keeps
SQL readiness and adds fixed, safe observations for engine TCP reachability,
SQL readiness, database reachability evidence, coordinated refresh and admission
counts. Failed or timed-out SQL readiness leaves database connectivity unknown;
pool saturation is not a database-outage diagnosis. PostgREST 14.3's admin `/live`
also checks SQL with listeners disabled, so independent engine reachability uses
a loopback TCP connection. Two-second callers share in-flight admin work, held
until completion or the existing query deadline, and the reserved refresh reader.
No new SQL pool, native health owner or process-restart behavior is introduced.
CLI `data-api health` combines these per-probe observations with validated recent
retained logs. Credentials remain confined to the management API, response bodies
are never echoed, missing runtime support/log coverage stays partial, and current
health is separate from historical failures. A report is not a fleet-wide or
atomic health guarantee and does not replace coordinated `data-api status`.

The runtime reads only the pinned engine's four private pool metrics: maximum
capacity, existing idle connections, pending acquisitions and cumulative
acquisition timeouts. The pinned admin handler checks SQL twice before metrics
with its listener disabled. Reads reuse the engine pool and can remain unknown
under saturation. Two-second callers share pending work until response or the
existing 15-second hard HTTP cap; SQL work can outlive HTTP cancellation. The
8 KiB health-response bound is reused and completed results are not cached. Only validated counts and an observation timestamp enter `/healthz`.
Missing/malformed/unsupported metrics yield explicit unknown coverage without
changing SQL readiness or deployment HTTP status. CLI validation preserves core
readiness when this optional observation is absent or invalid. Pending acquisitions
are a current finding; cumulative timeouts/reset are neither current failures nor
recent-window counts. Available connections belong to a lazy pool, so subtracting
them from capacity cannot identify active connections or prove exhaustion. Waiters
can include readiness probes. Counts do not isolate SQL/pool-wait latency or
establish database-wide budgets, and the private metrics endpoint is not exposed.
This coupling is explicit in the pinned engine's
[admin handler](https://github.com/PostgREST/postgrest/blob/v14.3/src/PostgREST/Admin.hs)
and [SQL connectivity checks](https://github.com/PostgREST/postgrest/blob/v14.3/src/PostgREST/AppState.hs).

[ADR-1731](1731-data-api-database-timing-telemetry.md) adds private engine
request timing headers to safe request logs, CLI diagnostics and health summaries.
The combined transaction interval includes pool acquisition and SQL. Isolated
SQL execution and pool-wait durations remain unknown; missing/error observations
are counted separately, and do not become zero latency or current readiness.

[ADR-1732](1732-data-api-user-concurrency-limits.md) adds opt-in per-user
concurrent HTTP request limits after verified identity and before SQL dispatch.
Users share slots across JWT rotation. Unknown dispatched work keeps a bounded
cooldown after cancellation; this is neither a SQL-session fence nor fleet-wide
fair admission. Safe 429 metadata flows into SDK recovery and CLI diagnostics.

The local load verifier composes signed-user REST/RPC traffic, RLS, admission and
rate pressure, the real HTTP deadline, disconnects, response bounds, lost replies
and embedded gateway/engine restart on one disposable PostgreSQL 16 fixture.
It settles uncertain SQL work and reconciles receipts before explicit same-key
replay, with no SDK mutation retry. The engine executable is checked against its
pinned digest before execution. Independent owner/control observations inspect
accepted/rejected writes and sample serving-role connections without adding a
serving SQL pool. Canonical workload, record, report and deadline bounds live in
`pkg/api/limits.go`. Versioned private redacted checkpoints retain source identities,
client latency/throughput, expected rejection counts, recovery and cleanup.
Local fixture success does not establish production supervision, fleet capacity,
SQL/pool timing or native qualification. The staging canary supplies a hosted
profile and adapter for the shared runner, with
seven supported public-edge scenarios, including completed fresh managed restart
and observed park/wake. Disposable migration-owned RPCs provide fixed pressure,
tenant-scoped reads and invocation-counted write receipts; the normal permission
release revokes PUBLIC and grants approved serving RPCs. The adapter can mutate
only the parent's acknowledged app, preserves build/contract/source evidence,
and bounds traffic, client bodies, duration and redacted checkpoints. Unsupported
private fault controls and SQL measurements remain explicit. Client cancellation
does not attest resource cleanup, which remains in the parent canary journal.
Portable and local SQL tests do not establish hosted qualification; live execution
remains behind existing staging and native/provider qualification gates.

Read-only connection-budget reports join the account-owned database, every
sharing binding/scope, deployment/instance lifecycle and node heartbeat in one
catalog statement per database. The app endpoint lists bounded independent
database snapshots. Canonical Data API/Auth pool estimates, parked/wake demand,
observed rotation/deployment overlap and control/migration headroom remain a
planning model. Duplicate consumers do not multiply the same pool; observed
replacement instances are not added twice. Auxiliary/unbounded clients, stale
nodes, truncated inventory and unverified runtime pool configurations retain
explicit unknown coverage. No provider call, SQL wake, credential delivery,
reservation or scheduler mutation occurs.

Only complete, fresh tenant-usable capacity evidence for the exact database
generation and backend placement can support within/over-budget comparison.
The optional [operator observer](1729-data-api-connection-capacity-observations.md)
now populates a durable capacity cache from a qualified allocation and direct SQL
ceilings. Without fresh matching evidence, capacity stays unknown. Reports
expire within 30 seconds, expose aggregate counts without
credentials or other consumer identities, and use existing read/MFA/ownership
middleware. CLI health validates the report and treats unknown budgets as partial
coverage independently of SQL readiness. Runtime/external-consumer coverage,
automatic qualified allocation sources and aggregate enforcement remain separate
work.

The read-only CI `data-api check` command combines the existing contract diff
and exact generated-type comparison. A hosted check exports one bounded private
snapshot and renders from that validated contract with the CLI's embedded pure
Node generator; no credentials leave the workload and no second catalog read
can disagree with the compared snapshot. Offline checks accept a validated saved
candidate. Reports include compatibility and type freshness without file bodies;
fingerprint-bound approvals do not waive stale types. The runtime, owner tools
and CLI share the same generator source. Neither mode changes database or serving
state, rewrites inputs, or claims to verify policy bodies or all serving replicas.

CLI diagnostics consume the existing owner-authorized runtime log stream or a
provided file. They summarize only validated safe request metadata, deduplicate
request IDs, separate REST/RPC from internal routes and compute sample latency
percentiles. Canonical CLI input/record bounds live in `pkg/api/limits.go`.
Unknown application content and raw transport/error payloads are never included
in the report; gaps and incomplete reads remain explicit partial results.
Diagnostics add no serving endpoint, persistence store or infrastructure owner,
and retained log samples do not attest complete traffic or fleet coverage.

Admitted request records measure sequential, fixed gateway phases: verification,
refresh, readiness, signing, upstream HTTP wait and response streaming. The last
phase is marked incomplete on timeout/cancellation; earlier completed phases
remain usable. Diagnostics retain per-phase coverage and completed percentiles,
with interrupted elapsed lower bounds separate and absent measurements never
converted to zero. Legacy logs remain readable. Query-shape ranking accepts
request count, total gateway p95 or error count. Engine HTTP wait includes
transport/upload, pool and SQL work; these observations cannot isolate PostgreSQL
execution or pool wait. No endpoint, SQL instrumentation or persistence is added.

The SDK's opt-in `classifyDataError` helper maps stable SQLSTATE/gateway codes and
bounded HTTP status fallbacks to application categories and recovery guidance.
It retains the original error by reference, does not interpret private message
text, and executes no requests or retries. Unknown codes remain inspectable;
status-zero failures and deadline/cancellation results preserve the need to
reconcile uncertain writes. It adds no transport or authorization behavior.

The SDK also offers `validatedRpc` to parse untrusted argument objects with a
synchronous application-owned parser before query construction. A typed callback
connects parser output to the generated RPC contract and preserves the awaited
query's exact result type and response identity. Validation failures omit input
and parser exceptions and prevent token lookup or transport. Request exceptions
propagate normally; server errors remain original query results. This opt-in
helper adds no generated runtime validation, authorization or retry behavior.

The browser starter composes application-owned argument parsing, `validatedRpc`,
error classification and request deadlines with the existing idempotent creation
function. It retains immutable pending arguments in memory and requires explicit
same-identity replay for uncertain outcomes within the receipt-retention contract.
It adds no migrations, automatic write retries, token persistence or new service.

Whole RPC response validation is an opt-in SDK helper over an already completed
result. Synchronous application-owned parsers determine output types, including
nullable handling. Server and transport failures remain original results;
validation failures retain status but omit rejected values and parser exceptions.
The helper performs no requests or retries and does not imply mutation rollback,
adding no generated validators, dependencies or authorization behavior.

Guided local onboarding composes the existing starter, checksum-verified SDK
installer and disposable development acceptance workflow. It generates types,
checks the client and replays CRUD, RPC and two-user denial scenarios before
reporting local activation. Step durations remain local; no telemetry is sent.
Failures stop later work, preserve the project and identify the existing commands
for recovery. Onboarding creates no hosted resources, changes no owner boundary
and does not establish managed-provider or production RLS qualification.

The team orders starter extends this application-template boundary with
owner-provisioned PostgreSQL membership, SECURITY INVOKER tenant policies,
read-only members and owner/admin order writes. It trusts verified JWT subjects
and current database membership rather than tenant/role claims. A reviewed RPC
combines version checks with per-subject/per-tenant immutable response receipts;
transaction-local scopes deny HTTP mutations that bypass that workflow. These
scopes are not a sandbox against holders of raw SQL login credentials. The
browser validates arguments/results, keeps pending state in memory and verifies
the original authenticated subject before explicit replay. Owner migration and
artifact tools share existing canonical sources. This adds no platform service,
external side effect or hosted qualification.

Team administration remains customer application SQL and UI. An append-only
starter migration adds a trusted owner-provisioned identity-ID registry,
per-membership UUID revisions, tenant-serialized owner-only RPCs with
READ COMMITTED checks, owner-loss
protection and atomic promotion/demotion for ownership transfer. Scoped SECURITY
INVOKER reads avoid recursive RLS ownership queries; direct HTTP membership,
registry and audit mutations remain denied. Mutation events are immutable
response receipts. The original actor may retrieve an exact-key receipt after
losing membership, which permits read-only confirmation without restoring
permissions or exposing a member directory/history. Clients retain original
identity and arguments, require explicit recovery, and never automatically
retry writes. Audit metadata contains user IDs and needs owner privacy/retention
maintenance; it is not a ledger protected from the database owner. Identity
creation, provider synchronization, invitations and team deletion are separate
owner workflows. No platform identity service or lifecycle owner is added.

Self-service team invitations likewise remain customer-owned starter SQL and UI.
The append-only third migration stores hashes of client-generated 256-bit bearer
secrets, member/admin roles, explicit expiry and immutable invitation events.
Verified JWT subjects accept and self-register atomically; callers cannot supply
the accepting identity. Tenant advisory locking and READ COMMITTED rechecks
serialize acceptance, revocation and owner changes. Issuer ownership is checked
at acceptance, existing memberships cannot be overwritten, and historical replay
does not re-grant removed access. A self-identity RPC binds client recovery to the
original verified subject without requiring prior membership. Link fragments and
in-memory recovery avoid browser-server query logs and persistent client secrets.
Email delivery, identity-provider account creation and email-bound invitations
remain outside this starter. Initial team bootstrap remains trusted provisioning.

The team starter's CSV workflow follows the same customer-application boundary.
A fourth append-only migration stores immutable create-only order manifests and
resumable progress. Reviewed SECURITY INVOKER registration validates the complete
bounded manifest before order writes; actor-bound batches atomically insert up to
20 rows and advance counts/cursors. Existing IDs are skipped, never overwritten.
Tenant locking rechecks owner/admin authority after waiting, and old cursors only
retrieve progress. Client recovery retains one immutable operation and verifies
its original actor. Confirmed jobs resume from owner/admin-visible database
history after reload. CSV validation and mapping run locally; exports scan current
RLS-visible rows with bounded cursor pagination and spreadsheet-safe serialization.
Workflow bounds are application limits, not platform quotas. This adds no worker,
platform-owned customer table, identity service or hosted qualification; manifest
retention and correction of already committed batches remain owner responsibilities.

Reviewed order actions remain customer application SQL and UI. The fifth
append-only migration replaces the existing save implementation without changing
its signature or stored receipts: fresh ordinary creates are draft-only, draft
amount edits remain supported, and status changes use a version-checked transition
RPC. A database trigger enforces forward draft/submitted/fulfilled transitions,
freezes totals after submission and prevents ordinary-save bypass. Another trigger
records reviewed edits/actions atomically with order/receipt writes. Imported
initial states are retained for historical migration, with subsequent updates
subject to the same rules. Tenant locks recheck current owner/admin authority;
original-actor recovery reuses immutable keys and returns historical receipts
without reverting newer state. All current team members may read tenant-scoped
order history. This is a deliberate application behavior change for old clients,
not a platform service or a generated universal workflow engine. Owner SQL
maintenance/retention and further business transitions remain application concerns.

The team's transactional outbox remains application-owned. A sixth append-only
migration atomically captures reviewed submit/fulfil snapshots, lease-based worker
claim/finish RPCs, finite retry/dead-letter state and private attempt history.
Issuer-signed boolean worker/team claims authorize one tenant's dispatcher without
team membership or migration credentials. RLS and transaction-local scopes deny
direct queue/history mutation; owner-only generation-checked replay preserves
stable event identity and lifetime attempts. A separately invoked Node worker
uses an operator-configured HTTPS destination and environment signing key, HMAC
signatures over exact bytes, bounded HTTP calls and no redirects or provider-body
persistence. Secrets/destinations stay outside browser and database metadata.
Delivery is at least once; receivers deduplicate signed namespace/event identities
atomically with business effects. This adds no platform scheduler or worker fleet;
worker deployment, destination configuration, egress, secret rotation and queue
retention remain customer/operator responsibilities.

Tenant delivery controls remain application-owned starter SQL/UI. The seventh
append-only migration adds revision-checked pause/resume, snapshot queue health,
bounded reviewed batch replay and immutable owner-operation receipts/history.
A shared advisory lock allows concurrent worker claims; owner pause and replay
hold its exclusive counterpart after the membership lock. A committed pause
blocks new leases while active token recovery and acknowledgement remain allowed.
Health partitions effective expired-lease states and uses enqueue age. Reviewed
batch generations are atomically reset or rejected as a set, without including
new failures after preview. Original actors can resolve exact receipts after
losing ownership; a missing receipt may retry only as the original current owner.
Current owners alone browse history; direct HTTP settings/history writes remain
denied. No platform scheduler, quota or hosted resource is introduced.

The customer-owned team starter's migration 0008 adds named webhook subscriptions.
Owner-only revision-checked metadata and immutable receipts drive atomic filtered
fan-out into tenant- and subscription-scoped queues. Verified worker claims scope
claim and acknowledgement RPCs to one subscription. Queue rows pin configuration
references and subscription versions; operator-owned worker bindings contain the
actual endpoints and signing keys. Team pause also gates named queues. No platform
service, credentials store, scheduler, or component ownership changes are introduced.

Customer-owned migration 0009 adds idempotent owner-approved sample webhook enqueue
and immutable test receipts, scoped by verified actor/tenant with exact receipt
recovery after demotion. Samples enter the existing subscription queue, pin the
reviewed metadata revision/configuration and obey shared pause/lease controls, while
leaving orders and order events untouched. Private workers emit signed `webhook.test`
sample payloads and persist only curated failure categories. The acknowledgement
RPC gains an optional diagnostic argument; direct queue/attempt mutations remain
denied by RLS. Paginated owner views expose attempts and metadata change history.
This adds no platform service or hosted scheduling contract.

Customer-owned migration 0010 adds per-user/team saved views and reviewed bulk
fulfilment. RLS isolates view preferences to the verified subject with current team
membership; all roles may save their own preferences through revision-checked,
idempotent RPCs. Typed queries support status/total filters and UUID tie breakers
for ascending/descending ID and total sorting. Bulk previews reveal only accessible
rows and require writer membership. Application rechecks that membership after the
team-administration lock, validates 1–20 distinct reviewed IDs/versions, and transitions
them atomically through the existing workflow/event/outbox triggers. Immutable
actor/team receipts preserve exact results and permit original-actor recovery after
demotion, while new changes require current writer access. Direct writes remain
denied. These bounds belong to customer application workflows, not platform quotas;
no platform component ownership or hosted processing contract changes.

Customer-owned migration 0011 adds shared order-view definitions and immutable
publication/copy operations. Verified team membership gates reads; owner/admin
membership, rechecked after the administration lock, gates publication. Members may
copy a reviewed shared revision into a new personal view through the existing save
RPC in one transaction. Copy operation rows and personal snapshots are actor-private;
publication history remains team-readable. Exact actor/key receipt recovery remains
possible after access loss, while new writes require current rights. Shared views
reuse typed query criteria and operational preview/version checks; they are presets,
not authorization policies or live links to personal copies. No platform component
ownership, plan quota or hosted service changes are introduced.

Migration rehearsal reuses the customer-owned local development runtime through
`gregale data-api migration preview`. It rebuilds migration history and local
seed data in disposable PostgreSQL, uses the existing contract comparator and
fingerprint-bound approvals, and reports authorization, committed types, client
checks, optional request replay, consumer impacts, and cleanup together. Preview
preserves contracts and types and persists no local identity receipt. Its scope
is a clean rebuild, not a clone of production data or a hosted migration action.

Fixture-based upgrade rehearsal extends local migration preview with
`--from-version`. The disposable database is built through the selected version;
its exposed contract must match the reviewed baseline before older-schema fixture
data is loaded and pending migrations run. Optional `--data-check` assertions use
single-statement, read-only owner transactions before API verification mutates
its own test rows. Report coverage is explicit and does not claim production
cloning, load, or concurrency qualification. Hosted component ownership is
unchanged.

The local fixture builder is a customer-controlled read-only PostgreSQL export
workflow. Explicit column allowlists and masking rules bound the selected data;
declared foreign-key closure preserves related rows. Source contract inspection
can use the serving role, while row reads retain the export account's existing
permissions. Packages contain typed SQL, generated key/reference checks, source
contract fingerprints, and checksums. Upgrade rehearsal verifies generated
fixture bindings before loading. The workflow does not alter hosted database
ownership or provide automatic sensitive-data classification.

Local query-plan inspection uses the disposable development database and gateway
identities. PostgREST plan responses are enabled only for `data-api explain`;
production defaults remain unchanged. Reports expose bounded estimated plan trees
without expressions or values, and index suggestions require developer review.

Query performance gates compare the same local workload and fixture against an
approved explain report. Explicit versioned budgets cover all planned reads;
missing baseline queries or skipped budgeted reads fail closed. Thresholds apply
to planner estimates, never hosted latency, and do not modify indexes or policies.

Standalone integrity verification reuses migration-preview assertion manifests in
a disposable current-schema database. Assertions inspect all fixture rows as the
local owner in separate read-only transactions; required project API scenarios
verify RLS independently. This command does not access hosted customer databases.

Identity-aware verification reuses validated saved request collections and the
local gateway's configured identities. Successful access assertions require
explicit JSON expectations. Failed scenarios stop subsequent mutations and
produce redacted named results; access checks never target hosted databases.

Verification scaffolding works offline from saved contracts and identity names.
Schema nullability and known API relationship metadata provide integrity templates;
permission semantics are never inferred. Access drafts require fixture edits,
coverage stays pending review, and optional budgets derive from matching successful
explain reports. Initializers do not connect to customer databases or run checks.

Coverage evaluation uses customer-declared rule mappings and current-schema report
artifacts. Generated templates are not evidence. New relations inherit explicit
default requirements; access checks must pass and performance checks require budget
enforcement. CI gates inventory coverage without inferring assertion semantics or
attesting fixture/test freshness, and never access hosted customer databases.

Release verification composes existing local commands with structured arguments,
private temporary evidence and schema fingerprint checks. Dependent stages stop on
failure; coverage uses reports from the current invocation. Clean rebuilds share
configured seed data, while upgrades require explicit older/current fixtures.
Cancellation permits disposable cleanup. This readiness gate does not publish,
deploy or access hosted customer data.

Migration guidance reuses validated saved contracts and the compatibility diff.
Consumer impacts are declared or conservatively matched to root API request paths;
SQL lineage, conversions and renames are not inferred. Suggestions and verification
review mappings are advisory, with no automatic application-code edits or approvals.
Saved request bodies, claims and filter values are excluded from guide output.

Opaque SDK continuation tokens extend existing typed keyset queries using server-only
AES-GCM keys and explicit verified identity scope. Actual query metadata and candidate
schema fingerprint bind continuations; cursors do not confer access or replace RLS.
The helper reserves one row of the existing 1,000-row cap for lookahead, validates
selected ordering columns and leaves uniqueness enforcement to schema constraints.
No hosted signing service or browser-distributed secret is introduced.

Structural query telemetry is opt-in and extends existing bounded request logs,
not a new hosted metrics store. Authenticated known-catalog operations contribute
normalized projections, operators and ordering without values, bodies or claims.
Diagnostics validates descriptors/digests and exposes coverage, per-operation and
per-shape sample statistics. Complex queries require manual fixture selection;
structural summaries are not executable queries or deployment attestations.

Transaction starters reuse the existing opted-in SECURITY INVOKER RPC contract and
schema type generation. Customer-reviewed functions own business validation, canonical
stock locking and idempotent receipts; RLS derives tenant/actor scope from verified
claims. Direct REST writes require an operation-specific local marker. This example
pattern does not replace grant/function review or authorize arbitrary SQL execution.

### Runtime input validation from catalog exports

The opt-in `data-api types --validators` owner task exports typed, dependency-free
insert/update parsers in the same TypeScript module. Parsers derive presence,
nullability, permissions and supported primitive/enum/array/composite shapes from
the normalized contract. A bounded, privilege-filtered catalog read in the same
read-only transaction supplies scalar varchar lengths and intrinsic integer bounds.
No CHECK expressions or function bodies are exported or interpreted. Generated
warnings identify domain, CHECK, unsupported type and unhandled modifier rules.
Parsing is strict, detached and bounded; failures expose no supplied values.

This extends customer tooling without changing the versioned API contract or its
fingerprint. Existing plain generation stays unchanged. Full generated output is
checked with `--validators --check`, since catalog-only rules are not runtime
accepted-contract policy. SQL constraints, triggers and RLS retain authority.

Local development accepts the same opt-in with `dev --validators`. Both initial
startup and successful watched migrations use the same read-only schema/metadata
bundle to generate a single module. Existing compatibility and authorization
checks precede adoption; `--check-types` compares the entire module and preserves
stale files. The starter form optionally consumes the generated parser before
request-key creation and token/transport access, while retaining explicit business
validation and the existing plain-type workflow.

Generated parser failures keep the existing top-level `invalid_input` code and add
one immutable issue with a stable reason code and structural path. Paths include
only schema-defined field names and typed-array indices. Unknown fields use the
parent path; arbitrary JSON keys are never copied into paths. Parsers remain
fail-fast and bounded, reject accessors without evaluating them, and never include
submitted values in issues or messages. Starter feedback maps recognized body
issues to application-owned text and retains generic fallbacks for other errors.

Runtime validator generation also exports a separate `rpcValidators` namespace.
Each parser implements `JsonParser<Database[Schema]['Functions'][Name]['Args']>`
for an already-exposed function, preserving unknown-input parsing and SDK query
callback inference without adding grants or routes. Normalized argument optionality
controls omission; explicit NULL remains accepted as in the generated Args type.
No default expressions or function bodies are read. Nested composites and supported
row arguments use normalized contract fields. Every RPC warns that database/body
rules need review; transaction wrappers keep the business validation not described
by the schema. Direct parser failures retain safe field issues, while `validatedRpc`
keeps its existing redacted failure envelope.

Generated `validateInputRows` reuses synchronous table/RPC parsers for at most
1,000 rows, collecting complete counts with a bounded first-error-per-row report.
Failed batches expose no partial data; reports retain only safe generated issue
codes/paths and row positions. Generic parser exceptions are redacted. The existing
team CSV preview applies schema insert checks with context-derived tenant/creator
when opted in, and caps combined business/schema errors at 100. The notes starter
adds a pure bounded CSV example with explicit numeric conversion and context-derived
subject. Neither preview performs token lookup or writes; reviewed submission and
SQL/RLS validation remain separate application actions.

Generated validator modules now export structural form metadata from the same
bounded normalized fields and catalog rules. It describes writable insert/update
fields, presence/nullability, primitive/control hints, enum options, supported
length/numeric bounds and review flags, without defaults or SQL expressions.
Applications own labels, allowlists and business/ownership context. Complex fields
require custom controls. The notes browser uses the metadata for body and edit
priority, retains transactional creation, and validates edits before version-checked
updates with bounded deadlines and explicit conflict/uncertain-outcome feedback.
UTF-16 HTML maxlength is not substituted for PostgreSQL code-point length validation.

The local inspector now consumes a bounded structured input model derived from the
same normalized descriptors/catalog rules as generated validators. A browser-side
pure validator is checked for parity against generated TypeScript parsers. The model
has its own revision so catalog-only changes invalidate reviewed inserts without
changing the API contract fingerprint. It is served only with the existing inspector
capability; private receipts omit it. Model limits degrade insert-form availability
without stopping the local API. Reads select only known readable columns, support
bounded filter operators and offset pages, and remain subject to the serving role's
RLS. Inserts require explicit review/approval and use the selected test JWT through
the regular fingerprint route with schema profiles and no automatic retry. No owner
SQL connection or mutation endpoint is added. Editing/deletion require reliable key
and concurrency metadata and remain outside this first browser increment.

Snapshot version 4 adds optional primary-key and concurrency metadata while retaining
versions 1–3 for compatibility baselines. A bounded catalog read exports only complete
readable primary keys and conservative scalar filterability. Concurrency requires
exact `@gregale:version` column annotation, a readable nonnullable native integer,
and an enabled unconditional row-level BEFORE UPDATE trigger covering all updates.
No trigger body is read or inferred; the annotation is the owner's declaration that
the database advances that value on every update. Existing migrations are preserved;
the starter supplies an opt-in example for a new migration.

Inspector edits require supported complete keys and the declared version. Keys and
version are not editable; patches are validated and reviewed with before/after values.
The regular gateway applies all key predicates plus the expected version under the
selected test JWT and contract fingerprint. Empty results require a fresh read;
unexpected returned keys/version require declaration review. The editor never retries
mutations and clears selected rows after outcomes. Key/concurrency changes contribute
to fingerprints and compatibility checks; removing/changing established metadata is
breaking for dependent consumers. This adds no owner-SQL mutation route or lifecycle.

The local Data API inspector offers Parent and Children actions for exposed foreign keys, including composite keys. Each action reads related rows with every key component under the selected identity and database RLS. Nullable, incomplete, unsupported or unsafe keys have no navigation action. The Back trail restores filters and pagination and re-fetches rows; changing identity or schema clears it. Navigation sends reads only and does not populate write forms.

Local inspector relationship views are versioned project JSON definitions,
separate from application-specific personal/team saved views. They retain named
FK paths and per-step query choices, not selected row predicates or identities.
Reopening uses fresh selected-identity reads and user-selected rows for every
relationship hop. Exact schema fingerprints gate reopening and pin all workflow
reads; stale definitions require review and reconstruction. The existing local
capability, origin guards and bounded atomic revision store protect collection
reads/writes without adding hosted services or database migrations.

Guided relationship RLS checks use an additive version 3 access manifest accepted
by the existing local verifier. Scaffold generation uses only saved view metadata
and configured identity names; synthetic fixture keys and counts require explicit
customer review. Composite key pairs must match across fixture bindings, and every
step requires a positive expectation. All identity/step reads execute independently
so denied parent reads do not mask child-policy regressions. The local capability
protects scaffold storage and execution; reports omit row payloads and fixture
values. This adds no hosted service or schema migration and preserves version 1/2
request scenarios for write/admin/revocation assertions.

Guided RLS mutation testing is an additive version 4 access manifest accepted
only by disposable CLI verification. Inspector export provides an unreviewed
scaffold; its save/run endpoints remain version 3 and read-only. User-reviewed
payloads use pinned PostgREST endpoints under application identities with minimal
responses, avoiding SELECT-return checks that could hide ownership-transfer
policy gaps. A separate local owner connection observes complete rows from
bounded physical exposed tables in read-only repeatable-read transactions.
Denied writes require unchanged row-state fingerprints; counters and tables
outside the exposed contract are excluded. Passing read baselines and precise
fixture cardinalities precede writes; failed or uncertain outcomes stop remaining
mutations. This adds no hosted mutation runner or database migration.

Version 5 local authorization lifecycle manifests extend ordered disposable
mutation checks with GET visibility checkpoints. Scaffolds leave actor identities,
policy data models and outcomes for review; read baselines still precede changes.
Previously issued identity JWTs are reused throughout the run without refresh,
allowing customer-defined expectations to detect stale permission claims.
Private observation requires each checkpoint's data fixture to remain present
and row state unchanged. Failed reads or uncertain writes stop subsequent steps.
Version 4 remains mutation-only, legacy access checks remain supported, and
inspector save/run endpoints remain version 3; version 4/5 export adds no hosted
service or live mutation execution path.

Local inspector CSV imports reuse the generated insert descriptor model through
pure client-side conversion and validation. Only known writable columns can be
mapped; invalid input blocks all writes and changes invalidate approval. Approved
rows use selected-identity pinned PostgREST inserts in bounded atomic batches,
with exact affected-row counts needed for confirmation. Known transaction
refusals are distinct from uncertain outcomes; only refused/unsent rows are
eligible for newly approved retry previews. Payloads/retry state stay in browser
memory; outcome exports omit data values and tokens. This adds no hosted import
service, automatic retry, schema migration or identity/ownership inference.

Inspector CSV exports use the existing contract-pinned REST gateway and identity,
not a new server export endpoint. The browser validates the selected projection
and live relationship scope, adds primary-key sorting ties where available, and
bounds ordered page reads, response bytes, elapsed time, and total output. Export
responses use a bounded exact-number JSON decoder so PostgreSQL numeric values
are not silently rounded. JSON-cell CSV encoding integrates with the reviewed
import wizard; imports reject unrepresentable JavaScript numbers. This remains
local tooling and does not change VM provisioning or hosted data services.

Selected-row bulk writes remain local inspector tooling over the existing
contract-pinned REST API. Each row uses the declared primary key and integer
version as predicates, commits independently, and requires a validated explicit
preview and approval. Confirmation checks returned keys and versions; ambiguous
transport/response outcomes stop the run and are excluded from retry. No new
server bulk endpoint, VM changes, or implicit filter-wide mutations are added.

Matching-view bulk writes collect a bounded complete list of keys and versions
through live ordered REST reads under the current identity and relationship
scope. The preview rejects truncation rather than implicitly approving a
partial set. Approval freezes the collected list; writes reuse the existing
per-row guarded workflow instead of sending a filter-wide PATCH/DELETE. This
adds local inspector functionality without new server services or VM changes.

Bulk-operation reports are constructed from an explicit metadata/outcome
allowlist and downloaded locally as versioned JSON. They omit row keys,
versions, write payloads, read predicates, and credentials. Immediate parent
run IDs and index mappings preserve retry lineage without executable manifests.
The latest completed report survives browsing-context changes in the inspector
and is supplemented by the bounded redacted project history described below.
Write snapshots are never persisted to project files or browser storage.

Bulk write approval is gated by bounded read-only preflight over each frozen key
and reviewed version. Preflight verifies current metadata and RLS-visible reads,
not hypothetical write permissions or trigger execution. Successful checks expire
after 60 seconds; failures discard the preview without silently refreshing
versions. Writes retain version predicates to handle races after successful
checks. This adds no server dry-run transaction or new hosted service.

Inspector reconciliation performs bounded reads against original conflicting or
uncertain keys and compares fresh versions and values with the reviewed changes.
It never upgrades uncertainty from observational evidence or adds uncertain rows
to ordinary retries. A selected visible row can enter a separate follow-up plan
with a fresh version, explicit uncertainty acknowledgment, preflight, and approval.
Reports use distinct reconciliation lineage while retaining their redacted format.
This remains local inspector tooling over existing contract-pinned REST reads.


Bulk-operation history persists a strict allowlist of redacted reports in a
bounded project file, using private atomic writes and optimistic revisions.
Retention bounds report count, age, and bytes without truncating outcome lists.
Keys and reviewed values remain in bounded tab-local snapshots only; reopening
requires matching schema/model and the original identity subject, and creates no
approval or writes. Explicit clear removes the project history and current-tab
snapshots. Persisted reports cannot reconstruct or replay operations.

Read-only support identities use a trusted boolean JWT claim enforced by the
existing gateway before mutation forwarding; the SQL role remains the configured
binding login. Saved support views bind to a named read-only local identity and
retain projections and filters as UI organization, not authorization. A private
base-table example uses restricted column SELECT grants, no base write grants,
SELECT RLS and security-invoker public views for database enforcement. Bounded,
privilege-filtered private origin metadata can infer FK edges between exposed
views without exposing private table or column contracts. No new SQL role switch,
VM lifecycle, deployment service, or production support-login portal is added.

The customer-facing support console is an independent stateless Node application
template over the existing Data API. It performs OIDC authorization-code/PKCE
sign-in, verifies issuer/audience/staff claims, seals short-lived staff tokens in
authenticated encrypted HttpOnly cookies, and forwards only approved read queries
against a pinned contract. Database grants/RLS remain authoritative. A synthetic
issuer/data demo and authorization tests exercise the application separately from
the privileged local inspector. No VM lifecycle, identity-provider provisioning,
automatic deployment, refresh-token flow or shared database role switching is
introduced.

ADR-1730 adds opt-in authenticated resident pool observations to the connection
budget and health report. It preserves estimates for parked/future runtimes and
keeps full consumer coverage and aggregate enforcement unqualified. See
[the runtime observation decision](1730-data-api-runtime-connection-demand.md).
