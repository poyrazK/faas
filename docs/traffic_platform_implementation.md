# Traffic platform implementation tracker

Objective: implement the six delivery steps in the 2026-09-29 gap-closure plan.
Base: `56618879c`; branch: `codex/traffic-platform-gaps`; decision: ADR-375.

## Requirements and acceptance

Public response ownership must independently verify the exact security
generations admitted by compute. It must cancel blocked client writes and both
directions of an upgrade without waiting for another upstream read. A missed
revoke/release pair during the handoff must refuse the old exchange. Protected
metadata is bounded, consumed privately and cannot be authored by a guest.
Managed realtime retains its separate, explicitly excluded owner.

Public HTTP dispatch must pin scoped deployment weights and release/revision
eligibility in one committed view before cache or wake. One deployment must
remain selected through capacity waiting and retries, including when the
original selected cohort is cold. Instance/session affinity cannot cross it.
Edge route substitutions must prove both the source hostname claim and target
app in that view. A reserved routing miss cannot become a synthetic hostname.
Claimed source apps retain emergency cancellation ownership through cleanup.

- [ ] Outbound circuit: startup wiring, IPv4/IPv6 rules, atomic updates,
  current desired state before new/restore execution, parked/restart recovery,
  all supported DNS answers, retry/reconciliation, opt-out cleanup, actual
  enforcement reporting, native network/KVM and leak evidence.
- [ ] Total deadline: explicitly configured total versus execution semantics,
  trusted start/remaining-time transport, upload/queue/wake/retry/execution and
  internal chains share deadline, streaming semantics and cancellation cleanup.
- [ ] Fleet accounting: authoritative shared rate and retry budgets, explicit
  outage behavior, defined cache/auth/internal/attempt accounting, real
  two-gateway/store/restart verification and latency evidence.
- [ ] Node admission: trusted instance cap before guest forwarding, public and
  declared internal paths, bounded overflow, full-exchange permit lifecycle,
  restart/generation fencing, real multi-gateway verification.
- [ ] Policy: immutable effective revision per request, emergency revocation
  semantics, covered-path matrix, runtime decision evidence, preview agreement,
  update-during-wake/retry and partial-outage tests.
- [ ] Customer release: validated existing API/CLI configuration, feature status,
  safe retry/idempotency contract, complete-path integration/load/recovery suite,
  staging rollout evidence and capability/docs updates.

## Evidence log

### Ordinary owned host aggregate mutation guard, 2026-09-30

The shared account-locked transaction now compares before/after enabled rule
groups and referenced presets before committing a rule or preset mutation.
Only selectors, IDs, counts and byte measurements leave Postgres. A bounded
automaton uses the runtime's exact-or-LIKE host language, including existing
percent/underscore, star/question-mark and escape behavior. Every matching
rule contributes, and a referenced preset contributes once per host. Disjoint
hosts retain independent allowances; connected overlap groups are not treated
as one account quota.

Counts and canonical SQL bytes are checked separately from a conservative
bound for escaped compiler inputs. The latter fills mandatory Go defaults,
mirrors the preset FK, accounts for HTML/Unicode escaping and reserves timestamp
spelling differences. Unsupported legacy aliases refuse analysis until repaired
through a supported replacement, disabling or deletion. Size-reducing repairs
to a legacy overload remain possible when analysis completes and none of the
over-limit dimensions increases. New or increased overload refuses with 422,
rolling back saved intent and transactional change events. Analysis exhaustion
has its own `traffic_policy_too_complex` code and generic limit/observed fields;
byte bounds additionally retain the explicit byte fields.

Metadata, nodes, states, retained state buffers/overhead, transitions and phase
time are bounded centrally. A local 1,750 ms SQL timeout precedes each two-second
analysis context, and the previous statement timeout is restored on success.
A blocked analysis releases its account lock before returning its server
timeout refusal. Formatting is evaluated once per row and only scalar measures
are materialized, avoiding repeated formatting and temporary storage of large
numeric-expanded bodies.

All 53 selected state tests passed in 36.788 s with no skips. They include real
Postgres comparisons for 154 selector/hostname pairs, decoded-runtime estimate
checks for sparse actions, header null entries, escaping and the preset mirror,
metadata refusal without body transfer, and statement-timeout restoration.
Two different apps competing for the final host budget admit one reference and
refuse the other without an extra change event. Preset growth is rejected
without changing saved intent or its ledger; a partial legacy repair and an
unrelated host remain writable. Canonical numeric expansions totaling over
64 MiB are accepted on disjoint hosts, refused when overlapping or retargeted,
and repaired by disabling a contributor. Store-lock timeout/retry, legacy shape
repair, account isolation, account waiters, quotas and existing rule/preset/
environment/clone behavior also passed.

All 152 selected apid/API tests passed without skips: apid 2.635 s and API
0.846 s. Injected row/aggregate-byte/aggregate-count/analysis refusals exercise
both HTTP rule write paths, preserve saved intent, abort convergence and emit
no activation request. Count/analysis errors retain generic limit/observed
fields without incorrectly labeling them as bytes; status mapping and docs
links are checked. Real store enforcement is tested by the Postgres fixtures
above. Fresh sqlc v1.31.1 generation matched all four generated Go files; the
new queries use existing schema and require no migration. Pinned lint v2.4.0
reported zero issues for apid/API with tests and state production code; state
used tests=false/unused disabled for the existing test-only helper. Whitespace
checks passed. Earlier disk-exhausted builds and failed intermediate behavior
checks are excluded from these final pass counts.

Scoped environment overlay totals, global synthetic route discovery across
accounts, the in-memory aggregate mirror, full daemon/load/customer rollout
and native VM/firewall/leak acceptance remain pending. No Linux x86_64 KVM host
is available. All six release guarantees remain unaccepted.

### Rule write bounds and account mutation serialization, 2026-09-30

Both edge-rule create methods and complete merged updates validate the saved
row's canonical runtime projection against the 64 MiB host ceiling before
commit. Mutation statements return only their ID; the sqlc projection read
returns a scalar observed size and no body for an oversized row. Metadata,
inactive union fields and JSONB numeric expansion participate. Failure rolls
back intent and its transactional change event. The in-memory mirror uses
the existing conservative JSONB estimator with timestamp spelling allowance.
The API returns the existing structured 422 and aborts fleet convergence on
refusal. Smaller replacements and deletion remain repair paths.

Rule/preset creates and updates, environment edge overlays and clones now
acquire the same account row lock before app, FK or policy rows. The lock is
retained through mutation and commit. Contended attempts roll back and return
the pool connection before a context-bounded retry. The account preset count
is therefore serialized across different apps. No byte quota across disjoint
hostnames was introduced. These are prerequisites for the aggregate guard;
per-host overlap analysis, combined rules/presets/overlay measurement and
concurrent aggregate-bound acceptance are still required.

All 56 selected state tests passed in 23.190 s without skips. Real Postgres
fixtures reject a roughly 5 KiB action whose numeric expansion exceeds 64 MiB,
verify no oversized body transfers from the guarded read, retain the saved
intent/change ledger after refusal, and repair the legacy row. Measurement of
the repaired row matches the public runtime SQL projection. Both create paths
and the in-memory mirror passed refusal/replacement/delete checks. Seven
mutation paths wait on the account lock without retaining an app lock; context
cancellation releases waiters and another account can still mutate. Concurrent
presets for different apps compete correctly for one account slot. Six writers
waiting on a three-connection pool leave ordinary reads available and complete
after the holder releases its lock. Existing edge-rule, preset, environment
and clone behavior passed in the same selection.

All 134 selected apid/API checks passed without skips: apid 2.481 s and API
0.811 s. An injected store refusal verifies both HTTP mutation error paths,
saved-state retention, convergence abort and absence of an activation event;
numeric expansion is verified by the actual stores separately. Fresh sqlc
v1.31.1 generation matched all four committed Go files. The new reads and
account-lock query use existing schema; no migration was required.
Pinned golangci-lint v2.4.0 reported zero issues for apid and API, including
tests. State production lint reported zero issues with tests=false/unused
disabled for the pre-existing test helper. Lint used matching compiler flags,
two runtime workers and GOGC=50. Whitespace checks passed.

Builds used CGO_ENABLED=0, one package worker, disabled DWARF and stripped
linker output. One initial build returned only FAIL; another reported disk
exhaustion before creating the test binary. Those runs are excluded. After
reclaiming obsolete task-owned test archives, the recorded runs passed.

Atomic aggregate per-host validation, decision/path evidence, preview
agreement and full daemon/load/customer/staging acceptance remain pending.
Native KVM/network/process-death/leak acceptance remains pending because no
host is available. All six release requirements remain unchecked.

### Initial public route graph ownership, 2026-09-30

The production matcher now uses the public router's namespace configuration
to resolve hostname ownership and read route rules in the same readonly
transaction. Claimed hosts apply the existing row/byte bounds after filtering
by verified account. Global lookup remains available for genuinely unclaimed,
substitutable hosts. Reserved misses and immutable deployment URLs skip the
unused route query. Missing production resolver configuration refuses with
503. The legacy in-memory matcher keeps its existing path.

A private root claim baseline survives replacement with the compiled owner
policy. Compilation and dispatch verification recheck all pinned claims with
independent projection readers sharing the new transaction. Account/domain
ownership, authoritative misses/reservations, root metadata and namespace
configuration must agree before an edge response or dispatch. Startup wiring
supplies the same router configuration as the actual public backend.

All 34 selected public-policy tests passed against local Postgres in 30.464 s,
with no skips. Fixtures exceed the actual global 50,020-row and 64 MiB bounds
while the claimed owner's graph and dispatch remain available. Root settings,
new app claims, new reservations, domain retargeting to another account and
namespace feature changes refuse an earlier graph; fresh requests recover.
An exclusive edge-table lock proves reserved/immutable initial graphs skip
the unused query while a substitutable claimed host still requires it. Real
HTTP/1 and HTTP/2 listeners refuse a plan cutover between initial graph pinning
and a pure edge response, then return the verified redirect on a fresh request.
Existing owned-preset/overlay/imported-contract, routing, outage and cleanup
checks passed in the same selection. Nine legacy matcher, owner-carrier and
source-security regressions passed without skips: internal package 1.812 s,
gateway package 1.176 s. Pinned golangci-lint v2.4.0 reported zero issues for
gatewayd-internal, including tests. Whitespace checks passed. No new SQL,
schema change or migration was needed.

The first run exposed fixture namespace and SQL parameter-type errors; both
were corrected. A subsequent overload fixture filled the disk and interrupted
Postgres, so that run is excluded. After reclaiming obsolete task-owned build
archives and inactive test databases, Postgres recovered and the recorded
runs passed. Builds used CGO_ENABLED=0, one package worker, disabled DWARF and
stripped linker output. Lint used matching compiler flags, two runtime workers
and GOGC=50.

Atomic aggregate per-host write validation, decision/path evidence, preview
agreement and full daemon/load/customer/staging acceptance remain pending.
Native KVM/network/process-death/leak acceptance remains pending because no
host is available. All six release requirements remain unchecked.

### Clone and imported-contract write bounds, 2026-09-30

Imported documents now validate canonical runtime bytes in both store write
methods, including JSON numeric expansion beyond the upload body's size. The
quota-aware path performs that check in the same transaction as the locked
account count, owned-parent verification and replacement. Existing imports
reuse their quota slot, including legacy repair at the account cap; new imports
still require a slot and unsupported plan tiers refuse. The API delegates this
decision to the store instead of rejecting all writes from an unlocked count.
Both oversized imports and clone projections use the existing structured 422.

Environment clones check the copied target rows inside the transaction before
commit. The SQL verdict returns only scope and observed bytes. Ownership
metadata, the target slug and app-wide fallback routes are included. Refusal
rolls back the target and all copied database configuration. The in-memory
store validates every candidate under its mutex before creating any target
state and uses the same route fallback for validation and copying. Existing
managed-provider preparation/compensation behavior is retained.

All 32 selected state tests passed in 12.817 s without skips. Real Postgres
fixtures cover numerical expansion, exactly-at-bound imports, oversized legacy
refusal and repair at a full quota, preserved first-import time, and two
concurrent new imports competing for one slot. Clone fixtures cover scoped edge
and route policies at the source bound whose longer target slug exceeds it,
oversized app-wide fallback routes, rollback of copied variables/secrets/policy
rows, and successful retry after source repair. In-memory preflight, quota,
IDOR, defensive-copy and existing clone behavior also passed.

All 44 selected HTTP tests passed in 4.638 s without skips. They cover the
actual plan import cap, replacement-slot reuse, new-slot refusal, structured
numeric-expansion errors with saved document retention, and clone refusal
without a target environment. Local runs used CGO_ENABLED=0, one Go package
worker, -gcflags=all=-dwarf=false and stripped linker output to reduce temporary
build storage. Initial runs that returned only FAIL are excluded from evidence;
reruns after task-cache cleanup passed. The first lint run failed to load pgx
export data. With matching Go compiler flags, a fresh task lint cache, two Go
runtime workers and GOGC=50, pinned lint passed apid with zero issues. State
production lint passed with tests=false/unused disabled for the pre-existing
test helper and zero issues. Whitespace checks passed.
Fresh sqlc v1.31.1 generation matched all four committed Go files; the queries
use existing schema and require no migration.

Atomic aggregate per-host validation, decision/path
evidence, preview agreement and full daemon/load/customer/staging acceptance
remain pending. Native KVM/network/process-death/leak checks remain pending
because no acceptance host is available. All six release requirements remain
unchecked.

### Individual policy write bounds and legacy repair, 2026-09-30

CORS preset creates and complete replacements, environment edge overlays and
scoped route contracts now validate their complete proposed runtime projection
before changing intent. Postgres measures canonical JSONB bytes with a sqlc
query, including ownership metadata and separator whitespace. Preset display
metadata and policy timestamps remain outside the projection. CORS PATCH checks
the merged object. The in-memory store uses a conservative serialization bound.
No flat account byte quota was introduced.

Oversized writes return `traffic_policy_too_large`/422 with the generic and byte
limit/observed fields, documentation URL and repair guidance. Saved policy is
retained and the environment convergence operation is aborted on refusal.
Smaller replacements and empty scoped policies repair existing oversized rows;
fresh bounded runtime reads then accept the repaired projection without needing
notification delivery. Referenced presets require reference removal before
deletion; deleting one alone continues to refuse those rules.

State checks cover rejected creates and replacements, retained saved state,
exactly-at-bound acceptance and one-byte-over refusal for all three Postgres
projections, legacy oversized runtime refusal and repair, and existing CORS and
environment clone/delete behavior. All 34 selected top-level state tests passed
in 14.260 s, with no skipped tests. Expanded builds exhausted disk; after
reclaiming this task's obsolete build archives, the final run passed. An initial
edge boundary fixture used an omitted empty header value; it was corrected to
retain that field while measuring overhead. Failed runs are not acceptance
evidence. HTTP fixtures cover the structured error contract, a merged preset
PATCH whose individual fields fit, saved-state retention and empty recovery.
All 15 selected HTTP tests passed in 1.618 s with no skips; the API package
suite passed in 4.301 s. Pinned lint found zero issues in apid and API code;
state production lint excluded tests and the pre-existing unused test helper
and found zero issues. Fresh sqlc v1.31.1 generation matched all four committed
Go files. No schema change or migration was required. Whitespace checks passed.

Atomic aggregate per-host checks across contributing rule/preset mutations
remain pending. Clone and imported-document validation continued in the later
evidence entry above.
These individual object guards do not establish preview/runtime agreement,
bounded decision evidence, complete daemon/load/customer/staging acceptance or
native KVM/network/process-death/leak acceptance. No acceptance host is available;
all six release requirements remain unchecked.

### Compiled edge-rule and preset agreement, 2026-09-30

Public requests now resolve a bounded fresh route-only graph before selecting
an owner. The hostname/app transaction reads only that owner's complete host
rules, referenced CORS presets and stable environment URL overlay. Sorted
host/preset reads and deterministic rule ordering produce a content baseline;
compiler-cache hits require that freshly verified baseline. Full sealed actions
replace the initial matcher view before security checks or a pure edge answer.
The original route graph remains private for later comparison. Claimed hosts
verify owned routes; unclaimed synthetic hosts verify the global route graph.

Target resolution checks the source claim in the same database view before a
substitution can answer at the edge. The production target loader retains read
failures and refuses a selected route whose target is unavailable or missing,
instead of falling back to the source app. Dispatch rechecks rules, presets,
overlays and both app/host baselines with routing eligibility and weights.
Admitted requests retain their sealed actions through cache reset and wake/retry.
Compiled carriers are excluded from serialized app evidence.

The sqlc projections cap matching rows at 50,020 and canonical row JSON at
64 MiB before aggregate transfer. Referenced presets count toward the compiled
input byte cap; presets and environment overlays each have a 512 KiB SQL
projection bound. Invalid owner IDs cannot turn an owned read into a global
read. Compiler caches retain the existing host-entry ceiling. The edge-rule
schema projection adds the existing match_headers column/check from the actual
migrated database; no migration was added. All four sqlc v1.31.1 generated
files matched fresh generation against those table definitions.

Real Postgres fixtures verify app/rule/preset changes committed between reads,
refusal between lookup and dispatch, missed-notification repair, independent
admitted actions, owner isolation, unused foreign preset locks/broken policies,
synthetic owner changes, source cutover before a pure edge answer, unavailable
targets, bounded aggregate/preset/overlay refusal, transaction cleanup and
store outage/recovery. Stable environment overlays preserve missing-row
fallback and explicit-empty replacement. Two HTTP gateway handlers, one HTTP/1
and one HTTP/2, repair IP and CORS changes without notifications; denied traffic
does not reach forwarding and response fingerprints change. Scheduler and
forwarding remain fixtures in one process.

Full internal-gateway and gateway suites passed in 37.274 s and 64.456 s.
The API suite passed separately in 1.595 s after reclaiming task-owned build
caches and inactive test templates; disk-exhausted runs are excluded from
acceptance evidence. Final focused Postgres/HTTP checks, including successful
claimed/synthetic substitutions, passed in 11.012 s; focused gateway carrier
checks passed in 3.249 s. Pinned lint passed gateway, internal gateway, API and
state production code with zero findings; state excluded tests and the
pre-existing test-only unused helper. Diff whitespace checks passed.

All six guarantees remain unchecked. Bounded runtime decision evidence,
preview/runtime and complete synthetic-path agreement, full daemon/load/recovery
and customer/staging release evidence remain required. Write-time validation
of aggregate projection limits and complete oversized-policy recovery need completion
before rollout. Initial fresh-policy
reads require complete-path total-deadline timing/failure acceptance. Native
Linux x86_64 KVM, nft connection/source-IP, process-death and leak acceptance
remain pending; no acceptance host is currently available.

### Imported and scoped route-contract agreement, 2026-09-30

Public hostname resolution now includes the exact environment's route-contract
overlay and any needed owner-scoped imported OpenAPI document in the same
read-only repeatable-read app/account/host view. Explicit routes keep precedence
and skip unused document reads. Missing documents are fingerprinted too.
The later routing transaction rechecks these inputs before eligibility and
weights. Changed imports or scoped fallback cannot combine with an old dispatch
baseline. Matching and route observation compile the admitted private carrier;
compilation cache reuse requires its content digest. Document bytes are copied
before sealing and excluded from serialized app/request evidence.

Both SQL projections bound canonical contract payloads to 512 KiB before
transfer. The new table projection exactly matches the migrated database;
the schema update adds no migration. All four sqlc v1.31.1 generated files
matched a fresh regeneration.

Real Postgres fixtures verify a document/app change committed between reads,
refusal between resolution and dispatch, creation/deletion without
notifications, preserved admitted matching/observation, scoped precedence and
deletion fallback, owner isolation, transaction closure, store outage/recovery
and oversized document/scoped-contract refusal before payload transfer.
Two real HTTP gateway handlers (HTTP/1 and HTTP/2) against Postgres repair warmed
contracts without notifications: old routes refuse before forwarding, fresh
routes run and policy fingerprints change. They run in one test process;
scheduler and guest forwarding are fixtures. Full internal-gateway and gateway
suites passed in 19.734 s and 61.668 s; the full API suite passed in 1.321 s.
Final focused Postgres/HTTP checks after tightening the entire scoped payload
bound passed in 5.890 s. Pinned lint passed gateway, internal gateway, API and
state production code with zero findings; state excluded tests and the
pre-existing test-only unused helper. Diff whitespace checks passed.

All six guarantees remain unchecked. Compiled edge-rule/preset agreement,
bounded decision evidence, preview/runtime and complete synthetic-path coverage,
full daemon/load/recovery and customer/staging release evidence remain required.
The initial fresh hostname/contract read must be measured against total
request-deadline semantics in complete-path acceptance. Native Linux x86_64
KVM, nft connection/source-IP, process-death and leak acceptance remain pending;
no acceptance host is currently available.

### Source-host agreement and reservation protection, 2026-09-30

Edge route substitutions now carry the source hostname's authoritative claim
alongside the target app projection. Positive claims verify the same account;
negative claims require a genuinely unclaimed hostname. Both content baselines
are independently rechecked in the read-only routing transaction before
eligibility/weights, with separate fingerprint recorders over the same view.
The sealed effective fingerprint retains both through wake/retry.

An internal or deleted app slug, an unverified exact/wildcard domain, a tenant
reservation or a failed/deleted alias target cannot become a synthetic host.
Immutable revision, alias and named-environment URLs preserve their exact
dispatch. The reserved alias namespace stays protected after alias removal.
New reservations invalidate an old negative claim; releasing a
domain reservation allows a fresh request to resolve. The reservation query
is generated by sqlc; no migration was added.

Claimed source apps join the account/target/deployment emergency fence before
wake. Real HTTP/1 and HTTP/2 handler fixtures verify source cancellation,
initial refusal, four-scope registration retention until joined forwarding
cleanup, and final registration release. Forwarding and security state are
fixtures. Real Postgres checks cover source settings, deletion, foreign domain
retargeting, new claims/reservations, same-view interleaving, unavailable reads,
transaction cleanup, reserved namespaces and forged allow metadata. Full
internal-gateway and gateway suites passed in 16.734 s and 61.436 s. Final
focused Postgres/HTTP checks after the alias-namespace refinement passed in
12.775 s and 2.897 s. Pinned lint passed gateway, internal gateway and state
production code with zero findings; state excluded tests and the pre-existing
test-only unused helper. All four sqlc v1.31.1 generated files matched a fresh
regeneration. Diff whitespace checks passed.

All six guarantees remain unchecked. Compiled/imported policy agreement,
bounded decision evidence, preview/runtime and complete synthetic-path
coverage, full daemon/load/recovery and customer/staging release evidence
remain required. Native Linux x86_64 KVM, nft connection/source-IP,
process-death and leak acceptance remain pending; no acceptance host is
currently available.

### Public host policy verification, 2026-09-30

Public host resolution now uses a fresh bounded read-only repeatable-read
Postgres view for hostname binding, app flags, account plan, environment policy
and deployment ingress. Ordinary project app hosts use production; standalone
hosts use the default scope. Immutable and alias hosts project their exact
deployment, including a retained live zero-weight revision. The dispatch
transaction verifies the private host-content baseline before reading routing
eligibility and weights; a changed baseline or missing verifier refuses.
Transactions end before wake. Fresh reads bypass route caches, so missed
notifications cannot preserve an old alias or domain/environment binding.
An unavailable source-owner read refuses synthetic route substitution.
The managed service deployment waker also projects exact ingress and refuses
another app's deployment. Soft-deleted targets and overflowing immutable
revision numbers refuse resolution.

Real Postgres fixtures passed for changes committed between app/account/ingress
reads, refusal between host resolution and dispatch, exact/scoped ingress,
zero-weight alias projection, retargeting without notifications, named
environments, changed environment policy, domain retargeting, tenant suspension
without legacy-domain fallback, minimal credential projection and transaction
cleanup. HTTP handler checks passed for unavailable and expired host reads and
source-owner failure before wake/forwarding. Full internal-gateway (10.381 s)
and gateway (61.565 s) suites passed; final focused checks after the tenant-guard
refinement passed in 7.116 s and 2.802 s. Pinned lint passed gateway, internal
gateway and state production code with zero findings; state excluded tests and
the pre-existing test-only unused helper. All four sqlc v1.31.1 generated files matched a
fresh regeneration; the three affected schema table projections matched the
migrated Postgres definitions. No migration was added.

All six guarantees remain unchecked. Imported/compiled edge-policy
agreement, bounded decision evidence, preview/runtime agreement,
synthetic-path coverage, complete daemon/load/recovery tests and customer/staging
release evidence remain required. Fresh host lookup must be measured within
the configured total request deadline in full-path acceptance. Native Linux
x86_64 KVM, nft connection/source-IP, process-death and leak acceptance remain
pending; the user confirmed no host is currently available.

Implementation started from a clean new worktree after fetching origin/main.
The audit snapshot remains unchanged. No guarantee is marked accepted yet.
Native metal/leak, real fleet tests and rollout remain required; unavailable
acceptance infrastructure must be reported rather than replaced by unit tests.

### Local verification, 2026-09-29

Outbound wiring/recovery implemented: both families, whole-set atomic updates,
pending-wake registration, durable revisions, boot-time read, retry/DNS refresh,
opt-out/stale removal, complete payload validation and completed-enforcement
stats. The real local PostgreSQL round-trip test passed for normalization,
unchanged revisions, process-store restart and durable empty close policies.
The database history test passed for distinct timestamps, conservative
same-time regional verdicts and upstreams with no recent probe. Prepared
network reuse was verified to seed enabled circuits before the restore hook.
Migration and extracted schema-dump blocks were verified against that database.
Full local suites passed for circuit, netns, api, grpcerr, sched, fcvm,
vmmdgrpc, daemonunitspec, cmd/vmmd and cmd/schedd. Linux nft transaction tests
were added; native execution, real connection rejection, KVM restore and leak
checks remain pending. The user confirmed no acceptance host is available.

The optional total deadline is implemented through existing API/CLI budget
actions. Gateway tests passed for upload cancellation/spool cleanup, cold-wake
expiry, trusted ingress elapsed time, forged timestamp replacement over HTTP/1
and H2, execution override containment and timer release. The reqbudget suite
and full gateway suite passed. Focused API, CLI, internal gateway command and
policy preview suites passed; generated Node and Python budget models include
the field, and the Node SDK build and Python model round-trip passed. Nested
managed-service deadline transport and complete-path acceptance remain
required before checking off the deadline deliverable.

Fleet counters now default to shared Postgres, with explicit local mode and
the existing Redis override. Central errors refuse admission; eligible public
requests charge before cache lookup. Retry observation failures refuse replay
while the original can run once. The full gateway suite and focused daemon
configuration/startup suites passed. Separate processes against real Postgres
verified aggregate burst/retry caps, replacement, expiry, store recovery and
bounded row-lock waits. Two HTTP gateway processes admitted four of sixteen
requests from one shared burst and preserved debt on replacement/recovery;
local fixture p95 was 8 ms. Narrow vet passed for the acceptance package.

The daemon-level Postgres/Redis service canaries compiled but could not boot
on Darwin: the mandatory capability check requires `/proc/self/status`. They
remain pending on Linux. The repository-pinned linter passed for gateway,
gatewayd-internal and the acceptance package; the deadline ingress refactor
and upgrade tests passed, and the operational verification script passed its
shell syntax check. Deployment evidence remains required.

Node HTTP admission is implemented at vmmd's forwarding boundary for both
ordinary streams and raw Upgrade. The trusted wake plan determines the cap;
client cancellation retains a permit until bridge completion or child reap.
Retirement and generation fencing precede network/lease reuse. Actual cap,
inflight, generation, plan and retiring status are reported in VM stats.
Local tests passed for all four plans, cancellation retention, late release,
replacement, cleanup, bridge acknowledgements and stale socket fencing. Two
separate HTTP forwarder processes, replacement, the real gRPC vmmd handler
and the reusable bridge binary preserved a cap of four and refused Upgrade
before guest tunnelling. VM/network ownership was a fixture in that test.
Full local fcvm, vmmdgrpc and gateway suites passed; the migration-drain
regression, bridge-command, vmmd startup and daemon-unit suites passed too.
The pinned linter and generated deployment-file check passed. The managed systemd unit
has explicit bridge-group shutdown and a private socket directory. Linux
parent-death/process-group tests were added but remain pending along with
native VM lifecycle/restart/leak and load evidence. Full policy snapshot,
nested deadline transport and release evidence are still outstanding.

### Local verification, 2026-09-30

The public handler now pins all compiled host-rule kinds before routing,
including resolved presets and environment policy, and deep-copies app flags
and plan inputs before admission. Cache refreshes do not replace the snapshot
during wake/retry. Unverified loads and reported compile errors refuse a new
snapshot; warm verified cache entries remain usable through store failures.
A versioned effective fingerprint is protected at ordinary response commitment
and reported in spans/logs. Full gateway (50.419 s) and internal gateway command
(4.708 s) suites passed. Emergency live revocation, imported document/service
policy coverage, simulator agreement, nested deadline transport and customer
release/acceptance evidence remain outstanding; all six guarantees are still
unchecked.

Blocked response writes are now bounded at compute and public HTTP hops. A
protected compute response carries the earlier deadline and successful session
decision; public ingress consumes these controls without exposing them. Guest
headers, edge header actions, Content-Type and late trailers cannot detach an
ordinary response. Invalid/ambiguous compute metadata refuses before response
commitment. Socket/HTTP2-stream deadlines and joined cancellation callbacks
release downstream writers as well as upstream reads. A pre-commit 504 has a
100 ms best-effort error-write allowance; a committed body is aborted.

Real HTTP/1 and HTTP/2 slow-reader fixtures passed through public production
wrappers, the actual routing handler, compute transport and real gRPC forwarding;
a rejected Upgrade and cancellation without a deadline also released ownership
while the unread client connection stayed open. Forgery and late-trailer,
invalid-metadata and deadline-containment checks passed. The full gateway suite
passed in 55.213 seconds, subsequent focused tests passed, and the full internal
gateway command suite passed in 4.703 seconds. The pinned gateway linter reported
zero findings. Compute must roll out before public for the new
private session decision. Native/deployed, cross-node clock/key-rotation, policy
revocation/preview and customer-release evidence remain pending.

The Linux bridge parent-death/process-group sources and their tests compiled
as an isolated, source-identical Linux amd64 test binary. That was a compile
check only; neither the tests nor native acceptance ran. The request snapshot
microbenchmark measured 22.4 microseconds and 12.1 KB per request on this Mac.
Owner-aware compile refusal was added to prevent another account's broken
wildcard/preset rule from blocking an unrelated tenant.
The repository-pinned linter passed for gateway and internal-gateway code.

The real gRPC forwarding regression verifies ordinary HTTP/1 and HTTP/2
deadline expiry after headers, explicit-stream/gRPC handshake detachment,
and failed-stream body cancellation. Raw gRPC coverage verifies successful
101 detachment, a non-101 body remaining bounded, and 504 on handshake expiry.
Session ceilings now remain active after detachment and are independent of
gRPC's remote handshake timeout. Public and service forwarding clear
caller-supplied stream controls. These checks use real gRPC and local HTTP
transports; VM ownership and native acceptance remain pending.
The final full gateway suite passed in 52.420 seconds. The API/gateway lint
invocation hit temporary local disk exhaustion while compiling the API test
dependency; this is recorded separately from code verification.
The narrowed gateway lint run then passed with zero findings.

Managed ordinary HTTP deadline transport is implemented with a target-bound,
purpose-derived MAC carrier and the existing shared session master key. MAC
verification precedes source-instance lookup so identity, discovery,
authorization and wake all inherit its deadline. Resolved app/account checks
still precede wake; child binding timeouts and retries cannot extend the
absolute parent deadline. Missing material refuses configured protection,
and no ephemeral or unsigned fallback is used. Public claims are stripped.
Node and Python helpers capture isolated request context, propagate it to
managed service hosts/aliases, and remove it on external hops. Node signed
calls return redirects; HTTPX guards each redirect. The loader no longer
logs credential-file contents.

The real local A-to-B-to-C HTTP fixture passed with a 300 ms root budget and
longer child timeouts, including leaf cancellation. Retry, identity lookup,
discovery, authorization, wake, invalid/ambiguous/audience/account refusal and
missing-key tests passed. Source-instance resolution and VM ownership were
fixtures. The full gateway suite passed in 52.950 seconds; focused gateway
and token suites passed after moving authentication ahead of identity lookup.
The full internal-gateway command suite passed in 4.693 seconds. Node build
and six context tests passed; Python's six context/redirect tests and Ruff
passed. The pinned Go linter passed for gateway, token and internal-gateway
code with zero findings. Cross-node clocks, key rotation, blocked downstream
write coverage, native VM/network/leak, full policy revocation/preview and
customer release/rollout evidence remain pending. All six guarantees remain
unchecked.

The bounded security-generation registry and reqbudget lifetime fence are
implemented as reusable primitives. Local tests passed for unchanged snapshots,
missed revoke/release pairs, scoped cancellation, generation regression/conflict,
store failure/timeouts, deduplicated scope/exchange capacity and observed limits,
late older-generation admission refusal, idempotent cleanup,
notification repair and shutdown. A real budget timer expired while its detached
stream remained live; a later lifetime revoke still canceled that stream, and
client cancellation remained effective. Unregistering a completed exchange does
not cancel its final buffered flush. Registry and reqbudget suites passed, and
their pinned linter reported zero findings. Durable security-generation triggers,
store reads and production public/service admission wiring are still pending;
this primitive is not yet an active runtime guarantee.

Durable security generations now have append-only Postgres triggers for account
suspension/abuse holds, app deletion and deployment security quarantine. Release
also advances the generation, and deletion retains UUID tombstones. Ordinary
plan, traffic-weight and nonsecurity parking changes leave generations unchanged.
The sqlc reader verifies the shared store without an allow-cache fallback; empty
input verifies the migration at startup. Real Postgres tests passed for each
transition, a missed revoke/release pair, replacement-backend tombstones,
existing blocked-identity backfill, rollback and missing-migration refusal.
Schema table/primary-key blocks match the real migrated database, sqlc
regeneration is unchanged, and the static embedded migration checks passed.
Production gateway/service enrollment remains pending.
The Postgres acceptance package passed the pinned linter. State production code
also passed with the unused check disabled because an existing helper is used
only by tests; the full state-test lint compilation exhausted local disk space.

Production public and service-proxy admission now uses those generations.
Startup verifies the shared table under a 250 ms deadline; requests enroll
before wake and add selected deployments before each dispatch. Periodic repair
and notification-triggered rereads cancel changed generations and store failures.
The lifetime fence survives stream/gRPC/Upgrade budget detachment. Cancellation
keeps registrations and forwarding permits until actual cleanup, and final
handler cleanup runs after response-write guards stop. Known initial account
hold/suspension errors remain compatible; a verified release overrides their
stale cached flags. Active revocation returns a stable 403 before headers,
verification failure a 503, and committed forwarding responses abort visibly.

Local checks passed for scope/capacity/refusal, account/app/deployment cancellation,
missed revoke/release, upload spool removal, public and service retry deployments,
HTTP/1 and HTTP/2 wake errors, and real gRPC cancellation after handshake-budget
detachment. A 64 MiB response with an open unread client verified interruption
and upstream cleanup for ordinary and streaming compute writes on both HTTP
versions. Two HTTP service gateways against real Postgres, with notifications
absent, repaired a missed hold/release pair. One lost its database pool and
refused new traffic while its healthy peer continued; replacement read the
durable released generation. These are gateway instances in one process with
source identity and forwarding ownership fixtures, not full daemon/VM acceptance.
The full gateway suite passed in 55.744 seconds and internal-gateway suite in
5.202 seconds. The pinned linter passed gateway, internal-gateway, db and the
Postgres acceptance package with zero findings. Operational semantics and
rollout gates are in `docs/ops/traffic-security-revocation.md`.
Imported document/service snapshots, bounded decision evidence, preview/path
agreement, all public-hop long-stream behavior, native VM/network/leak,
complete daemon/load/recovery and deployment acceptance remain pending.

Declared-route snapshots now include the imported OpenAPI contract and scoped
route override before guest work. Enforcement and route observation retain the
same immutable compiled view during document changes or store outages; fresh
requests fail closed on an unverified document. Cache keys include the owner,
and invalidation fences in-flight loads from republishing an old contract.
The configured total deadline also bounds this lookup. The protected policy
fingerprint cannot be replaced through guest headers or late response trailers.
Local document-change, owner-isolation, scoped-override, load/invalidation race,
deadline and real public HTTP/1/HTTP/2 trailer tests passed. The full gateway
suite passed in 55.912 seconds and internal-gateway suite in 4.676 seconds;
the pinned linter passed both with zero findings. Service snapshots, decision
evidence, preview/path agreement and the remaining acceptance gates above are
still pending. No native Linux x86_64 KVM acceptance host is currently available.

Managed service discovery/access now pins one bounded read-only repeatable-read
Postgres view: alias declaration, target selection, preview/test namespace,
caller binding/transport, target caller/method/path grant, reliability and target
protocol/WebSocket posture. The transaction ends before wake or forwarding.
The gateway deep-copies the result, retains it across retries, and protects its
response/span fingerprint against guest headers/trailers. Declared dependency
timeouts now include time spent loading policy from service-handler entry.
The sqlc projection excludes credentials and unrelated manifest settings;
two existing tables missing from the schema snapshot were added from their
real migrated Postgres definitions.

Real Postgres tests passed for a policy change committed between snapshot reads,
fresh-request denial/protocol changes, preview/test namespaces and project
preview policy changes, credential exclusion and transaction cleanup. Local
wake/retry mutation, unavailable/malformed/timed-out snapshot, proof forgery and
lookup-time budget tests passed. The full gateway suite passed in 56.068 seconds.
The full internal-gateway suite passed in 8.803 seconds against a fresh isolated
database; its first run encountered an existing shared-public migration ledger
in a legacy schema-based test. Pinned lint passed gateway/internal-gateway and
state production code with zero findings; state used tests=false and disabled
unused for the existing helper used only in tests. Project release, exact
deployment and affinity routing inputs still need snapshot/evidence coverage.
Decision evidence, preview/path agreement, public-hop blocked long responses,
native VM/network/leak and complete daemon/load/deployment acceptance remain
pending. No release guarantee is marked accepted.

Managed service routing now joins the access snapshot's read-only transaction:
source deployment release membership/expiry, exact override eligibility and
positive deployment weights are verified together. The roster is bounded to
100 positive deployments. One deployment is selected before wake, with release,
override and affinity precedence followed by weighted selection for ordinary
calls. Instance rotation and local preference stay inside that deployment;
refreshes and retries cannot cross into a new graph or changed rollout cohort.
No eligible deployment refuses before wake. Selected deployment is separate
span evidence; random selection entropy/choice does not change the policy proof.
Known release refusals remain pinned verdicts while storage errors fail verification.

Local wake/retry mutation tests passed for release, exact override, affinity and
ordinary weighted calls; fresh requests observed changed routing. Refusal tests
preserved 400/403/409/410/422/503 without endpoint lookup, wake or forwarding.
Real Postgres tests passed for a cutover committed between access and routing
reads, ambiguous source membership, expired release/direct pins, current/foreign
override checks, current weights and transaction cleanup. The final Postgres
check passed in 2.759 seconds; full gateway and internal-gateway suites passed
in 56.074 and 8.862 seconds. Pinned lint passed gateway/internal-gateway and state
production code with zero findings; state used tests=false/unused disabled for
the existing test-only helper. sqlc regeneration matched all four generated Go
files. The existing revision-pin table was added to schema.sql from its actual
migrated definition. Public routing snapshot completeness, bounded decision
evidence, preview/path agreement, public-hop blocked long responses and the
remaining native/daemon/load/deployment acceptance gates still require work.

Public HTTP response ownership now independently enrolls the exact security
generations admitted by compute. The private, bounded snapshot is stamped after
guest/action header mutations and verified against fresh authoritative rows
before public commitment. A revoke/release pair during handoff refuses the old
exchange; fresh requests can use the released generation. Both production
gateways verify the security table before startup. Public repair interrupts
blocked ordinary/long HTTP writes even after the handshake budget detaches.
Successful Upgrade tunnels close both transports and join both copy goroutines
before releasing their registration. Managed realtime explicitly retains its
separate excluded owner, and private metadata never reaches a matched public
client. Rollout requires a drained compute/public cutover for mixed versions.

Local public/compute tests passed for 64 MiB open unread clients over HTTP/1 and
HTTP/2, with real forwarding gRPC transports. Public-only revoke, a missed
revoke/release, store failure and periodic repair without notifications end
public, compute and RPC ownership before client closure. Long-response cases
remain active after an 80 ms public handshake budget expires. HTTP/1 Upgrade
fixtures cover idle and both blocked directions, protocol refusal and cleanup.
Handoff/recovery, missing/invalid/ambiguous metadata, guest/late trailer forgery,
the separate managed owner and startup refusal are covered. These public tests
use an in-memory security store, not complete daemon/Postgres acceptance.

The full registry, reqbudget, gateway and public-daemon suites passed; gateway
took 59.197 seconds and public-daemon 0.986 seconds. After refining detached
budget/protocol checks, final focused gateway tests passed in 4.538 seconds.
Pinned lint passed registry/gateway/public-daemon with zero findings. Public
routing snapshot completeness, bounded decision evidence, preview agreement,
customer status/capability delivery and native/daemon/load/deployment acceptance
remain pending. No Linux x86_64 KVM host is currently available, and no release
guarantee is marked accepted.

Public HTTP dispatch now pins owner identity, scoped positive weights,
release resolution/expiry, revision eligibility and host-pinned deployment
availability in one read-only repeatable-read Postgres view, bounded to 250 ms.
The selected deployment stays fixed through cold wake, capacity wait, retries
and detached cache refresh. Cache partitions include the deployment for ordinary
and keyed requests. Valid instance affinity stays in the verified positive roster
and respects readiness; version affinity takes precedence. Pure edge answers
do not read unused dispatch inputs. Policy proof includes routing verdicts and
weights, while choice and reason are separate request span attributes.

Cold requests retain one app queue across cohorts and one total wait allowance,
plus the gateway-wide admission queue and browser retry page. Ordinary burst
expansion retains one app worker and pins every scheduler batch member and
continuation to the admitted deployment. Steady app/plan limits are retained;
the existing bounded rollout overlap applies to a cold second positive cohort
beside a routable cohort and explicit verification. The scheduler owns that
capacity decision. Store failures, missing verdicts, malformed/oversized rosters
and unavailable cohorts refuse before wake rather than falling back to live
picker weights or a warm sibling. Existing pin validation and 410/503 release
refusal contracts remain.

Real Postgres tests passed for a cutover committed between owner and routing
reads, retained/expired release graphs and direct pins, scoped weights,
missing/foreign ownership, incomplete graphs, the 100-row refusal bound and
transaction cleanup. Full gateway, scheduler RPC and internal-gateway suites
passed in 61.393, 1.070 and 9.919 seconds. Final focused gateway/RPC checks
passed in 2.900/0.663 seconds after the cold-overlap refinement. Local checks
cover immutable wake/retry routing, total-deadline consumption, cache partition
and refresh, instance readiness/affinity, shared queue limits, bounded
cross-cohort waiting, generation cleanup, browser detached wake and coherent
burst RPC continuation. Full-path and native acceptance remain pending.

Pinned lint passed gateway, scheduler RPC and internal-gateway code with zero
findings. State production lint passed with tests=false/unused disabled for the
existing test-only helper. Direct sqlc v1.31.1 regeneration matched all four
generated Go files. The new reads use existing schema; no migration was added.

Atomic host/alias/environment binding with earlier resolved app settings,
cross-process exact-wake coalescing, complete synthetic admission/security
ownership, bounded decision evidence, preview agreement, customer status and
capability delivery, and remaining native/daemon/load/deployment acceptance
still require work. No Linux x86_64 KVM acceptance host is available; all six
release guarantees remain unaccepted.


### Named environment aggregate mutation guard, 2026-09-30

The account-locked Postgres projection now includes registered environment/app
identities and scalar measurements of their edge overlays. Exact stable URL
markers split the host-selector analysis. Runtime and management share the
pure host identity encoder and the app-filter/headers/CORS replacement helper.
Missing overlays keep fallback rules; explicit empty overlays suppress them.
Synthetic rules retain runtime identity/default fields. Referenced presets are
deduplicated after replacement. Original account read counts/canonical bytes,
post-filter compiler counts/escaped bytes and overlay contract bytes have
separate bounds. Unused siblings and replaced presets do not consume compiler
capacity, while all original matching rules still count toward the SQL read.
Action bodies stay in Postgres during analysis.

Overlay replacement, rule/preset changes, new environment registration and
clones use the same before/after transaction. Newly registered URLs start with
no serving-policy baseline, so an old oversized wildcard cannot authorize a
new oversized scope. Refusal rolls back the environment and copied scoped
configuration. Existing over-limit scopes retain non-increasing repair when
the bounded analysis finishes.

Focused state regression passed 80 cases with no failures/skips in 35.022 s.
Real Postgres fixtures compare analysis sizes/counts to the production runtime
projection for fallback, replacement and explicit empty overlays; verify
original account-read bounds and scalar-only metadata; race an overlay and a
rule for the last compiler allowance; check rejected intent/change-ledger
rollback; refuse new URL/clone exposure of legacy overload; and clone after
repair. Gateway/environment checks passed 18 cases with no failures/skips
(gateway 1.754 s, wire 1.066 s, internal gateway 9.414 s). These include existing
HTTP ownership, missed-notification and immutable-admission fixtures. Fresh
sqlc v1.31.1 generation matched all four generated files. The CLI docs-domain
tripwire passed in 2.224 s. Pinned lint passed with zero findings for state
production code (tests and the pre-existing test-only unused helper excluded)
and for hostidentity, gateway, wire and internal gateway with tests enabled.
Diff whitespace checks passed. Disk-exhausted build attempts are excluded
from this evidence.

App membership/reactivation writer integration, global synthetic route
aggregate validation and the MemStore aggregate mirror remain pending. Bounded
decision evidence, preview/runtime and complete synthetic-path agreement,
complete daemon/load/recovery and customer/staging rollout evidence still need
completion. No native Linux x86_64 KVM acceptance host is available; VM/restore,
nft connection/source-IP, process-death and leak checks remain pending. All six
release guarantees remain unchecked.

### Registered environment app activation guard, 2026-09-30

Postgres app creation, quota/activity creation, preview batch/set replacement,
project apply/reconcile, restore (including activity), deleted-to-live status
updates/CAS, and internal-to-public visibility updates now acquire the account
traffic lock before app/FK locks. The existing before/after host analysis runs
before commit. Refusal rolls back app intent together with project, preview-set,
cron and activity-outbox changes. Existing quota/conflict, restore grace/claim
and CAS predicates are retained; ordinary park/wake transitions keep their
existing path. Registered URL eligibility follows runtime status and public
visibility, including reactivation with a retained historical deletion stamp.
Removing an environment/app URL does not expose an unknown-host fallback.
The affected HTTP entry points retain the typed 422 count/byte/analysis refusal
codes and do not emit change notifications after rejected writes.

The final regression used the original source and test files, without a Go
overlay: 117 state cases passed with no failures/skips in 58.936 s. Real
Postgres fixtures cover 15 activation writers against a legacy oversized
wildcard, atomic rollback and repair/retry, plus concurrent restore and rule
updates sharing the last environment compiler allowance. Existing quota,
project/reconcile, preview, lifecycle/activity, policy mutation and host-analysis
regressions are included. HTTP/API regression passed 60 cases with no
failures/skips (apid 1.902 s, API 2.555 s). The HTTP refusal fixtures exercise
real handlers with an in-memory store wrapper; they are not full daemon
acceptance. Pinned lint reported zero issues for API/apid with tests enabled and
state production with tests=false/unused disabled for the existing test-only
helper. Fresh sqlc v1.31.1 generation matched all four generated Go files.
Whitespace checks passed. Disk-exhausted build attempts are excluded.

Primary-hostname and alias/domain activation, global synthetic route aggregate
validation and the MemStore aggregate mirror remain pending. Bounded decision
evidence, preview/runtime and complete synthetic-path agreement, complete
daemon/load/recovery and customer/staging rollout evidence still need
completion. The user confirmed no Linux x86_64 KVM acceptance host is currently
available. Native VM/restore, nft connection/source-IP, process-death and leak
checks remain pending; all six release guarantees remain unchecked.

### In-memory owned host aggregate mutation guard, 2026-09-30

MemStore now projects proposed rules, referenced presets, environment overlays,
environment registrations/clones and app activation rows under its existing
mutex and runs the shared before/after host analyzer before publication.
Project apply preflights its complete app/environment set; reconcile retains
its rollback maps through the final verdict. The quota/activity, preview batch
and preview-set paths retain their related rollback behavior. Ordinary
park/wake transitions keep their existing path. Clones include copied overlays
in the proposed view, avoiding an intermediate fallback policy.

Canonical measurements retain MemStore's existing conservative JSONB
whitespace/numeric estimator. Compiler measurements use actual typed Go JSON,
including compact RawMessage numbers, and count each referenced preset once.
The shared environment host encoder and runtime rule projection supply exact
identities, app filtering and explicit-empty replacement. Input/metadata,
automaton and phase-time limits remain centrally defined. Unused presets and
other accounts do not consume an owned host's allowance. The read projection
contains scalar identities/measurements rather than action bodies.

The final no_pg state regression passed 248 named cases with no failures/skips
in 3.102 s. It includes MemStore conformance, existing quota/plan/reconcile,
preview/activity, edge/preset, clone and projection recovery cases; new tests
cover overlap create/quota/retarget/enable refusal and repair, two different
apps racing for one allowance, 17 activation/environment/clone rollback and
repair cases, cancellation, and production Go compiler size agreement for
fallback/explicit-empty overlays and shared presets. An actual near-64-MiB Go
compiler input verifies that preset growth and an escaped environment overlay
each refuse the compiler aggregate without changing saved intent, then succeed
after removing the large contributor. Runtime matcher cases cover the shared
exact-or-SQL-LIKE parser, rich wildcards, escaping, Unicode and newline matching;
the analyzer parser was already verified against real Postgres. Exact/global/
plain-suffix matches retain their fast paths. The affected HTTP/API regression
passed 60 cases without failures/skips (apid 1.947 s, API 0.673 s) before this
matcher refinement; its write guards and HTTP mappings were unchanged by the
refinement. These are selected handler/store checks, not full daemon acceptance.
Pinned golangci-lint v2.4.0 reported zero issues for state with tests enabled
and no_pg; unused was disabled for the pre-existing test-only helper. Whitespace
checks passed. This slice changes neither SQL nor schema.
The earlier build attempt with no test output is excluded from this evidence.

Primary-hostname and alias/domain activation and global synthetic route
aggregate validation remain pending. Bounded decision evidence,
preview/runtime and complete synthetic-path agreement, complete
daemon/load/recovery and customer/staging rollout evidence still need
completion. No native Linux x86_64 KVM host is currently available; native
VM/restore, nft connection/source-IP, process-death and leak checks remain
pending. All six release guarantees remain unchecked.

### Global route aggregate mutation guard, 2026-09-30

Route creates and updates now validate enabled route-only discovery across
accounts, after the existing owned verdict and before intent/change-ledger
commit. The bounded scalar projection reuses the exact-or-SQL-LIKE host
analyzer, canonical count/byte bounds and conservative Go compiler estimate.
Disjoint hosts retain separate allowances, including when a small wildcard
connects their selector graph; this is not a flat global byte quota. Other rule
kinds, presets and environment overlays do not enter global route discovery.
MemStore runs the equivalent global verdict under its existing mutex.
Global refusals retain the structured 422 codes with `global_route_` scopes.
Unsupported legacy route action shapes refuse global writes until repaired;
non-route mutations in other accounts retain their owned scope.

Positive traffic mutations now acquire their account session lock before
starting the repeatable-read transaction and taking its account row lock.
Route mutations first acquire the global route session lock. This ordering
prevents a snapshot from predating the previous serialized writer's commit.
The stable before/after view also prevents a concurrent deletion/cascade from
subsidizing a growth write against legacy overload. Contenders release locks
and the direct-pool connection before retrying. Commit/rollback release the
session locks; uncertain lock grants or failed unlocks close the session.
The hub-enabled apid direct sibling reserves three connections for the hub,
outer convergence lock and guard transaction. The API process mutex admits
one outer edge mutation lock. An explicitly pooled control plane must include
that sibling pool in its operator capacity budget; the deployed compute
pooler topology keeps the existing ordinary control-plane pools.

Successful selected verification, with no failures/skips in these runs:

- 12 new Postgres cases in 12.594 s: cross-account quota race, disjoint and
  connected selectors, retarget rollback, deletion-credit refusal and fresh
  repair retry, pool/app-lock waiters, scalar/runtime projection agreement,
  serialization before the snapshot, canceled rollback, failed unlock and the
  actual API direct pool with hub/outer-lock connections already occupied.
- 24 existing external Postgres cases in 14.408 s: mutation lock order, account
  preset quota, pool waiters, edge/preset/environment projection recovery,
  canonical boundaries, legacy/event rollback and clone atomicity.
- 211 no_pg state cases in 3.064 s: MemStore conformance, shared host analyzer,
  global/owned overlap and rollback, activation/environment/clone recovery,
  runtime selector agreement and actual Go compiler boundary fixtures.
- 56 database/API/HTTP cases: db 1.501 s, API 0.700 s, apid 1.590 s. Global
  count, byte and analysis refusals exercise real create/update handlers and
  verify preserved intent, structured fields and aborted convergence.
- Pinned golangci-lint v2.4.0 reported zero issues for state/db production
  with tests=false (unused disabled for the pre-existing test-only helper),
  and db/API/apid with tests enabled. Fresh sqlc v1.31.1 generation matched
  db.go, models.go, querier.go and queries.sql.go exactly. Whitespace checks
  passed. No schema or migration changes are required.

The state verification uses temporary Go overlays outside the repository to
omit unrelated external state tests. All production and internal test sources
are retained. The external regression retains fourteen original fixture/test
files; the no_pg profile retains the original conformance and two common
projection test files. These runs do not establish the full package/make-test
or daemon acceptance gate. A broader internal run passed 131 named cases but
failed the new direct-pool fixture because pgx ConnString retained the source
DSN after the clone harness changed Config. The fixture now carries the actual
database/search path and passes in the final new-Postgres run. Earlier full
builds and broader reruns failed from local disk exhaustion during linking or
Postgres clone creation; those attempts are excluded from passing evidence.

This first global guard conservatively checks the complete route-selector
language. Actual claimed/reserved-host exclusions and newly unclaimed scope
transitions still need integration with the hostname binding projection.
Primary-hostname and alias/domain activation, bounded decision evidence,
preview/runtime and complete synthetic-path agreement, full daemon/load/
recovery and customer/staging rollout evidence remain pending. The user
confirmed no native Linux x86_64 KVM acceptance host is available. VM/restore,
nft connection/source-IP, process-death and leak checks remain pending. All
six release guarantees remain unchecked.

### Configured primary-host activation and YAML import repair, 2026-09-30

This goal turn made progress on ordinary primary app URL activation. PgStore
and MemStore receive an immutable apps-domain option. The API writer supplies
its existing TOML/environment getter; GitHub preview/reconcile supplies the
same DNS value through its config, manifest renderer and managed systemd
drop-in. The common default is the existing API default `gregale.dev`; an
explicit empty domain disables this namespace. Deployment URLs keep their
separate suffix. Runtime and management share suffix normalization, one-label
slug extraction and the higher-priority environment/deployment parser.

Eligible public, non-deleted app primary URLs join the owned scalar SQLC
metadata projection and its input/byte ceilings. Exact literal markers split
the before/after automaton at those URLs. New publication/restore starts with
no serving-policy baseline; unchanged legacy overloads cannot authorize a new
overloaded URL. Removed primary scopes cannot become global discovery while
their app slug remains reserved. Ordinary primary compilation retains all
matched account rules and distinct presets, including sibling app rules; only
named environments use the app filter. In-memory projection and publication
use the same analyzer under the existing mutex.

All 15 existing guarded app writers now enforce this primary scope. The new
Postgres and in-memory activation profiles use a custom apps domain, no
registered environments and an exact target selector to isolate the primary
guard. Refusals retain app/project/cron/preview/activity intent; supported
policy deletion and retry succeeds. Namespace tests cover custom/disabled
domains, immutable shape precedence, literal legacy metacharacters, scalar
metadata bounds and separation from global route-only analysis. The existing
HTTP activation profile verifies structured 422 count/byte/analysis errors
and unchanged intent/notifications for create, restore and publication.

The full in-memory suite exposed a valid YAML OpenAPI fixture rejected by the
new JSON projection check. Both plain and quota import writers now normalize
YAML through the API validator's parser before checking and saving its JSONB
representation. JSON source numbers and formatting remain intact for the
canonical size check. Upload size/hash metadata retains the original input.
Shared PgStore/MemStore tests preserve declared routes and metadata and reject
a small YAML alias document whose normalized representation exceeds 512 KiB,
without replacing the saved import. The original YAML snapshot fixture remains
unchanged and passes; JSON numeric-expansion refusal/recovery also passes.

Final evidence (full source sets, no Go source overlays):

- Selected PostgreSQL/state traffic regressions: 217 named cases, no failures
  or skips, 81.857 s. `/tmp/gregale-primary-state-verified-pg-20260930.jsonl`.
- Full `pkg/state -tags no_pg` suite: 1,867 named cases pass, 772 existing
  guarded integration skips, no failures, 4.514 s.
  `/tmp/gregale-primary-full-no-pg-final-20260930.jsonl`.
- Import/snapshot/HTTP regressions: 63 named cases, no failures or skips.
  `/tmp/gregale-primary-import-api-20260930.jsonl`.
- Writer config and deployment parser regressions: 88 named cases pass.
  `/tmp/gregale-primary-writer-config-20260930.jsonl`.
- Shared identity, renderer and manifest tests: 182 named cases pass.
  `/tmp/gregale-primary-config-20260930.jsonl`.
- HTTP activation refusal/rollback profile: 10 named cases pass.
  `/tmp/gregale-primary-activation-http-20260930.jsonl`.
- Pinned production lint passes for state, identity/import, renderer/manifest
  and API/GitHub/internal gateway packages (`tests=false`, existing test-only
  unused helper disabled). `/tmp/gregale-primary-production-lint-20260930.log`.
  Identity/import, renderer/manifest and API/GitHub lint with tests also passes.
  `/tmp/gregale-primary-tests-lint-20260930.log`.
- Fresh SQLC v1.31.1 output matches all four generated files; whitespace check
  passes. No schema or migration changes. The first full no_pg attempt's YAML
  failure and one later Postgres clone disk-exhaustion failure are excluded
  from passing evidence; both complete profiles above are successful reruns.

The primary guard currently includes potential legacy tag-prefixed app URLs
conservatively. Exact alias shadowing/activation, domain binding transitions,
operator namespace changes and global claimed/reserved exclusions/newly
unclaimed scopes still require the complete hostname binding projection.
Bounded decision evidence, preview/runtime/full synthetic-path agreement,
full daemon/load/recovery and customer/staging rollout acceptance remain
pending. No native Linux x86_64 KVM acceptance host is available; VM/restore,
nft connection/source-IP, process-death and leak checks remain pending. All six
release guarantees remain unchecked.

### Alias publication aggregate guard, 2026-09-30

New serving deployment aliases join the configured apps-domain host projection.
SQLC returns only their stable host labels, with the same public-owner,
deletion-metadata and target-status predicates as routing, including retained
superseded targets. Alias identities count toward the scalar metadata input
and byte bounds. The aggregate header estimate now measures the empty JSON
object's actual serialized size rather than using a fixed header allowance.
In-memory projection uses the same host identity and runtime predicates.
Legacy SQL labels remain visible to reads; new alias allocation retains the
existing DNS-label limit. Tombstone/internal app slugs retain their alias
collision reservation in both stores.

Alias publication takes the account serialization lock before its stable
before/after transaction. The collision lookup, upsert and aggregate verdict
use that transaction. A new URL starts with no serving baseline; an existing
overloaded selector cannot authorize exposing it. Retargeting an unchanged
serving URL retains the legacy incremental-repair contract. Alias scopes use
all matched account rules and distinct referenced presets, including sibling
app policy. App restore and visibility publication validate attached aliases
in the same projection. Refusal rolls back saved intent and returns the
existing structured byte/count/analysis 422 errors without notification.

Final evidence (full source sets, no Go source overlays):

- Selected PostgreSQL traffic regressions: 254 named cases pass, no failures
  or skips, 114.825 s. `/tmp/gregale-alias-broad-pg-20260930.jsonl`.
  Includes publication/new-URL rollback and repair, unchanged-URL retarget,
  custom/disabled namespaces, runtime target-status and legacy-label agreement,
  scalar metadata refusal, five app restore/publication paths, and an alias
  writer canceled behind the account lock with no saved row, no retained app
  lock and successful retry.
- Full `pkg/state -tags no_pg` suite: 1,881 named cases pass, 772 existing
  guarded integration skips, no failures, 4.478 s.
  `/tmp/gregale-alias-full-no-pg-20260930.jsonl`.
- Runtime routing, alias CRUD/refusal, API and shared host identity profile:
  36 named cases pass, no failures or skips. Internal gateway 5.405 s,
  apid 1.328 s, API 8.248 s, identity 0.426 s.
  `/tmp/gregale-alias-http-routing-20260930.jsonl`.
  Fresh Postgres host snapshots retain alias retargeting without notifications;
  actual HTTP publication verifies structured refusals and unchanged intent.
- Pinned production lint passes for state/API/identity/apid/internal gateway
  (`tests=false`, existing test-only unused helper disabled).
  `/tmp/gregale-alias-production-lint-20260930.log`.
  API/identity/apid lint with tests also passes.
  `/tmp/gregale-alias-tests-lint-20260930.log`.
- Fresh SQLC v1.31.1 output matches all four generated files; whitespace check
  passes. No schema or migration changes. The first empty-output/disk-pressure
  build and the initial missing deployment-kind and superseding-fixture failures
  are excluded from passing evidence; the final profiles above pass.

This completes the local alias-publication guard slice, not the complete
hostname binding projection. Exact legacy alias shadowing, alias removal and
fallback transitions, deployment-status resurrection, custom-domain binding,
operator namespace changes and global claimed/reserved exclusions/newly
unclaimed scopes remain pending. Bounded decision evidence, preview/runtime
and full synthetic-path agreement, full daemon/load/recovery and customer/
staging release acceptance remain pending. The user confirmed no native Linux
x86_64 KVM acceptance host is available. VM/restore, nft connection/source-IP,
process-death and leak checks remain pending. All six release guarantees
remain unchecked.

### Deployment alias revival guard and writer DNS wiring, 2026-09-30

Generic writes to an alias-eligible deployment status now share account
serialization and the stable before/after policy transaction. Cancelled rows
retain their terminal fence. Mark-live, including its Git-driven variant,
takes that account guard before the existing app/deployment locks. Cutover,
cron reactivation, OpenAPI snapshot and outcome activity writes remain inside
that transaction. A failed target cannot revive an attached alias against an
old oversized selector. Existing eligible targets retain the incremental
repair baseline. MemStore validates a proposed target status before saving
any associated intent or enqueueing webhooks and applies the same bounded
analysis to positive status writes.

The writer audit found that dark promotion requires pending/live targets,
while rollback preparation, automatic rollback, canary/service transitions
and build allocation only move already eligible targets among alias-eligible
statuses. They do not newly expose this alias scope. Immutable revision URL
activation and exact legacy shadow/fallback binding still require their own
complete projection.

Builderd, imaged and schedd now receive the configured apps domain through
TOML and its environment overlay, using the existing shared default. Their
store wiring passes that immutable value. The manifest catalog/renderer
includes it in all three writer configs. A shared systemd drop-in carries the
operator DNS value to builderd, compute imaged/schedd and control-plane
schedd. The generated environment contract records the additional owners.

Final evidence (full source sets, no Go source overlays):

- Selected PostgreSQL traffic/deployment regressions: 326 named cases pass,
  no failures or skips, 128.385 s.
  `/tmp/gregale-alias-revival-broad-final-pg-20260930.jsonl`.
  Covers all six positive statuses, stable/manual/service/canary mark-live
  revival rollback, unchanged serving URLs, dark promotion, cancelled target
  refusal, canceled analysis/fresh retry, account-lock cancellation and retry,
  and existing snapshot, promotion-fence, service/canary and traffic behavior.
  Refused revival preserves deployment, alias, cron, snapshot, activity and
  webhook delivery intent; supported policy repair and retry succeeds.
- Full `pkg/state -tags no_pg` suite: 1,896 named cases pass, 772 existing
  guarded integration skips, no failures, 4.983 s.
  `/tmp/gregale-alias-revival-full-final-no-pg-20260930.jsonl`.
- Writer config, renderer, manifest, environment contract and Ansible profile:
  74 named cases pass, no failures/skips. Builderd 0.876 s, imaged 0.808 s,
  schedd 0.778 s, renderer 0.344 s, manifest 0.367 s, unit specs 1.536 s.
  `/tmp/gregale-alias-revival-config-final-20260930.jsonl`.
  Custom/default/disabled config and environment precedence are exercised.
- Full local image/build library and imaged/builderd/schedd command suites:
  1,203 named cases pass, 24 existing guarded skips, no failures.
  Image library 7.518 s, builder library 7.111 s, imaged 0.865 s,
  builderd 1.594 s, schedd 1.019 s.
  `/tmp/gregale-alias-revival-pipeline-final-20260930.jsonl`.
  These verify the local consumers of positive deployment status writes;
  they do not establish native or deployed daemon acceptance.
- Pinned production lint passes for state, all three writer commands,
  renderer, manifest and unit specs; state uses tests=false and disables the
  existing test-only unused helper. Writer/config packages also pass lint
  with tests enabled. The final state parity change passes production lint.
  `/tmp/gregale-alias-revival-production-lint-20260930.log`,
  `/tmp/gregale-alias-revival-state-final-lint-20260930.log`,
  `/tmp/gregale-alias-revival-tests-lint-20260930.log`.
- Fresh SQLC v1.31.1 output matches all four generated files. The three changed
  Ansible task files parse as YAML; their resolved shared Jinja template
  renders custom/default/empty domains. Generated environment docs are in
  sync; whitespace checks pass. No schema or migration changes.

The earlier alias metadata fixture supplied status to CreateDeployment, which
Postgres always initializes as pending. It now explicitly transitions and
reads back each of all eight actual statuses before comparing projection to
runtime eligibility. The earlier named status cases do not establish those
individual status branches; the final profile above does. The mark-live
fixture likewise explicitly promotes its base before allocating a candidate.
Initial fixture-interface, manual-base, imaged config-name and stale generated
contract failures, plus disk-exhausted builds, are excluded from passing
verification. The final profiles above pass after those corrections.

Exact legacy alias shadowing, alias removal/fallback transitions, immutable
revision activation, custom-domain binding, operator namespace changes and
global claimed/reserved exclusions/newly unclaimed scopes remain pending.
Bounded decision evidence, preview/runtime/full synthetic-path agreement,
full daemon/load/recovery and customer/staging release acceptance remain
pending. No native Linux x86_64 KVM acceptance host is available; VM/restore,
nft connection/source-IP, process-death and leak checks remain pending. All
six release guarantees remain unchecked.

### Ordinary custom-domain publication guard, 2026-10-01

Plain and challenge-bound verification now use the owning account's guarded
traffic transaction. The bounded scalar projection includes verified ordinary
domains attached to public, non-deleted apps. Exact domain markers are literal;
wildcards match the runtime's strict suffix language, including nested
subdomains and excluding the apex and any literal asterisk. Domain/app identity
gives newly published bindings a zero baseline; unchanged bindings retain the
incremental repair allowance. Public visibility and app restoration validate
the same attached ordinary domains, including when the apps namespace is empty.

The verification write repeats the discovered app owner and, for challenge
verification, the token, unverified state and expiry against the wall clock.
Account-lock waiting cannot verify a reclaimed owner's domain or resurrect an
expired challenge using the transaction's start time. Refusal rolls back
verification. Quota claims take the account row before the app row to avoid
reversing the guarded mutation's lock order. MemStore validates proposed
verification before publishing under its existing mutex.

The DNS poller preserves certificate intent and emits no certificate work on
refusal or stale proof. Its new fixed-label publication counter distinguishes
success, stale proof, policy refusal and store error separately from TXT probe
results. Repair followed by the existing retry operation publishes successfully.
Polling backoff remains independent of publication.

The broad PostgreSQL regression exposed repeated escape scans over numerically
expanded compiler text reaching the existing SQL deadline. Compact JSONB string
values and object keys now establish escape presence when formatting amplifies
the input beyond the maximum sixfold string escape expansion; ordinary small
rows retain their text checks. Byte estimates, defaults, quotas and phase
deadlines are unchanged. The original legacy numeric environment-clone fixture
is unchanged. Numeric amplification with sensitive keys, values and Unicode
separators is checked against the actual decoded runtime projection.

Final evidence uses full source sets without Go source overlays on Darwin
arm64, Go 1.25.13 and PostgreSQL 16.15. Go runs use CGO_ENABLED=0, serialized
package execution, vet disabled, inlining/DWARF disabled and stripped links.
The PostgreSQL runs use an owned local cluster and private migrated test
templates; the source database remains unmigrated.

- Selected PostgreSQL traffic, domain, deployment and promotion regressions:
  372 named cases pass, no failures or skips, 173.793 s.
  Covers verification refusal/repair, binding identity, wildcard language,
  owner reclaim during a real account-lock wait, expiry after transaction
  start, visibility publication, scalar metadata bounds, account-before-app
  cancellation/retry, quota/activity rollback, alias revival, environment
  clone and the existing snapshot/service/canary/promotion fences.
  `/tmp/gregale-domain-broad-post-escape-pg-20261001.jsonl`.
- Full `pkg/state -tags no_pg`: 1,914 named cases pass, no failures,
  772 existing guarded database skips, 5.191 s.
  `/tmp/gregale-domain-full-final-no-pg-20261001.jsonl`.
- Full `cmd/apid`: 3,628 named cases pass, no failures,
  16 existing guarded database skips, 68.926 s.
  Includes DNS publication outcomes, unchanged certificate intent and
  notification refusal followed by successful repair/retry.
  `/tmp/gregale-domain-full-api-20261001.jsonl`.
- Focused PostgreSQL regression: 20 named cases pass, no failures or skips,
  54.029 s. Includes the unchanged legacy numeric clone fixture and extended
  decoded-runtime byte estimator.
  `/tmp/gregale-domain-escape-final-pg-20261001.jsonl`.
- Production state/SQLC lint and API lint with tests pass, zero issues and
  successful exit codes. State uses tests=false and disables its existing
  test-only unused helper.
  `/tmp/gregale-domain-state-post-escape-lint-20261001.log`,
  `/tmp/gregale-domain-api-final-lint-20261001.log`.
- SQLC v1.31.1 regeneration exactly matches all four generated files.
  Whitespace checks pass. No schema or migration changes.

Compressed final logs and a machine-readable result summary are preserved in
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-domain-evidence-20261001/`.
Earlier interrupted, timed-out or failed profiles are excluded from this
passing evidence. The final PostgreSQL profile passes after the escape scan
change without weakening its numeric fixture or analysis deadline.

Named-environment custom domains, tenant surfaces, exact cross-account binding
shadowing, alias/domain removal and fallback, immutable revision publication,
operator namespace changes and global claimed/reserved exclusions still need
the complete binding projection and acceptance. Bounded decision evidence,
preview/runtime/full synthetic-path agreement, full daemon/load/recovery and
customer/staging release acceptance remain pending. No native Linux x86_64 KVM
acceptance host is available; VM/restore, nft connection/source-IP,
process-death and leak checks remain pending. All six release guarantees remain
unchecked.

### Environment-scoped domain policy and publication guard, 2026-10-01

Explicitly environment-bound verified domains now select the stable environment
URL's workload app filter and optional headers/CORS replacement. Missing policy
retains the workload fallback; an explicitly empty replacement suppresses its
headers/CORS. Authoritative compilation resolves the actual binding with the
captured router namespace in the same snapshot. An alias, exact ordinary domain
or higher-priority tenant binding cannot inherit a shadowed domain's overlay.
Fresh requests read the authoritative inputs before cache reuse; admitted
snapshots remain immutable. Oversized or failed scoped reads refuse forwarding.
ADR-375 records the decision, with follow-up references in ADR-233 and ADR-283.

Bounded aggregate analysis includes valid owned scoped-domain identities and
injects their environment overlays by binding rather than only by generated
URL. Overlapping potential ordinary, exact/wildcard scoped and generated
bindings are checked separately. A new domain/app/environment identity starts
with zero serving-policy allowance. Verification, overlay changes and positive
app visibility/status/restoration writers preserve intent after refusal and
succeed after repair. Raw owner rules are bounded before app filtering;
compiled bounds include only retained rules, overlays and distinct presets.

Public policy input is bounded to 253 hostname bytes before authoritative reads.
The centralized limit matches domain validation. Overlay estimates reserve the
maximum hostname and its sixfold JSON escape expansion. No account quota,
analysis deadline, schema or migration is changed.

Direct and snapshot wildcard reads now use SQLC literal suffix comparisons;
legacy percent and underscore characters cannot widen a match as SQL LIKE
operators. Whitespace normalization, one trailing host dot, supported ASCII DNS
case folding, byte-length specificity and lexical ties agree with the shared
Go matcher. Unverified wildcard reservations and complete management rows are
preserved. Asterisk-bearing request hosts cannot route or retain a cached domain
route. Exact snapshot lookup, reservations and both verification writes retain
citext domain identity; exact aggregate markers use lowercase request hosts.

Final evidence uses full source sets without Go source overlays on Darwin
arm64, Go 1.25.13 and PostgreSQL 16.15. Go runs use CGO_ENABLED=0, serialized
packages, vet disabled, inlining/DWARF disabled and stripped links. PostgreSQL
uses an owned local cluster and private migrated test templates; its source
database remains unmigrated.

- Selected PostgreSQL state traffic, domain, deployment and promotion
  regressions: 430 named cases pass, no failures or skips, 149.540 s.
  Covers scoped verification and overlay refusal/repair, eight positive app
  publication/restoration writers with preserved domain and activity intent,
  overlapping scopes, wildcard lookup/full-row parity and mixed-case identity.
- PostgreSQL public binding, route-source and edge-policy regressions:
  62 named cases pass, no failures or skips, 34.725 s. Includes captured custom
  namespaces, exact/wildcard scoped domains, 253-byte escaped input, shadowed
  bindings, old dispatch refusal and repair without notifications on HTTP/1
  and HTTP/2 peers. These are in-process listeners sharing PostgreSQL with
  fake scheduling/forwarding; they do not establish real daemon acceptance.
- Full `pkg/state -tags no_pg`: 1,939 named cases pass, no failures,
  772 existing guarded database skips, 7.741 s.
- Selected `pkg/gateway` policy, public routing, deadline and revocation
  regressions: 116 named cases pass, no failures or skips, 7.407 s.
- Production state/SQLC/API/gateway lint and internal gateway lint with tests
  pass, zero issues and successful exits. Production lint uses tests=false and
  disables the existing test-only unused helper.
- SQLC v1.31.1 regeneration exactly matches all four generated files.
  Whitespace checks pass. Compressed final logs, command/profile metadata and
  changed-file hashes are preserved in
  `/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-env-domain-evidence-20261001/`.
  Earlier failed, empty-output and superseded runs are excluded from passing
  evidence. The existing limits and legacy numeric fixtures remain intact.

Tenant-surface publication, exact cross-account binding shadowing,
alias/domain removal and fallback, immutable revision publication, operator
namespace transitions and global claimed/reserved exclusions still need the
complete binding projection and acceptance. Bounded decision evidence,
preview/runtime/full synthetic-path agreement, real daemon/load/recovery and
customer/staging release acceptance remain pending. The user reports no native
Linux x86_64 KVM acceptance host available; VM/restore, nft connection/source-IP,
process-death and leak checks remain pending. All six release guarantees remain
unchecked.
