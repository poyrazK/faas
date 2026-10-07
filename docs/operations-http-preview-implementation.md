# HTTP Operations preview implementation

The staged release starts with ordinary HTTP handlers under ADR-521. The
contracts and initial SQL ledger landed in #3935 and #3943. Private result
storage receipts landed in #3951 as `2fc339ede`. Customer admission remains
disabled while the HTTP execution, ingress, and SDK slices are qualified.

## Durable state slice

The state layer commits an invocation, owner-scoped operation, idempotency
receipt, and initial event together. Equivalent JSON submissions share an
identity across definition revisions; conflicting input is rejected. Progress
and result attachments use current invocation, instance, attempt, lease, and
capability authority. Backend outcome and completion webhook delivery have
separate state, backed by the existing webhook outbox.

An expired uncertain dispatch enters reconciliation. Confirmed outcomes and
an explicitly authorized safe retry have fenced recovery receipts. Active work
keeps its identity and code through long waits. Separate private code pins and
owned release references protect cleanup and rollout without enlarging public
revision grants. File cleanup receipts survive owner deletion.

This slice adds transactional methods and invocation lifecycle hooks. The HTTP API and manifest slice adds route registration and typed SDK contracts.
The local continuation adds HTTP execution propagation, private result
copy/download services, frontend subscription helpers and bounded admission
configuration. Fleet rollout qualification remains outstanding. A state-layer pass alone does not
qualify a customer preview rollout.

## Managed-operation compatibility

[PR #4133](https://github.com/poyrazK/faas/pull/4133), reviewed at
`6d79fa09b18e1d3e8603e04eb4a7b7cfab781fa8`, adds managed operation business
transactions and named webhook effects. The HTTP preview preserves its
execution contract:

- Customer claims carry `X-Gregale-Customer-Operation-Id`, plus the customer
  attempt and capability headers. Managed exclusive execution keeps
  `X-Gregale-Operation-Id` and its result-version negotiation.
- The ordinary HTTP adapter validates the complete response body. It does not
  implicitly unwrap `gregale_operation_result` or create a managed exclusive
  operation from a customer operation identity.
- Completion notification and named business effects can share webhook
  transport while retaining their independent authorization and delivery
  policies. Retrying delivery does not regenerate a business result.
- The explicit `transaction_receipt: postgres_v1` HTTP adapter (ADR-599) binds
  customer-owned transaction receipts to the captured operation contract and
  negotiates separate customer receipt headers. Its SDK helpers return ordinary
  typed result bytes and support evidenced recovery without repeating committed
  database writes. They retain COMMIT uncertainty and platform completion fences;
  managed transaction helpers do not infer support from a frontend header.

Acceptance tests cover the header boundary and preserve the underlying
completed invocation when a managed envelope violates an ordinary output
schema. The subsequent ingress and SDK slices must also qualify reserved-header
stripping and negotiated result handling.

## Source and qualification

The HTTP state methods and eleven acceptance suites were extracted from
`fbbde67fda1daf4e396b407b95dd692de49fca1c`, then reconciled with main. Private
code-retention hardening and its tests come from
`e0ee42d8df5fb5bb782ea5ee890ffd8e9c0a3138`. Only HTTP-compatible named queries
and state methods are included. SQLC generates all database bindings.

Local qualification passed on 2026-10-04: 39 PostgreSQL/MemStore acceptance
groups, 102 state and migration regression groups, and 822 API groups, with the
race detector and no skips. Coverage includes replay of both new migrations,
ordinary invocation behavior, rollout and rollback, long-running retention,
and the managed-contract boundary. Linux lint, SQLC v1.31.1 reproduction and
static policy checks passed. The qualification receipts and source hashes are
retained under `operations-checks-20261001/staged-release-20261004/evidence`.

The state slice landed in [PR #4173](https://github.com/poyrazK/faas/pull/4173)
on 2026-10-04 as `b0cf1feb178ae9383438d3616fe73c40ca59c133`. Its final
source head passed 30 applicable checks, with one verified non-applicable
base-image skip; aggregate exact-package state coverage was 73.8% against the
unchanged 70% floor. Before enabling the
preview, real-handler HTTP and SDK acceptance must pass with the default
admission switch off, a bounded opt-in path, and documented rollback. The code-retention migrations use fresh generated IDs
`20261004123536799` and `20261004123650815`. They replace this slice's former
IDs claimed by preserved drafts #3973 and #3979. Those drafts must be reconciled
before landing any duplicate schema changes.

The original full implementation worktree remains preserved with its pending
main merge. Native Job and workflow adapters, KVM qualification, and leakcheck
remain part of that separate paused release scope.

## HTTP API and manifest slice

The next slice exposes immutable HTTP definitions, account-owned status and
cancellation, and tenant-owned submission, status, cancellation and event pages.
The events route also provides resumable SSE with bounded stream leases, periodic
credential reauthentication and durable cursor replay. PostgreSQL notifications
are wake hints; a missed hint falls back to polling durable state. Public status
omits original input and execution credentials. Delivery state stays separate
from the business result. Account routes use the existing MFA and scope boundary.
Only tenant bearer routes have browser CORS, without credentialed cookies.

Source deployments resolve `operations` declarations and source-local JSON
schemas from the selected immutable archive. A single archive pass retains only
the selected app's bounded schema files; duplicate files, symlinks, missing files
and invalid schemas fail the bundle. Definitions install before a build becomes
claimable. Retrying a deployment retains its immutable contract and creates new
deployment pins. Existing deployments without Operations keep their current path.

**Production admission remains closed.** The original API slice qualified a
private admission field defaulting to false, without an environment, flag or
startup configuration path. Definition registration and customer submission returned 503, and
source deployments containing selected Operations definitions fail before source
publication or deployment creation. Tests enable the private field to qualify
the contract. Retained status, events and cancellation remain available with the
gate closed. The local continuation below adds the bounded activation path;
native qualification is still required before customers can submit work.

The canonical and embedded OpenAPI documents initially defined seven routes.
The Go API client and generated Node/Python SDK contracts cover them.

## Local HTTP execution continuation — 2026-10-05

Draft [PR #4208](https://github.com/poyrazK/faas/pull/4208) was closed at the
user's request, unmerged, with its branch preserved. This continuation is local;
no replacement PR or production activation is authorized yet.

The local implementation adds five routes: workload-authenticated progress and
artifact reporting, account reconciliation, and separate account/customer result
downloads. Workload assertions must identify a running instance of the owning
app and use the dedicated Operations audience. The invocation attempt and
capability are checked against durable state before reporting and again when an
artifact is attached. Native execution context is rejected by the HTTP adapter.

The scheduler binds the actual instance before invoking a handler, renews its
claim throughout wake and execution, and cancels dispatch when renewal fails.
Pre-dispatch binding failures can retry without treating the handler as having
run. Lost authority after dispatch leaves uncertain effects for reconciliation.
Public ingress strips both reserved operation header namespaces; the synthetic
adapter restores only the trusted customer claim. At this runtime checkpoint,
operation ingress wiring was absent from production startup.

Result attachments verify managed-object ownership, environment, size and digest,
then reserve and write a private copy. Interrupted writes retain cleanup receipts;
a retry uses a distinct key. Downloads verify retained bytes and current operation
state before responding, without reading a mutable customer source as a fallback.
Cleanup continues while admission is closed. Completion webhook retries retain
their separate delivery state and do not rerun the business handler.

The browser Node client refreshes credentials, resumes durable SSE cursors and
surfaces resync snapshots. Node and Python HTTP helpers isolate runtime authority
per request, fetch workload identity for each report and omit capabilities from
public context. The standalone Go client supplies stable submission keys,
runtime proof redaction, typed recovery and verified downloads. Generated Node
and Python contracts and the embedded spec cover all twelve routes.

Portable qualification passed 2,535 top-level test groups with the race detector
and no skips across the HTTP/API, ingress, authority, scheduler and synthetic
execution boundaries. Seven additional environment-contract groups passed,
including generated-document parity. Scoped Go lint and SDK route coverage passed;
the canonical and embedded OpenAPI documents match.

The standalone Go SDK passed its complete race suite and daemon wire-contract
parity check. Node passed 97 SDK tests, eight generator tests and one application
fixture with no skips. Python passed 205 tests; its unrelated PostgreSQL commit
fixture skipped without `DATABASE_URL`, and the regeneration-marker test was
excluded by the suite's default selection. The five Operations Python tests passed
without skips, and regeneration preserved the runtime helper.

An owned PostgreSQL fixture with real Node and Go SDKs passed the HTTP execution
acceptance scenario on that checkpoint's local source. It exercises a real export handler, claim renewal, scoped
progress, durable event replay, duplicate submission, retained downloads after
source deletion, and separate completion-delivery retries. The fixture replaces
the VM bridge with local HTTP; it does not qualify native boot, park/restore or
leakcheck. The final exact-source run passed on 2026-10-05; earlier attempts stopped
by the shared-disk guard remain preserved and are never counted as passes.
Test receipts, source hashes and the local checkpoint belong under
`outputs/operations-http-api-20261004`.

That runtime qualification is retained as
`local-http-runtime-qualified-20261004T221138252073Z.json`; it predates the
admission/configuration changes below. Its source hashes do not qualify those
later changes. Native Job/workflow adapters and the full implementation
worktree's pending merge remain paused and untouched.

## Local bounded admission continuation — 2026-10-05

apid and gatewayd-internal now accept an absolute
`operations_preview_policy_path` in TOML, empty by default. A regular JSON file
grants exact account/app/environment cohorts and individual authenticated
platform tenants, with a UTC window capped at one hour. Bounds are 64 KiB,
ten cohorts and ten customers per cohort. Unknown fields, duplicate JSON members,
wildcards, invalid IDs, excessive bounds, expiry, file removal and unsafe write
permissions close admission. Each request reads current policy, without an open
cache or a broad boolean production override.

Definition registration, source enqueue and source retry retain the existing
plan and ownership boundaries and require the allowed environment. Customer
starts additionally require the allowed tenant, workload trust and private
artifact storage. Source manifest policy is checked before applying changes.
Startup always installs the gateway resolver when durable storage is present;
closed declared routes cannot escape into ordinary execution or protocol
upgrades. Ordinary GET requests keep their current path. Mutating ingress now
resolves operation metadata even with admission closed; native performance
qualification must cover that cost and metadata-store availability.

Rollback atomically replaces the policy with version 1, enabled false. Accepted
executions, reports, retained reads/downloads, delivery, recovery and cleanup
continue with their existing fences. An admission decision made before replacement
may still commit; there is no fleet-wide atomic policy barrier or automatic
cancellation. The [preview runbook](ops/operations-http-preview.md) requires
closure checks on each serving node and separate inspection of accepted work.

The broader local race selections passed 1,738 unique top-level groups across
the complete Operations contract/ingress/source-enqueue packages and the selected
API, source, configuration, startup, recovery and synthetic-execution regressions.
One existing source-scanner file-descriptor test skips on macOS because it cannot
enumerate per-thread descriptors; it still requires Linux validation. The bounded
admission PostgreSQL fixture passed with real Node and Go SDKs: it closes admission
before dispatch, finishes the accepted export with progress and a private result,
and retries completion delivery without regenerating work.

Later changes only lowercase three startup error-message prefixes to satisfy
staticcheck. Their before/after hashes and exact textual difference are retained.
Final focused race builds include every platform-selected apid production source
and only the necessary admission/SDK fixture files, reducing the temporary build
footprint while preserving the real handlers. They qualify the current source;
the broader selections are linked through the explicit message-only difference.
The SDK and public wire sources match the prior runtime checkpoint.

Final focused admission/ingress/source race checks and the PostgreSQL fixture
passed on the corrected source. Scoped golangci-lint v2.4.0 passed with zero
issues, using the pinned Go 1.25.13 toolchain and existing race exports. Formatting,
whitespace, embedded OpenAPI parity, handler size limits, encoding, shell quoting,
ADR uniqueness and runbook SQL checks passed. The immutable local admission
checkpoint and full source manifest are retained under
`outputs/operations-http-api-20261004/local-preview-admission-qualified-*.json`.
Disk-guard stops and the initial focused fixture assembly failure are preserved
and are not passes. Other builds remained running.

No cohort is installed in production, no replacement PR is open, and no native
lifecycle pass is claimed. A dedicated
x86_64 Linux KVM run of native acceptance and leakcheck, plus fleet rollout and
rollback qualification, remain necessary before a customer preview.

## Local customer history and export continuation — 2026-10-05

Customers can now rediscover retained work without keeping operation IDs in an
application table or browser storage. The tenant-self collection GET requires
`platform_tenant:operations:read`, an explicit app UUID and environment scope.
Ownership comes from the verified tenant credential. Optional name/state filters
and bounded pages use a creation-time/UUID keyset cursor bound to account, tenant,
app, environment and filters. Page defaults are 20, the maximum is 100, and cursor
text is capped at 512 bytes. Progress updates and new arrivals do not shift an
existing page watermark. Membership remains a live view rather than a snapshot;
retention expiry and state changes can remove filtered rows.

MemStore and PostgreSQL expose the same narrow summaries, excluding submitted
input, result bodies, artifact locations, delivery IDs/errors and runtime
capabilities. Pending work remains discoverable; expired settled work is omitted.
The PostgreSQL query projects public fields and current completion delivery in
one SQLC query, using the existing tenant creation index. This continuation adds
no schema migration. History, status and retained downloads remain readable when
new-operation admission is closed.

Go, generated Node/Python and the browser-safe Node client include typed history
calls. The browser client's default fetch now retains its global receiver; real
browser verification found and corrected an illegal-invocation failure that Node
transport fixtures could not expose.

`examples/customer-operation-export` combines a typed ordinary HTTP handler,
progress, server-owned history, selection/subscription, cancellation intent and
retained download. Business completion and notification retry appear separately.
Fresh sessions rediscover existing exports; notification errors never submit work.
Lost POST responses retain an in-session key and payload, concurrent clicks share
one submission, and a failed follow-up status read preserves confirmed acceptance.
Sign-out aborts old reads/streams and fences late submissions/downloads.
The example generates bounded CSV rows, uses the repository's internal SDK, and
accepts a short-lived tenant token through a demo form. Application login/token
issuance and standalone SDK packaging remain application integration work; this
is a repository example, not a published or registered CLI template.

The exact-source PostgreSQL acceptance passed with the real Node SDK and this
example's session controller: history rediscovers the retained export after
admission closes, controller download reads its private retained artifact after
source deletion, and completion retry leaves the business execution count at
one. Both MemStore and PostgreSQL pagination acceptance passed under the race
detector. Focused state history race tests, standalone Go SDK race tests, 99 Node
SDK tests plus eight generator tests, seven export session/server tests, and the
Python suite (206 passed, one unrelated PostgreSQL-dependent skip) passed. The
final generated Python Operations contract selection passed again after generator
reproduction. Generated Node/Python sources reproduce deterministically; SQLC
reproduction copies all three state query inputs. Browser checks confirm fresh
sign-in discovery, separate delivery state and clearing customer data on sign-out.
The browser's download-event observer timed out; the actual controller download
is qualified by the PostgreSQL acceptance instead.

Full scoped golangci-lint v2.4.0 passed with zero issues, including package
tests, on the pinned Go 1.25.13 toolchain. SDK route coverage, OpenAPI route/DTO
parity, embedded-spec equality, encoding, shell quoting and whitespace checks
passed. The real manifest/schema compiler also accepts the example handler's
maximum 1,000-row output and rejects an oversized input. Final static receipts,
the full changed-source manifest and browser screenshot are retained under
`outputs/operations-http-api-20261004`. Disk-guard interruptions and fixture/check
failures are preserved and never counted as successful qualification. Only owned
disposable cache files and temporary fixtures were cleaned; unrelated builds
continued running. Native KVM/leakcheck and fleet rollout/rollback qualification
still gate customer activation. No PR, push, merge, deployment or cohort activation
was performed for this continuation.

## Local backend operator APIs and CLI — 2026-10-05

Owning accounts can now list customer work by app and explicit environment,
optionally filter by tenant, and inspect durable events and retained execution
generations. Account history uses the same bounded summaries, retention and
creation-time keyset ordering as tenant history, with account-specific cursor
binding and optional tenant IDs. The new execution projection includes the
ledger's aggregate attempt count without inventing per-attempt outcomes or
exposing submitted input, invocation headers or reporting authority. A new
append-only migration adds an account/app/creation index.

Account routes retain read/deploy scope and MFA checks. Delivery retry resolves
the immutable completion destination and atomically resets only a dead delivery;
concurrent requests have one winner. Business results and execution generations
stay unchanged. Account event history is bounded JSON, and tenant SSE authority
remains separate. These reads and operator decisions work after new-operation
admission closes.

The [customer Operations CLI guide](ops/customer-operations-cli.md) documents
`gregale customer-operations list|get|events|executions|watch|download|cancel|recover|retry-delivery`.
Watching emits changed snapshots, supports NDJSON, and exits on business state
independently of notification state. Local timeout and interruption leave work
running. Downloads verify retained metadata, size and SHA-256, then publish a
private temporary file atomically without overwriting existing paths. Recovery
requires an observed generation, stable decision ID, explicit resolution and
bounded regular-file evidence; succeeded results must match the original output
schema. The existing exclusive-work `gregale operations` commands retain their
behavior. No frontend source changed in this continuation.

Focused CLI race tests passed, including file/symlink guards, download integrity,
watch exit states, recovery receipts, completion metadata and exclusive-operation
compatibility. Current-source state history, cursor isolation, execution
projection and cleanup race tests passed using a temporary test-only overlay
that retained all production source and the selected test files. This is focused
qualification, not the complete state test suite. Real PostgreSQL operator/API
acceptance and the shared MemStore/PostgreSQL history, lifecycle, recovery and
admission selections passed under the race detector, including reads after
admission closes, ownership boundaries and delivery-only retries.

The complete standalone Go SDK race suite, Node SDK and generator tests, and
the selected Python Operations contract/runtime tests passed. Node/Python
generation reproduced identically across two runs. SQLC reproduced from all
three query inputs; embedded OpenAPI equality, route/DTO and SDK coverage checks,
OpenAPI lint, encoding, shell quoting, ADR uniqueness, formatting and whitespace
checks passed. Golangci-lint v2.4.0 reported zero issues for changed lines in the
new production code, loading the affected production packages with tests
disabled. Repository lint configuration and CI gates were not changed.

The broader state package compilation was killed on this shared Mac, and the
full test-enabled lint loader timed out. These attempts are preserved as failed
or incomplete checks and do not qualify the full suite. Immutable logs, source
manifests and the final checkpoint are retained under
`outputs/operations-cli-20261005`. Only owned disposable test/cache files were
cleaned; other builds remained running. Native KVM/leakcheck, full CI and fleet
rollout/rollback qualification still gate customer activation. No commit, push,
PR creation, merge, deployment or cohort activation was performed.

## Local developer workflow for backend and CLI — 2026-10-05

Deployment-scoped account reads now expose sorted immutable definition metadata
and individual full contracts, including bundled schemas and revision pins. App,
account and deployment ownership are checked before discovery. Definition reads
remain available after new admission closes. Go, Node and Python SDK contracts
include these routes and the tenant identity projection.

`gregale customer-operations validate` compiles YAML/TOML source declarations
with the same manifest parser, plan limits and schema compiler as deployment.
It works offline, requires an explicit target plan and can validate sample input
for a selected definition. Source reads are bounded and anchored to the selected
root, rejecting symlinks and non-regular files.

`start --self` verifies server-derived tenant identity, saves a private immutable
request receipt before submission, and separately records confirmed acceptance.
The request binds the API origin, account/tenant identity, original definition,
canonical input and stable idempotency key. Lost-response replay retains those
values; changed requests and identities are rejected. Concurrent callers share
exclusive publication and server idempotency. Confirmed acceptance returns the
original ID without another submission. Unconfirmed replay is conservatively
bounded to the shortest enabled-plan idempotency window, currently 30 days.
Tenant `get`, `events`, `watch`, `download` and `cancel` reuse the existing CLI
behavior through the tenant-self routes. Account recovery remains separate.
The [CLI guide](ops/customer-operations-cli.md) documents the complete workflow.

Focused CLI race tests passed, including concurrent submission, lost responses,
maximum-depth JSON, payload conflicts, identity changes, receipt privacy and
corruption, expiry, source escapes, tenant routing and verified downloads. Real
MemStore/PostgreSQL operator API acceptance and focused shared-store history,
lifecycle, recovery and admission tests passed under the race detector. The
shared-store selection overlapped a CLI-only edit; its relevant store/API source
hashes remain unchanged. Complete standalone Go SDK race tests passed.

Node/Python generated routes and models reproduced identically across two runs;
101 Node SDK tests, eight generator tests and one development-bridge test passed.
Python's full unit selection passed with 214 tests, one unrelated PostgreSQL-
dependent skip and one deliberately excluded regeneration test. The generator's
error path had discarded hand-written helpers; all helpers were restored, with
the uncommitted Operations runtime matching its prior recorded SHA-256 exactly.
Generation now restores hand-written source and project metadata on success and
failure, retaining a recoverable backup if restoration fails. Regression tests
cover failed backup, partial cleanup, normalization, generator failure,
postprocessing and failed restoration. Generator reproduction preserved every
hand-written helper byte for byte.

SQLC reproduction used all three query inputs; embedded OpenAPI equality, OpenAPI
lint, route/DTO parity and SDK coverage passed. Changed-production-line lint passed
with tests disabled. The broader production-only lint attempt reported a
pre-existing helper used only by tests; it is preserved as a failed check rather
than presented as full-package qualification. Formatting, encoding, shell
quoting, ADR uniqueness and whitespace checks passed. Immutable logs and source
receipts are retained under `outputs/operations-developer-cli-20261005`.

This continuation changes backend, CLI and SDK code only. Frontend and the
exclusive-operation CLI source are unchanged. Native KVM/leakcheck, full CI and
fleet rollout/rollback qualification remain activation gates. All work stays
local; no commit, PR, push, merge, deployment or cohort activation was performed,
and unrelated builds continued running.

## Local backend and CLI diagnostics — 2026-10-05

`gregale customer-operations doctor` now reads submission prerequisites for an
explicit owned app, deployment and tenant, optionally selecting one definition.
The account read/MFA endpoint returns sanitized reason codes, remediation,
immutable pins and a timestamp scoped to the responding API node. It uses the
same current-file preview decision as admission and the existing compiler and
pending-count query. No SQL, migration, execution or recovery mutation is added.
The report is bounded to 1,024 checks and a ten-second request context.

Submission blockers and unknown prerequisites determine observed eligibility.
Completion destination warnings remain independent. Workload trust and result
storage presence are configuration observations; runtime reporting, storage I/O,
gateway admission, native lifecycle and fleet rollback remain explicitly
unverified. The endpoint reserves no slot, opens no cohort and makes no external
probe. Metadata can change during and after these reads. CLI human and JSON
output expose these distinctions and reject incomplete or mismatched reports.
The [CLI guide](ops/customer-operations-cli.md#diagnose-submission-prerequisites)
documents selectors, authority, remediation and exit codes.

Focused CLI and preview-policy race tests passed, including current policy
failure, separate delivery warnings, invalid reports and exclusive-operation
compatibility. Current-source MemStore/PostgreSQL operator API tests passed
under the race detector, covering ownership, closed-admission reads, pending
counts including reconciliation, tenant suspension, plan downgrade, unusable
revisions and release pins. Route/DTO parity, including the new diagnostic DTO
scanner registration, passed. The complete standalone Go SDK race suite passed.
Focused shared MemStore/PostgreSQL history, lifecycle, recovery and admission
race tests also passed.

Node/Python generation reproduced identically across two current-spec runs and
preserved all hand-written helpers. Node passed 102 SDK tests, eight generator
tests and one development-bridge test. After correcting its new diagnostic
fixture's string-enum assertion, Python's full unit selection passed with 215
tests, one unrelated PostgreSQL-dependent skip and one deliberately excluded
regeneration test. SQLC reproduced from all three query inputs; embedded OpenAPI
equality, OpenAPI lint and SDK coverage passed. Changed-production-line lint
passed with tests disabled; this is focused qualification, not full repository
CI or the complete state suite.

Shared-disk pressure stopped two final CLI race attempts and interrupted one SDK
regeneration. Those logs remain preserved. The generator retained its backup;
all 16 hand-written/project files were restored byte for byte, then successful
reproduction preserved them again. Only older disposable artifacts in this task's
Go cache were cleaned. No unrelated build was interrupted, source or evidence
removed, or database reused. The original Python Operations runtime SHA-256 and
prior example/browser source remain unchanged. Immutable source and qualification
receipts live under `outputs/operations-doctor-20261005`.

All changes stay local. Native KVM/leakcheck, full CI and fleet rollout/rollback
qualification remain customer activation gates. No commit, PR, push, merge,
deployment or admission activation was performed.

## Local completion delivery evidence and retry receipts — 2026-10-05

Account read/MFA routes now expose delivery observations and bounded attempt
history. They separate the retained business outcome from notification state,
replay generation, response status, next attempt and receiver cooldown, without
raw receiver errors, URLs, payloads or external probes. Ownership checks cover
operation, app, immutable destination, event and embedded operation identity.
The CLI exposes these reads as `delivery` and `delivery-attempts`.

The new deploy-write/MFA `/delivery-retries` route atomically records an immutable
decision and resets its observed dead delivery. Same-ID replay returns the original
receipt after later failure or transport pruning; changed payloads and stale
replay generations conflict. Distinct IDs at one generation have one winner.
At most 32 decisions are retained per operation, with cascading parent retention
and no transport foreign key. Business state, result, invocation and execution
generation stay unchanged. Dispatcher policy and receiver cooldown still apply.
The legacy reset API remains compatible.

`retry-delivery` now publishes an immutable bounded 0600 fsynced request before
mutation, verifies current API/account identity, and stores a separate digest-bound
`.queued.json` acknowledgement. Lost replies resume that exact request. Unsafe
files, conflicting selectors, invalid acknowledgements and expired unconfirmed
receipts fail closed. Concurrent decoding uses independent storage. Queued is the
recorded decision, not live notification status. Go, Node and Python SDK contracts
expose all three routes.

Focused MemStore/PostgreSQL operator API and shared-store regression checks passed
under the race detector. They cover duplicate and competing decisions, stale and
changed requests, ownership, quota, pagination, sanitized evidence, replay after
later failure and transport cleanup, and unchanged business work. CLI race checks
passed for lost replies, identity binding, concurrent receipt use, size bounds and
unsafe-file refusal. The complete standalone Go SDK race suite passed. Node passed
103 SDK tests, eight generator tests and one bridge test; Python passed 216 unit
tests with one unrelated PostgreSQL skip and one excluded regeneration tripwire.
Two current-spec generator runs reproduced identically and preserved hand-written
helpers. SQLC reproduction used all three SQL inputs. OpenAPI/embedded parity,
specification lint, SDK coverage and focused changed-production-line lint passed.
These checks qualify the local slice, not full repository CI or complete state
coverage. An unchanged earlier test-only artifact helper remains outside delta
production lint.

Validation fixed PostgreSQL timestamp precision/timezone replay differences and
a shared generation-pointer decode race. Failed and disk-stopped checks remain
preserved; optional repeat checks blocked by later disk pressure add no passing
claim. Only disposable Operations cache artifacts were removed. A temporary owned
RAM disk supplied build scratch; the cache was restored and the RAM disk removed.
Other builds, source, databases and evidence were preserved. Qualification/source
receipts are under `outputs/operations-delivery-20261005`. The previous schema
prefix, example/browser code and Python Operations runtime remain unchanged.
No commit, PR, push, merge, deployment or activation was performed. Full CI,
native KVM/leakcheck and fleet rollout/rollback remain activation gates.

## Backend and CLI release preparation — 2026-10-05

The user authorized releasing the accumulated local backend, CLI and SDK work.
A fresh release branch preserves the closed #4208 branch and its historical
qualification. The release includes ordinary HTTP execution, private result
artifacts, scoped history and recovery, developer/operator CLI commands,
submission diagnostics, and completion-delivery inspection with durable retry
receipts. Existing customer-operation state foundations remain in main.

Admission defaults closed. No preview policy, customer cohort, daemon
configuration, deployment or native rollout is installed by this release.
Portable local qualification receipts remain preserved with source hashes;
integration with current main and current-head Linux CI must qualify the combined
source. Native KVM, leakcheck and fleet rollout/rollback remain activation gates.

The release integration preserves main `debf579635a7ea9f6c4e5c6650bb6b24f8222e2e`,
including managed-operation transactions, environment clones and object-version
protection. Customer HTTP SDK modules now use distinct filenames where main
introduced managed-operation modules. The incoming managed implementations are
byte-identical after resolution. SQLC reproduced all current configured inputs
and outputs; the current main schema is retained exactly with the two additive
Operations schema changes appended.

Current integration passed complete Go SDK race tests, Node unit/generator/bridge
tests, focused CLI and preview-policy race tests, OpenAPI/DTO/SDK coverage and
static checks. Python unit tests passed after preserving the incoming managed
exports. Current-source SDK reproduction is qualified separately. A broader
scheduler test build exhausted owned temporary space; no scheduler runtime
qualification is claimed from that attempt. Other builds remained running.
Linux CI now explicitly enables the portable PostgreSQL/HTTP/SDK acceptance
fixture in the existing API shard and builds its Node SDK first. This test uses
a local HTTP bridge and does not qualify native VM lifecycle or activation.
The exact-package state coverage floor remains 70%.

The final Python SDK reproduced identically across two isolated current-spec
generator passes while preserving all seventeen handwritten helpers. Its unit
suite passed with 221 tests, four PostgreSQL-dependent skips and the separate
regeneration tripwire excluded. Node passed 106 unit tests, eight generator
tests and one bridge test. The production CLI generated the current reference,
including the complete customer-operations command group. Local interrupted
builds and their disk-guard receipts remain preserved as stopped attempts.

A further clean merge retained main `85b50d0c3d630258ac96ec608d559e0d5dff92c2`,
including its deployed ingress/worker settings and release-policy invalidation
migration. No Operations source was replaced by that incoming change.

Release CI identified four bounded integration gaps: delivery retry receipts
needed an operational clone-schema policy, execution history needed its checked
integer conversion inside the bounds helper, the OpenAPI response test assumed
an obsolete byte offset, and the new retry migration needed replay-safe creation.
The fixes preserve receipt isolation, reject invalid watermarks before lookup,
check the complete embedded specification, and tolerate a missing migration
ledger entry after the same DDL has already applied. No merged migration was
edited. Focused race tests and migrated PostgreSQL validation qualify these
changes separately; final current-head repository CI remains the release gate.

## Job Operations feature starter — 2026-10-06

`gregale init --template customer-operation-job-export` now materializes an
independently installable feature using the packed public Node SDK. The same
source lives in `examples/customer-operation-job-export`, with an enforced
example/embed parity check. It includes a customer-owned Linux/amd64 Job image
recipe and command, Job-targeted manifest, input/output schemas, progress and
cooperative control, verified private CSV preparation and typed results. Its
browser uses the existing authenticated session and durable receipt store for
submission, restoration, history, progress, cancellation and private downloads.
Business success and completion delivery remain separate.

Job overrides carry public configuration only. A bounded application upload
endpoint verifies the current native task proof with the existing tokenless
control route, checks app/environment/tenant/generation authority, validates
size/checksum and rechecks authority before one managed PUT. Storage credentials
remain in sealed app secrets. Source keys include tenant, operation, generation
and run. Upload uncertainty never automatically repeats a source write. The
SDK can replay private-copy/result acknowledgements and reuse a retained copy
without its source. The browser exposes neither native proofs nor storage
credentials. Prepared private files require confirmed native exit or explicit
account-authorized success recovery before publication.

CLI discovery, metadata and generated reference include the starter. Unprepared
`init --deploy` and direct template deployment reject before writing/deploying;
the README covers app reservation, ready Job image and private bucket setup,
definition selection, tenant login, delivery and evidenced recovery.

Both HTTP and Job starters passed isolated packed-SDK installation and browser
module-graph checks. The Job starter's ten portable checks and 62 focused SDK
control/session/receipt/artifact tests passed. Focused CLI/source-pack/manifest
and template race checks passed, with generated-reference and example parity.
These transport fixtures do not qualify native execution or real-provider I/O.
Dedicated KVM, public ingress/provider behavior and fleet rollout evidence remain
required before opening production admission. No deployment or policy activation
was performed by this slice.


## Direct private Job file uploads — 2026-10-06

Job handlers can now use `scope.uploadArtifact({report_id, name, data, maxBytes})`
with bounded text/bytes. The API receives bytes using the current native task
proof and active customer ownership, verifies their exact size and SHA-256, and
retains a private receipt. An opaque `operation://.../artifacts/...` reference
contains no physical storage key. Scoped downloads verify retained bytes through
the operation's private storage binding.

Each transfer reserves a fresh durable staging object. Matching concurrent copies
converge on one report receipt; losing, interrupted and rejected copies remain
collectable without overwriting its retained object. The SDK snapshots bytes,
coalesces matching calls and looks up the durable receipt before retrying private
platform transport. Business code executes once. Changed declarations conflict,
cancellation and stale native authority fence new writes, and prepared files
publish only after confirmed task exit with valid typed output or explicit
account success recovery. Recovery generations use fresh file identities.
Completion delivery stays independent.

The canonical Job export template and example now use this helper. Their app
upload broker, bucket writer, storage credential and bucket configuration have
been removed. The browser server serves public selectors/assets; the Job requires
its public API URL and scheduler context. Managed-source file preparation remains
available for existing integrations, HTTP handlers and workflows.

Focused memory/PostgreSQL API and recovery checks passed under the race detector,
including publication/downloads, concurrent copies and cleanup, interrupted writes,
lost replies, cancellation while receiving bytes and ownership revocation during
copying. Node SDK and packed starter checks passed; Go SDK/native guest portable
checks passed. OpenAPI lint, specification parity, SDK route coverage, generated
contract reproduction and focused production lint passed. These are focused local
checks, not full repository CI or native qualification.

The dedicated native lane now requires a fifth direct-upload scenario. The metal
package compiles and portable lane/guest checks pass; actual KVM/provider/fleet
qualification remains pending. Admission stays closed. See ADR-606 and the native
qualification guide for activation requirements.


## Direct HTTP result uploads — 2026-10-06

[ADR-607](adr/607-http-operation-direct-artifact-uploads.md) adds direct private
HTTP uploads and durable receipt lookup under the existing workload JWT and
invocation proof. Uploads share retained blobs, quotas, transfer budgets and
cleanup with other execution adapters. Cancellation fences new attachments;
committed matching receipts remain replayable under a live claim. Retention does
not settle HTTP work or change reconciliation and completion delivery.

The Node HTTP helper shares the bounded upload state machine with native Job
uploads, fetches fresh workload metadata, coalesces matching calls and checks a
receipt before retrying byte transport. The CLI HTTP starter and example now
return `artifact_id` and `rows` and download that exact private file, without an
application bucket writer. This updates the starter's output schema for a new
immutable definition revision. Existing deployed definitions remain pinned.

Local qualification covers both stores under the race detector, installed SDK
starter execution, generated SDK contracts and API/spec parity. Native KVM and
fleet activation remain pending; production admission was not activated.

## Direct workflow result uploads — 2026-10-06

[ADR-608](adr/608-workflow-operation-direct-artifact-uploads.md) adds direct private
uploads and receipt lookup for the final workflow action. Both routes require
current workload/native proof. The file keeps its operation/run/step/report identity
across approved resumes, while fresh generation/attempt authority rebinds the
existing receipt without another transfer or report. Retention does not confirm
the action: pending files remain private until final-step success or explicit
confirmed-success recovery. Completion delivery remains independent.

The Node workflow helper shares bounded byte snapshots, hashing, coalescing,
receipt validation and transport retries with the HTTP/Job helpers. Each retry
checks for a durable receipt first. The workflow export example now returns
`artifact_id` and `rows` without a managed bucket, provider writer or storage token.
Go streaming clients and generated Node/Python contracts expose both routes.

Focused memory/PostgreSQL race checks cover lost replies, approved resume without
another transfer, final-step proof restrictions, conflicting declarations, byte
integrity, concurrent copies, cleanup, private downloads, cancellation, tenant
revocation and fixed deadline expiry during I/O. PostgreSQL lock-wait checks cover
both new receipt commit and existing receipt reuse after the native deadline.
SDK/helper and packed starter checks are portable evidence. Full repository CI,
native KVM and fleet activation remain separate gates; admission stays closed.

## Native Customer Workflow Operations lane — 2026-10-06

[ADR-609](adr/609-native-customer-workflow-operation-qualification.md) adds five
real-guest scenarios and a blocking `customer-workflow-operations-only` CI lane.
The app fixture uses migrated PostgreSQL, real daemon owners, vmmd workload
identity and native linear workflow dispatch. It covers customer-scoped typed
exports/private downloads, a lost upload acknowledgement, independent signed
notification retry, process-death reconciliation, daemon restart after the
uncertain outcome, prefix/code/file retention through approved resume, queued
and running cancellation, fixed deadline expiry and owner revocation.

The test-only per-instance vsock relay forwards native authority unchanged and
injects faults after private retention. It does not mint proof or settle work.
The source-derived dedicated lane requires every selected scenario to PASS;
full-suite selection includes them in the blocking wake phase. Cleanup drains
the app, reaps producers, removes only owned retained keys and runs leakcheck.
Portable guest/image checks, metal compilation and CI selection/verdict tests
are development evidence. Native KVM execution, production network/provider
qualification and fleet activation remain pending; admission stays closed.
See [the qualification guide](ops/customer-workflow-operations-native.md).

## Complete customer workflow export starter — 2026-10-07

[ADR-610](adr/610-customer-workflow-operation-starter.md) adds
`customer-operation-workflow-export` to the CLI's embedded template catalog.
The generated source and synchronized example combine three cancellable native
actions with customer login integration, history, durable submission lookup after
reload, live step progress, generation-fenced cancellation and exact private file
downloads. Business and completion delivery status remain separate. The output
schema bounds rows and explicitly validates the artifact UUID pattern.

The starter requires the internal packed SDK in its source archive and an
explicit app/environment/definition binding. Its public bootstrap projects only
selectors; browser assets exclude workload/runtime modules. A development token
form keeps credentials in memory. `public/customer-auth.mjs` now defines an
application login adapter with an async fresh-token callback and an identity
change subscription; switching accounts closes the prior operation session and
clears its visible data. Confirmed prefixes and interrupted final
actions have distinct progress states; reconciliation calls for operator review.
The included guide uses inspection, read-only preview and a fenced CLI recovery
receipt. It never retries uncertain business work from the customer UI.

Portable acceptance installs the packed SDK outside the checkout. Chromium loads
the actual generated UI and browser modules to check lost-response reload recovery,
customer switching, SSE progress, cancellation, result/delivery independence,
artifact identity and explicit retry preserving input/key/definition. A dedicated
blocking browser CI job uses pinned Playwright/Chromium. Existing SDK upload tests
cover lost acknowledgements and approved final-action receipt reuse without a
second transfer. CLI checks cover schemas, discovery, prepared-source guards,
source packing and example parity. These checks do not settle native executions;
KVM, public ingress/provider behavior and fleet activation remain separate gates.
Production admission remains closed.

## Shared customer Operations login adapter — 2026-10-07

[ADR-611](adr/611-shared-customer-operations-auth.md) adds
`CustomerOperationAuth` to the browser-safe SDK entry point. The HTTP, Job and
workflow starters share the same async credential and identity-change adapter.
The SDK resolves credentials on demand and makes provider unsubscribe
idempotent; each feature closes and clears its operation session after signout
or a customer switch. Demo forms remain as a local fallback. No platform API,
storage or admission behavior changes.

## Shared customer Operations feature controller — 2026-10-07

[ADR-612](adr/612-customer-operation-feature-controller.md) adds
`CustomerOperationFeature` to the browser-safe SDK. The HTTP, Job and Workflow
starters now delegate credential preflight, client/session construction,
history loading, receipt resumption, stale-connect fencing, identity-change
teardown and disposal to one controller. Starter code keeps its operation UI,
rendering and explicit actions against the returned session. A superseded
connection returns no session, so it cannot publish another customer's history.
The feature and session APIs carry separate TypeScript input and result types;
server-side JSON Schema remains the runtime validation contract.
No platform API, storage, execution or admission behavior changes. Production
Operations admission remains closed.

## Generated customer Operation types — 2026-10-07

`gregale customer-operations types` now validates the selected source manifest
and emits input/output declarations from each operation's JSON Schemas. The
HTTP, Job and Workflow starters reference those generated declarations in
JSDoc, carrying the schema types through `CustomerOperationFeature` to
`session.start()` and typed result snapshots. Runtime JSON Schema validation
remains authoritative, and generated files are only replaced when they carry
the generator marker. Its `--check` mode compares the expected declarations
without writing, reports missing or stale output and exits nonzero so CI can
keep committed types synchronized with schemas. Each starter now provides
`npm run typecheck`, which checks its browser feature and workflow progress code
against the generated declarations and packed SDK types with no emitted files.
See [ADR-613](adr/613-customer-operation-typescript-generation.md).
