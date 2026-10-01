# Traffic platform implementation tracker

Objective: implement the six delivery steps in the 2026-09-29 gap-closure plan.
Base: `56618879c`; branch: `codex/traffic-platform-gaps`; decision: ADR-375.

## Managed attempt identity and forwarding correlation — 2026-10-01

The unchanged-runtime baseline reproduced managed retries retaining the caller's
correlation identity, mutating earlier bodyless attempt headers and omitting
endpoint ownership from the logical completion span. Real HTTP and raw RPCs
received prior-hop metadata instead of the canonical selected target envelope.

Each managed dispatch now clones the request and headers, stamps verified account
and endpoint identity into headers and context, and updates completion span
ownership immediately before forwarding. Causal wake/invocation identity is
preserved. Empty endpoint provenance clears stale values. A sibling refused by
policy does not replace the last actually forwarded owner. Upgrades use the same
identity preparation and keep single-dispatch semantics. HTTP and raw forwarding
explicitly publish the canonical context, replace reserved metadata and retain
unrelated transport values. Repeated publication does not accumulate duplicates;
legacy forwarding without a canonical context is unchanged. Correlation remains
diagnostic data rather than an authorization credential.

Focused cases cover nil/NoBody requests, replayable bodies, application errors,
disabled retry, missing provenance, a policy-refused sibling, Upgrade identity,
actual forwarding RPC metadata, 64 repeated hops, causal fields and cancellation.
The configured private service listener is also exercised in two real gateway
processes against Postgres. Fresh caller lookup, stored declared bindings and
reliability policy, production node dialing and forwarding verify four originals,
one shared retry, five RPCs and two guest executions across replacement within
one live database window. Spoofed caller identity never forwards; internal calls
do not create public rate debits. Source addresses, listener binds, placement,
VM forwarding and telemetry receivers remain explicit local fixtures.

Verification against the final 12,513-file source freeze:

- All nine complete unit packages pass in 164.065 s. The log contains 9,547
  named pass events and 1,438 skips. A further 46 passed parents whose children
  are entirely guarded are excluded from acceptance: 9,501 named results are
  accepted and 1,484 are guarded. Accepted package counts are state 2,124,
  internal gateway 789, scheduler 1,778, gateway 2,321, trafficrevocation 33,
  schedd 85, public gateway 93, API 1,813 and wire 465.
- The selected Postgres profile passes 149 named results with no skips in
  66.850 s: 35 results under 20 actual Postgres fixture roots and 114 memory/
  transport checks. The configured managed-listener case again verifies one
  live retry window across replacement and agreement between RPC and guest
  identity. Parent-only passes from guarded unit subtests are not Postgres
  acceptance.
- Across both profiles, 9,536 distinct named results are accepted; 1,462
  guarded results remain without acceptance. Pinned lint 2.4.0 reports zero
  issues for all nine complete packages with tests in 36.507 s. SQLC 1.31.1
  reproduces all four generated files exactly. Runbook SQL, text encoding,
  shell quoting and ADR uniqueness gates pass in 45.015 s, retaining the
  71 pre-existing duplicate groups.
- Go and lint run serially with CGO disabled, GOMAXPROCS=2, GOGC=50, one
  package/analysis worker, disabled inlining/DWARF and stripped test binaries.
  Lint uses its task-owned cache, permits unrelated checkout runners and
  emits all diagnostics. No new suppressions, source exclusions, overlays or
  weakened assertions were added. Only this tracker changes after the final
  source freeze. The disposable source database remains unmigrated, with
  fsync, synchronous_commit and full_page_writes enabled.

Focused results and the corrected fixture's production-slug diagnostic remain
outside accepted final counts. Raw Go events, guarded parents and accepted
results are retained separately in the receipts.
Two intentionally interrupted runs have no terminal result and are retained.
The earlier temporary build cache and Postgres cluster were absent on resume;
a fresh task-owned cluster uses port 55491, leaving sibling clusters intact.
The global lint lock wait, cache-load timeout and context-inheritance diagnostics
are retained. Preparation now returns its inherited context explicitly for retry
and trace operations, including the minted assertion marker. Complete-package
preliminary lint passes without new suppressions. Task-owned heavy runs remain
serial; lint uses a separate cache and permits unrelated checkout runners, with
one compiler/analysis worker and all diagnostics visible.

Evidence: `outputs/traffic-managed-retry-20261001/` relative to this checkout's
parent. All six release requirements remain open. No native Linux x86_64 KVM
acceptance host is available; native network/lifecycle/leak checks, complete
path qualification, deployed load, recovery and staging remain pending.

## Bounded request evidence shutdown — 2026-10-01

The unchanged-runtime baseline reproduced lost shutdown evidence: the publisher
received an already canceled daemon context, dropped all ten represented requests
in the unit fixture and emitted zero pending RPC records from either real daemon.
Cancellation during an ordinary upload also discarded the drained batch.

The publisher now pauses ordinary uploads after daemon cancellation while HTTP
producers drain. Explicit stop cancels the active RPC, retains its exact collapsed
payload and event IDs, and flushes that payload plus queued evidence with one
independent deadline. Every concurrent stop caller joins the same actor. A known
successful upload counts once; deadline loss counts represented logical requests.
Stop before start is terminal. Shipping callbacks must honor cancellation.

The final publisher budget is at most two seconds. Debugger publishing, egress
RPC shutdown, trace export and retained-span cleanup share five seconds after
HTTP drain, with their cleanup deadline capped at 30 seconds from drain start.
Startup-error cleanup gets five seconds. Limits live in pkg/api/limits.go. These
contexts do not configure the service manager, bound every unrelated deferred
close, or guarantee evidence from producers outliving an exhausted HTTP drain.
Optional debugger records remain best effort across process death; financial
request usage retains its separate durable outbox.

Focused fixtures verify parent cancellation, active RPC interruption, exact-ID
ambiguous acknowledgment replay, multiple queued batches, shared owner deadlines,
loss accounting, start/stop races, all concurrent waiters and budget configuration.
Both real daemon processes publish pending request records during stop. Placement,
VM forwarding and the telemetry receiver remain fixtures. The existing actual
Postgres telemetry/log ledger replay fixture verifies atomic duplicate suppression
and rollback. These local checks do not establish deployed fleet or native VM
acceptance.

Verification against the final 12,507-file source freeze:

- The complete state, internal gateway, scheduler, gateway, trafficrevocation,
  schedd, public gateway and API unit scope passed 9,068 named checks in
  154.514 s. Package passes are 2,159, 799, 1,778, 2,307, 33, 86, 93 and 1,813.
  The 1,435 guarded/skipped results are not accepted passes.
- The selected Postgres profile passed 130 named checks with no skips in
  100.676 s: 33 named results under 18 actual Postgres fixture roots, plus
  97 memory/transport checks. Both daemon shutdowns emit the pending RPC record
  with the successful guest, app/account/deployment and trace identity. The
  independent ledger test preserves one telemetry/log event after duplicate
  replay and rolls back a failed telemetry write atomically.
- Across both profiles, 9,100 distinct named checks passed; 1,416 guarded results
  remain without acceptance. Lint v2.4.0 checks all eight complete packages with
  tests and reports zero issues in 74.351 s. SQLC v1.31.1 reproduces all four
  generated files exactly. Runbook SQL, text encoding, shell quoting and ADR
  uniqueness gates pass in 29.514 s, retaining the 71 pre-existing duplicate groups.
- Go and lint ran serially with one package compiler, CGO disabled, GOMAXPROCS=2,
  GOGC=50 and disabled inlining/DWARF. Test binaries are stripped. Source scope,
  assertions and profiles were not weakened. Only this tracker changed after
  the accepted source freeze. The disposable source database stayed unmigrated,
  with fsync, synchronous_commit and full_page_writes enabled.

The failed baseline, disk-full fixed-core linker run and disk-full preliminary
lint are preserved as whole diagnostics. A later lint diagnostic found the nil
stop context and context replacement; explicit context inheritance under the
lifecycle lock fixed both before the final freeze without suppressions. Earlier
focused handler/daemon checks and the diagnostic source freeze remain excluded
from accepted final counts.

Two cache cleanups ran only after owned heavy sessions terminated. The first
removed two obsolete API archives totaling 1,345,605,690 bytes. The second removed
1,182 older repository archives totaling 3,339,743,890 bytes, retaining newer
archives and standard dependencies. No sibling cache, process or Postgres cluster
was changed. Exact source, staged/committed content, diagnostics and gate receipts
are in the evidence directory:
`outputs/traffic-evidence-shutdown-20261001/` relative to the checkout's parent.

All six release requirements below remain open. Native Linux x86_64 KVM host
availability, complete path agreement, deployed load/outage/recovery,
forced-termination and staging qualification remain pending.

## Retry completion ownership — 2026-10-01

The unchanged-runtime baseline reproduced a second retry ownership bug. The
healthy sibling received the RPC and served the request, but successful activity,
per-instance usage, completion logs/spans and the real daemon publisher's 200
record still named the failed first instance. Buffered guest cookies also replaced
platform and affinity cookies.

The retry wrapper now returns the last target whose forwarder actually ran.
Completion uses that owner; a sibling refused before dispatch cannot replace it.
The original target stays immutable for detached mirrors, streaming hooks and
wake metrics. The request's wake cause and cold outcome survive replay without
adopting a sibling's cached historical wake. Empty final provenance clears stale
completion span attributes. Affinity is stamped per dispatch, and buffered
attempts inherit the original headers so platform and final guest cookies survive
while discarded failure cookies remain private.

Regressions cover successful and failed replay, an application error, disabled
retry, missing sibling provenance, security refusal, cancellation and streaming.
Two OS daemon processes use the production node client and debugger publisher;
the test observes its normal five-second tick and compares two logical records
with three RPC attempts and the one successful guest's identity. Placement,
VM forwarding and telemetry receivers remain explicit fixtures. This acceptance
does not prove production telemetry persistence or shutdown delivery.

The final complete unit scope passed 7,234 named checks in 143.008 s:
state 2,159, internal gateway 795, scheduler 1,778, gateway 2,290, traffic
revocation 33, scheduler daemon 86 and public gateway 93. The 1,434 guarded
results are not counted as passes. The selected Postgres profile passed 98
named checks in 74.280 s without skips: 31 named results under 16 actual
Postgres fixture roots, plus 67 memory/transport checks. Across both profiles,
7,264 distinct named checks passed and 1,417 guards remain unaccepted.

All 12,502 tracked and untracked source files were frozen before the accepted
gates. Pinned golangci-lint 2.4.0 reported zero issues for all seven complete
packages with tests in 40.710 s. SQLC 1.31.1 regenerated all four files exactly.
Runbook SQL, text encoding, shell quoting and ADR uniqueness gates passed in
34.574 s; the 71 pre-existing ADR duplicate groups remain at their baseline.
Go and lint ran serially with CGO disabled, one package worker, GOMAXPROCS=2,
GOGC=50, disabled inlining/DWARF and stripped test binaries. There were no
source exclusions, package-scope reductions, overlays or assertion changes
after the freeze. Only this tracker was updated after the accepted gates.

Diagnostics remain outside the accepted results. The initial daemon fixture
canceled the publisher before its normal tick and captured zero rows; the final
fixture observes that tick before shutdown. A combined baseline build-failure
event, earlier baselines and focused verification runs are retained. Preliminary
lint found a wrapped EOF comparison and an unused initializer; both were fixed
before freezing without suppressions. Two complete unit runs retained failures
at existing compiler analysis bounds and one real RPC's 100 ms handshake budget.
The unchanged-source third complete run passed. The diagnostic tests and direct
forwarding path were verified unchanged from the parent commit. The disposable
source database remained unmigrated, with durability settings on.

Evidence directory: `outputs/traffic-retry-completion-20261001/` relative to the
checkout's parent. All six release requirements below remain open. No native
Linux x86_64 KVM acceptance host is available; deployed load and staging remain
pending.

## Daemon fleet accounting and retry target identity — 2026-10-01

Two local OS processes now exercise parsed daemon defaults and `runWithDeps`
against independent Postgres pools. Stored app limits and cache/retry rules
drive the actual handler. Serving observations identify the selected central
rate backend and shared retry backend, and record replacement generations.
The production node client forwards over a Unix socket to a fixture VM endpoint
and one real HTTP origin. Placement, VM-side forwarding and telemetry service
endpoints remain fixtures; outer `run()` discovery is outside this acceptance.

The baseline reproduced a public retry dispatch bug: both RPCs named the failed
first instance even though the picker selected a sibling. One retry was spent,
no guest request ran, and the client received 503. Every public attempt now owns
a cloned header map and restamps the selected target's guest identity and
correlation context. The new regression also checks empty provenance clears
stale values and prior attempt headers remain unchanged.

The fleet fixtures cover the shared burst across two gateways, no forwarding
after rate refusal, store outage and bounded row-lock waits, preserved debt
after recovery and replacement, cache-hit app/account charging, warm-cache
outage refusal, account sharing across apps, and sequential scope accounting.
One finite retry minimum is shared by replicas and replacement within one
unchanged ten-second database window. Additional attempts do not cause another
app/account debit. Application 500 responses run once. Failure to observe an
original during a retry-store outage refuses replay, and recovery retains the
shared budget contract.

The final complete unit scope passed 7,224 named checks in 121.874 s:
state 2,159, internal gateway 795, scheduler 1,778, gateway 2,280, traffic
revocation 33, scheduler daemon 86 and public gateway 93. The 1,433 guarded
results are not counted as passes. The selected Postgres profile passed 87
named checks in 40.464 s without skips: 30 named results under 15 actual
Postgres fixture roots, plus 57 memory/transport checks. Deduplicated across
both profiles, 7,253 named checks passed and 1,417 guards remain unaccepted.

The daemon fleet admitted four of sixteen concurrent requests. Local p50 was
51.657 ms and p95 was 54.674 ms; these fixture measurements do not establish a
deployed SLO. Three daemon generations retained one spent retry for four
eligible originals, including one application 500. App and account balances
each fell by four rather than by the five forwarding attempts. Recovery and
replacement stayed within the same live database-owned retry window.

All 12,499 tracked and untracked source files were frozen before the accepted
gates. Pinned golangci-lint 2.4.0 reported zero issues for all seven complete
packages with tests in 37.106 s. SQLC 1.31.1 regenerated all four files exactly.
Runbook SQL, text encoding, shell quoting and ADR uniqueness gates passed in
20.963 s; the 71 pre-existing ADR duplicate groups remain at their baseline.
Go and lint ran serially with CGO disabled, one package worker, GOMAXPROCS=2,
GOGC=50, disabled inlining/DWARF and stripped test binaries. There were no
source exclusions, package-scope reductions, overlays or assertion changes
after the final freeze. Only this tracker was updated after the gates.

Earlier fixture runs retained import, workload-name, release-retention,
request-journal, TCP-without-mTLS, cleanup and global-generation diagnostics.
The unchanged runtime baseline retained the duplicate failed-instance RPCs.
Two lint diagnostics prompted explicit inheritance of the request context;
pre-context-fix unit/Postgres runs and earlier freezes remain recorded and
are excluded from accepted evidence. Two obsolete task-owned archives totaling
315,370,972 bytes were reclaimed only after owned Go sessions terminated.
Current build artifacts, other task caches, processes and Postgres were kept.
The disposable source database remained unmigrated, with durability settings on.

Evidence directory: `outputs/traffic-daemon-fleet-20261001/` relative to the
checkout's parent. All six release requirements below remain open. No native
Linux x86_64 KVM acceptance host is available; deployed load, full path coverage
and staging qualification remain pending.

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

### Custom-domain removal and newly unclaimed policy guard, 2026-10-01

Native PostgreSQL and in-memory domain removal now validate the policies
exposed by deleting an exact or wildcard claim before publishing deletion.
Actual exact/most-specific wildcard selection retains unverified blockers;
new domain/app/environment bindings have zero prior serving allowance.
Checks include cross-account and environment-scoped fallbacks, newly unclaimed
global discovery and each potential route owner's retained policy. An active
tenant shadow can still cause a conservative refusal because the potential
fallback with tenant surfaces disabled is checked; complete tenant transition
guards remain pending. ADR-375 and the request-policy operations guide record
the scope and repair behavior.

Global aggregate analysis now subtracts app, tenant and exact/literal-wildcard
domain reservations, plus the syntactically reserved tag/environment/revision
namespaces. Parser property checks cover canonical UUID padding, integer
ordinals, namespace precedence and punctuation lookalikes. The public wildcard
reservation read uses literal SQL suffix comparisons; percent and underscore
characters cannot widen a legacy claim as SQL LIKE operators.

The PostgreSQL deletion acquires the global route session lock and sorted
affected account session locks before account row locks and its repeatable-read
snapshot. Secret-free bounded claim metadata discovers owners twice. Each
owner policy is read separately and the whole owner-check loop shares the
existing analysis allowance. No action bodies cross owners and no limit,
deadline, schema or migration changes. The API carries the authorized app ID
through the lock wait and native SQL repeats the app predicate. Reclaimed names
and cancellation preserve domain/default/activity intent. An authoritative API
adapter without the owner-bound seam refuses deletion. Limit errors expose a
proven lower bound, without foreign counts or a hostname witness. Refusal emits
no removal audit or notification; repair followed by retry succeeds.

Final checks use full source sets without Go overlays on Darwin arm64,
Go 1.25.13 and PostgreSQL 16.15. Go uses CGO_ENABLED=0, serialized packages,
vet disabled, inlining/DWARF disabled and stripped links. PostgreSQL uses the
owned local cluster and private migrated test templates; its source database
remains unmigrated, with fsync, synchronous_commit and full_page_writes enabled.

- Selected PostgreSQL state traffic/domain/deployment/promotion regression:
  535 named cases pass, no failures or skips, 131.439 s. Includes all four
  deletion forms, legacy overload refusal/repair, exact/wildcard/scoped/global
  fallbacks, retained shadows, reservation parity, metadata bounds, original
  owner reclaim during a verified lock wait, cancellation/connection cleanup
  and a fallback policy committed by the previous account-lock holder.
- Public PostgreSQL binding/route-source/edge-policy regression: 63 named cases
  pass, no failures or skips, 23.213 s. New HTTP/1 and HTTP/2 peers preserve the
  original owner after refusal and select another account's wildcard fallback
  after repair/removal without notifications. Old dispatch refuses; its admitted
  policy remains immutable. These are in-process listeners with fake scheduling
  and forwarding, not real daemon or native acceptance.
- Full `pkg/state -tags no_pg`: 1,991 named cases pass, no failures,
  772 existing guarded database skips, 5.625 s.
- Full `cmd/apid`: 3,634 named cases pass, no failures,
  16 existing guarded database skips, 42.559 s. Includes refusal privacy,
  unchanged audit/default/notification intent and missing owner-bound seam.
  An earlier run failed the unchanged password timing test; three consecutive
  rechecks and the full rerun pass without changing authentication or its
  assertion. The earlier fixture-only metadata mismatch is also corrected.
- Production state/SQLC/API/gateway lint and API/internal-gateway lint with tests
  pass, zero issues and successful exits. Production lint uses tests=false and
  disables the existing test-only unused helper. SQLC v1.31.1 regeneration
  exactly matches all four generated files. Formatting and whitespace pass.

Compressed passing logs, diagnostic runs, command/profile metadata and changed
file hashes are preserved in
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-domain-removal-evidence-20261001/`.
Diagnostic runs are excluded from passing gate counts.

Positive binding publication/shadowing across accounts, complete alias and
tenant transitions, immutable revision activation, operator namespace changes
and other reservation writers still need their full guards and acceptance.
Bounded decision evidence, preview/runtime/full synthetic-path agreement,
real daemon/load/recovery and customer/staging release acceptance remain
pending. No native Linux x86_64 KVM acceptance host is available; VM/restore,
nft connection/source-IP, process-death and leak checks remain pending.
All six release guarantees remain unchecked.

### 2026-10-01: bounded public and managed-service decisions

ADR-375 records the design before runtime changes. Public routing requests and
managed HTTP service calls now own a fixed decision record. Seventeen scalar
span attributes report path, last phase, final outcome, actual proxy dispatches
and replays, first retry refusal, rejecting limiter family, cache outcome,
managed endpoint circuit verdict and measured policy/body/wake/capacity/backoff
durations. Public logs take a copy at their existing observation point. Spans
seal the final record; a later lifetime cancellation can differ from the log.

Vocabularies and counters are bounded. Unknown labels cannot retain credentials,
request content or error text. A child owns a new record, while detached cache
refresh clears the parent's carrier. Concurrent and nested measurements share
fixed state, coalesce each phase's overlapping work and saturate without overflow.
Successful stream and raw-upgrade handshakes record detachment at the transport
owner. Their expired admission timer cannot masquerade as a final deadline;
security revocation remains visible after headers. Cached 404/410 and fixed
preview replies remain edge answers. Guest errors and bridge-generated errors
after dispatch do not become inferred platform authentication/rate refusals.
Dispatch counts do not prove guest execution or rollback.

Final checks use full source sets without Go overlays, source exclusions or
weakened limits/timeouts on Darwin arm64 with Go 1.25.13. The existing serialized,
CGO-disabled, inlining/DWARF-disabled, stripped-link test profile is retained.
The owned PostgreSQL 16.15 cluster uses private migrated templates; its source
database remains unmigrated, with normal durability settings enabled.

- Full `pkg/gateway`: 2,179 named cases pass, no failures or skips, 62.813 s.
  Includes 60 focused decision/real-gRPC cases: every limiter family, store
  outage, cache/fixed response, guest 401, retry safety and amplification denial,
  raw public/service dispatch, upload/wake/capacity cancellation, request-owned
  state, bounded labels/counters, sealed records and detached refresh isolation.
  Existing real gRPC tests verify ordinary versus detached HTTP/gRPC/raw lifetime
  behavior and HTTP/1/HTTP/2 security revocation after headers with cleanup.
- Full `cmd/gatewayd-internal`: 724 named cases pass, no failures,
  72 existing guarded database skips, 4.860 s.
- Selected public binding/route-source/domain/edge-policy PostgreSQL regression:
  63 named cases pass, no failures or skips, 56.110 s.
- Gateway and internal-gateway lint with tests pass, zero issues. Formatting
  and whitespace pass. No SQL, schema, customer API contract or platform limit
  changes; earlier state/API/SQLC evidence remains recorded separately.

Diagnostic runs caught fixture omissions, the first service retry refusal being
overwritten, and context-checker warnings. Fixtures now provide the admitted
deployment roster, existing typed timeout, debug-level hot-success logger and
mandatory ordinary upgrade forwarder. The upgrade test verifies that the ordinary
forwarder is not dispatched. The first retry refusal is retained. Narrow context
annotations preserve the final rebound request and its lifetime fences; global
lint rules remain enabled. Passing counts exclude diagnostic runs.

Commands, compressed logs, diagnostics, profiles and changed-file hashes are in
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-decision-evidence-20261001/`.
The request-policy operations guide defines the fields and their limits. These
local checks establish the bounded record on these handler/transport paths;
they do not establish a latency SLA, real daemon or deployed acceptance.

All six release guarantees remain unchecked. Complete binding publication and
tenant/alias/revision/operator reservation transitions, preview/runtime and full
synthetic-path agreement, real daemon/load/recovery/customer/staging acceptance
remain pending. No native Linux x86_64 KVM acceptance host is available; native
VM/firewall/process-death/leak acceptance remains pending.

### Deadline/retry preview and forwarding agreement, 2026-10-01

The daemon compiler, forwarding budget resolver and read-only simulator now
share stored budget/retry normalization and the deterministic retry method
guard. API write defaults remain separate: a legacy stored zero retry floor
stays zero, and the forwarding loop still applies its low-traffic allowance
when spending. An invalid total deadline surfaces the owner's host-snapshot
verification refusal; a non-replayable retry action is omitted without
shadowing a later valid retry rule. Budget overrides are clamped as integers
before duration conversion, fixing a large-positive-value overflow that could
previously wrap into a short positive timeout. App timeout arithmetic is also
bounded before multiplication.

Preview selects the total-deadline candidate on the original public path and
immutable ingress headers, then retains that execution rule across rewrites.
A total field reached only after rewriting creates no ingress timer. Header
actions change forwarded values and execution overrides while later selectors
still consult the original snapshot. CLI JSON/text and dashboard render the
ingress candidate independently of the later execution step, so fixed
responses and incomplete cache/throttle traces retain it. The reported
enforcement state is explicitly unverified; configuration does not establish
signing-key availability, operator enablement, counter health or admission.
Routes to another app and missing plan ceilings remain incomplete.

The final source freeze covers 6,278 Go/source and embedded-input files, with
matching hashes after tests and lint. Verification uses the pinned Go 1.25.13
darwin/arm64, CGO-disabled, inlining/DWARF-disabled, stripped-link profile and
the owned caches; no source exclusions, overlays or limit/timeout assertions
were weakened.

- Full `pkg/api`: 1,813 named cases pass, no failures/skips, 1.231 s.
- Full `pkg/edgeruletrace`: 117 pass, no failures/skips, 0.404 s.
- Full `pkg/gateway`: 2,222 pass, no failures/skips, 62.052 s.
- Full `cmd/gatewayd-internal`: 736 pass, no failures, 72 existing guarded
  database skips, 4.677 s.
- Full `cmd/gregale`: 2,902 pass, no failures, two existing unavailable-shell
  skips, 29.483 s.
- Selected dashboard edge-rule/capability tests: 24 pass, no failures/skips,
  1.277 s. CLI and dashboard checks exercise real read-only handlers/rendering.
- 89 focused passing cases compare real forwarded execution stamps/deadlines,
  retry method guards, stored-row compilers and sealed owner-policy refusal,
  original selectors, rewrite pins, numeric overflow and early trace stops.
- Lint with tests passes for all six affected packages, zero issues. Formatting
  and whitespace pass. No SQL/schema, quota or capability-maturity changes.

Diagnostic runs are retained separately. They caught decoder pointer/value
mistakes, noncanonical fixture headers, a POST cache bypass, conditional fixed
response expectations and older fixtures that expected header actions to
manufacture later selector matches. Those expectations now follow the runtime;
the response composition test also proves an original ingress header does match.
Review additionally distinguished owner compilation refusal from a dropped
retry action before the final passing verification. Passing counts exclude
diagnostic runs.

Profiles, source hashes, compressed logs and commit receipts are in
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-preview-agreement-20261001/`.
All six release guarantees remain unchecked. Complete binding publication and
tenant/alias/revision/operator reservation transitions, broader preview/full
synthetic-path agreement, live daemon feature observations and
load/recovery/customer/staging acceptance remain pending. No native Linux
x86_64 KVM acceptance host is available; native VM/firewall/process-death/leak
acceptance remains pending.


### Fresh gateway traffic wiring observations, 2026-10-01

Named internal gateways now publish credential-free wiring observations after
handler construction and serving-listener binding. A database sequence allocates
process generations with compare-and-swap registration against a baseline read
once. An ambiguous registration can recover its original process token; a late
old writer cannot reclaim or retire a replacement. Registration clears previous
freshness and feature values. Readiness failure and bounded shutdown retirement
clear the report; process death or failed retirement expires after ten seconds.
Writes run every two seconds under a 250 ms context, outside request accounting.

The owned app policy-status API adds a separate `traffic_runtime` block. It uses
active named compute gateways, database timestamps and a 4,096-member bound;
oversized rosters refuse rather than truncate. Missing/stale members leave
features unverified. Fresh reports expose the public retry gate, actual rate
and retry backend types, retry endpoint disagreement, deadline signer, public
snapshot reader, emergency revocation registry, and tenant managed HTTP/adaptive
breaker wiring. `gregale app <slug> traffic-status` renders the same data and
policy convergence; JSON and generated CLI documentation are updated. Older
API responses render unverified wiring. Optional API waits retain their policy
convergence semantics. The HTTP handler's reads and polling are extracted so
the handler remains below the repository's 50-line limit.

These are process wiring observations. They do not attest public-hop or target
reachability, successful counter operations, rate endpoint or signer-key
agreement, per-VM admission, outbound firewall enforcement, or request-path
acceptance. Existing policy-revision watermarks retain their separate writer
semantics; this change fences the new observations. Enforcement remains
unverified and capability maturity is unchanged.

Verification:

- Full API, gateway, internal gateway and CLI suites pass: 1,813 / 2,223 / 740 /
  2,906 named cases. The internal suite is repeated after the bounded-retirement
  context fix; existing database/shell guards account for 74 remaining skips.
- Four PostgreSQL state cases pass without skips: concurrent ownership,
  replacement/late-writer fencing, lost-response recovery, roster membership,
  credential-free validation and refusal of a truncated roster.
- Seven targeted internal gateway cases pass without skips, including real
  PostgreSQL, actual daemon handler/listener construction, a local gRPC usage
  receiver, central/local mode and flag differences, a real HTTP response,
  restart generation, shutdown retirement and bind-failure non-publication.
- Forty-four selected app runtime-policy/traffic/capability cases pass without
  skips, including app ownership, missing/stale/future observations, mode and
  endpoint disagreement, and database-read refusal. The CLI suite includes
  real SDK round trips, text/JSON output and older API compatibility.
- The combined final evidence has 7,733 distinct passing named cases. Shared
  API/state, gateway, internal gateway, CLI and apid lint all report zero issues
  with tests enabled. SQLC 1.31.1 regeneration matches all four generated files;
  source hashes, formatting and whitespace checks pass.

A fresh private PostgreSQL database applies the complete migration set. The
new table's seven canonical table/sequence/default/key/FK blocks are copied
from its actual pg_dump output. Full schema regeneration also exposes older
unrelated schema drift; that drift is preserved as a diagnostic and is outside
this slice. No existing schema block is rewritten.

Diagnostics retain the generated-column fixture correction, disk-full build
and migration failures, missing usage-receiver fixture, and the retirement
context lint finding. The usage compatibility gate is exercised with real
local gRPC. Retirement preserves parent values with context.WithoutCancel and
its own bounded deadline. Disk cleanup removes only older entries in this
chat's owned Go cache; test/lint scopes and assertions are retained.

Receipts, source hashes, compressed logs, schema and SQLC evidence are in
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-runtime-observations-20261001/`.
All six release guarantees remain unchecked. Binding publication and
tenant/alias/revision/operator reservation transitions, broader preview/full
synthetic-path agreement, feature observations for the public hop and VM
consumers, backend-operation evidence, load/recovery/customer/staging and
native VM/firewall/process-death/leak acceptance remain pending. No dedicated
Linux x86_64 KVM host is currently available.

## 2026-10-01 — policy acknowledgements bound to serving processes

The four durable gateway repair consumers now share the traffic runtime
process session. Its registration baseline is captured before consumers
start; listener binding precedes registration and serving reports. A replaced
process cannot rebase that session or publish progress for its successor.

An additive, bounded `gateway_traffic_policy_observations` table keeps separate
app/traffic, edge-rule, CORS-preset and response-cache-purge positions. SQLC
publishers lock the node and current epoch before writing. Positions are
monotonic within an epoch and reset independently in a new one. Status and
convergence waits join only matching generation/process tokens and require a
fresh serving report. Component freshness uses the database clock and rejects
future observations. The roster reader refuses truncation beyond the existing
4,096-node limit.

All four pruning paths require fresh current-process acknowledgements. Legacy,
missing, stale and retired serving members hold pruning at zero; prior legacy
watermarks cannot overwrite the new ledger. Existing latest app/account/purge
retention rules remain in force. Cache-purge bootstrap can resume a known
node's applied fenced position across restart, or a fresh serving peer's
position under the existing shared-cache assumption. The new process must
still replay before publishing its own acknowledgement.

The broader API gate exposed missing schemas for the preceding wiring-status
slice. The canonical OpenAPI specification and embedded copy now include
`traffic_runtime` and both status DTOs. The new response field is optional in
the SDK contract so older API-server responses remain readable. Pinned Node
and Python generators were rerun; Python's barrel template now preserves the
existing request-deadline helpers. Regeneration also reconciles previously
stale Python egress-flow models and abuse-reason values with the canonical
specification, and synchronizes the prior execution/total-deadline documentation.

Verification on Go 1.25.13, CGO disabled:

- 6,504 distinct passing named Go cases across state, internal gateway, API,
  embedded API and migrations. The repaired complete API/internal packages
  were rerun; the initial combined profile's API parity failure is retained
  as a diagnostic and is not accepted as a green package verdict.
- Focused PostgreSQL evidence covers generation replacement, a publisher
  blocked behind a replacement transaction, legacy writes, stale/future
  serving reports, durable cache bootstrap, all four pruners, real repair-loop
  mutations, delayed process registration and actual daemon listener/retirement.
- 1,562 guarded named cases remain without acceptance in these profiles;
  broader PostgreSQL, host and external integration coverage is not claimed.
- All five affected Go packages, including their test sources, pass
  golangci-lint 2.4.0 with zero issues.
- Node build and 51 unit cases pass. Python has 89 passing cases, one default
  regen-test deselection and two existing collection warnings. Typed current
  and older API responses round-trip; deadline exports survive regeneration.
- SQLC's four generated Go files reproduce. Node's 1,027 generated files and
  Python's 2,567 non-cache generated files reproduce. Canonical OpenAPI and
  the embedded copy match; pinned vacuum reports zero errors, with 1,542
  warnings and 106 informs remaining in the existing specification.

The new migration was applied in a private PostgreSQL database. Canonical
pg_dump blocks for the new table and the three existing repair-ledger tables
were added to the SQLC snapshot; unrelated pre-existing schema baseline drift
remains recorded. The test source database's public schema remains unmigrated.
Initial duplicate-import, missing-app fixture, API-parity and redundant-selector
lint diagnostics were fixed in source without overlays, exclusions or weaker
assertions. Only old entries in this chat's own Go cache were removed when disk
space became tight. Generator-only formatting in two unchanged Python test
files was restored to its original content before final SDK verification.

Receipts, source hashes, compressed logs, canonical schema and generator
evidence are in
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-policy-process-fencing-20261001/`.
All six release guarantees remain unchecked. Complete-path and broader policy
transition evidence, public-hop/VM wiring and backend-operation observations,
load/recovery/customer/staging and native VM/firewall/process-death/leak
acceptance remain pending. No dedicated Linux x86_64 KVM host is available.

## 2026-10-01 — shared custom-domain publication transitions

Creation, expired-claim reclamation and verification now use the same bounded
binding transaction as removal. It takes the global route lock and sorted
locks for overlapping claim owners, the destination account and global route
owners before its repeatable-read projection. Discovery and destination
ownership are repeated after locking. Each affected owner's policy is checked
against exact and most-specific wildcard claim selection across accounts,
including pending claims that reserve a name and block fallback.

Safe shadowing changes remain publishable. Verification of a wildcard hidden
by a foreign exact or narrower wildcard claim no longer treats that hidden
hostname as selected. A new serving domain/app/environment binding cannot
inherit an old owner's overload allowance. Plain verification retains its
original app identity; challenge verification also repeats the observed token
and current expiry predicate. A stale waiter cannot approve a reclaimed claim.

Native and quota creation, including environment and activity variants, commit
the claim and optional activity through this guard. Cancellation/refusal
preserves the previous claim and consumes no destination quota slot. The
in-memory store checks the same before/after projections under its mutex before
publishing a row or activity and now observes creation cancellation. Creation
analysis failures return the existing structured 422 codes with proven lower
bounds and no foreign hostname or exact policy counts. Refusal emits no creation
audit or notification.

Verification on Go 1.25.13 with CGO disabled:

- Complete state, internal gateway and API unit suites passed; selected private
  PostgreSQL state coverage passed all 461 named cases with no skips.
- 6,696 distinct named cases passed across the accepted profiles: 2,222 state,
  821 internal gateway and 3,653 API. Another 1,082 guarded named cases remain
  without acceptance in these profiles (1,059 state, seven gateway, 16 API).
- All three affected Go packages, including their test sources, passed
  golangci-lint 2.4.0 with zero issues.
- PostgreSQL cases check all creation forms, cross-account exact/narrow wildcard
  shadows, unchanged activity on activation refusal, repair, actual owner/global
  locks, same-app token and foreign-app replacement during waits, cancellation
  and reuse of a one-connection pool and the sole pending-domain quota slot.
- Two in-process HTTP peers exercise HTTP/1.1 and negotiated HTTP/2 through the
  actual PostgreSQL routing and policy reads. A pending exact claim blocks the
  previous wildcard owner, refused activation stays blocked, and repair reaches
  the new owner without notifications. Old dispatch is refused and admitted
  policy remains immutable. Forwarding and the scheduler use test seams; this
  is not real daemon, VM, fleet or staging acceptance.

The first HTTP diagnostic assumed a nonexistent problem-response scope field;
the corrected assertion checks the existing contract, lower-bound counts and
unchanged-claim detail. An older PostgreSQL race test assumed creation could
bypass the original account lock and observed an idle try-lock query. Its setup
now identifies the real worker's released lock set and models a prior holder's
legacy handoff; stale plain/challenge assertions and the verified replacement
comparison remain. The corrected complete state profile was rerun. No source
overlays, exclusions or weaker acceptance assertions were used.

Receipts, frozen source hashes and compressed logs are in
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-domain-publication-20261001/`.
No schema, SQLC query, SDK or limit changes were needed. The PostgreSQL source
database's public schema remains unmigrated. All six release guarantees remain
unchecked. Tenant/alias/revision/operator reservation transitions, broader
runtime/preview agreement and observations, load/recovery/customer/staging and
native VM/firewall/process-death/leak acceptance remain pending. No dedicated
Linux x86_64 KVM host is available.

## 2026-10-01 — serving tenant-host policy bounds

Owned analysis now includes verified hostnames on active tenant surfaces with
a public, non-deleted app in the same account and no suspended platform tenant.
It checks the potential tenant-enabled path even when the runtime flag is off.
Hostname row, surface, app and platform tenant identify each serving binding;
a newly verified/activated/linked binding has zero prior overload allowance.
Platform URL namespaces take precedence. Challenge tokens and tenant names do
not enter the bounded metadata transfer.

Direct hostname verification, surface activation, tenant reactivation and
surface linking use the account transaction's before/after bound. The memory
store checks proposed rows before publishing. Challenge verification repeats
the observed token and captured hostname/surface IDs after lock waits, so a
stale waiter cannot verify a replacement row, even with a reused token.
The DNS poller requires that seam and emits no verification audit for stale or
refused publication. Direct platform tenant status/link APIs return the existing
structured 422 policy problems. Repair leaves publication retryable.

Memory verification keeps hostname case aliases coherent. Public snapshot
tenant surface, hostname, binding and reservation reads now explicitly retain
citext comparison, correcting their former case-sensitive text operator.

Verification on Go 1.25.13 with CGO disabled:

- Complete state, internal gateway and API unit suites passed. Selected private
  PostgreSQL coverage passed all 482 named cases with no skips: 336 state,
  99 internal gateway and 47 API.
- 6,839 distinct named cases passed across the accepted profiles: 2,354 state,
  822 internal gateway and 3,663 API. Another 1,021 guarded named cases remain
  without acceptance in these profiles (998 state, seven gateway, 16 API).
- All three affected Go packages, including test sources, passed golangci-lint
  2.4.0 with zero issues. SQLC 1.31.1 reproduces all four generated Go files.
- The source database's public schema remains unmigrated. No native host,
  external daemon, fleet or staging acceptance is claimed.

Focused regressions cover direct writer refusal/repair, stale challenges,
cancellation, namespace and foreign-account isolation, case aliases, app
visibility, skinny metadata eligibility/privacy/bounds, observed lock waits,
claim replacement and single-connection reuse. Actual DNS passes check
stale/refused audit suppression and missing-seam refusal. Two in-process HTTP
peers use PostgreSQL routing/policy reads over HTTP/1.1 and negotiated HTTP/2,
including an uppercase stored claim, activation/repair, suspension and refused
reactivation without notifications. Their scheduler and forwarding are test
seams; this is not daemon, VM, fleet or staging acceptance.

The first API build diagnostic had passed a private poller row to a helper that
expected a state row; the helper now accepts the observed hostname/token.
The first serving HTTP fixture seeded a default deployment for a project-backed
app that dispatches in production. The corrected fixture retains admission and
HTTP assertions. A separate uppercase snapshot regression demonstrated the
case-sensitive SQL error before the citext fix. No source overlays, exclusions
or weaker acceptance assertions were used. Initial lint found two test callback
context-inheritance issues. The callback now uses its supplied context; all ten
affected named API cases and full lint passed again. Production and assertions
were unchanged after the full unit/PostgreSQL gates. Only this test callback and
the tracker finalization differ from the initial frozen sources.

Receipts, frozen source hashes and compressed logs are in
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-tenant-serving-bounds-20261001/`.
No schema, SDK or limit changes are needed. All six release guarantees remain
unchecked. Bulk tenant apply/reconciliation/offboarding and cross-account
tenant shadow/removal/reservation writers remain pending, along with complete
alias/revision/operator transitions, runtime/preview agreement, observations,
load/recovery/customer/staging and native VM/firewall/process-death/leak
acceptance. No dedicated Linux x86_64 KVM host is available.

### 2026-10-01 — tenant writers across binding owners

Tenant hostname creation, verification and removal, surface status changes and
linking, tenant reactivation, delegated hostname registration, bulk onboarding,
reconciliation and offboarding now share bounded binding coordination. Native
transactions discover overlapping domain and tenant owners plus global-route
owners, acquire the global routing lock and sorted account locks before their
repeatable-read snapshot, then repeat discovery. They wait without retaining a
pool connection. Domain publication/removal includes overlapping tenant owners
in the same lock set.

Both tenant-enabled and tenant-disabled routing are checked. Active verified
public tenant bindings precede exact/wildcard domains; suspended tenants block
fallback while retaining their claims. Pending/inactive/unverified claims fall
through to domains but still reserve global routing, including hostname rows on
soft-deleted surfaces. Platform namespaces retain precedence. Newly exposed
binding identities receive no legacy allowance.

Bulk apply and reconciliation validate the final topology before publishing
links, credentials, webhooks or receipts. Dry runs and plans preserve empty
creation IDs and stable plan hashes. The memory store stages IDs and proposed
links/claims before publishing maps or side effects. Offboarding checks its
cleanup proposal before changing credentials or delegation policies. Immediate
tenant suspension remains independently available when cleanup would expose an
unsafe fallback. The HTTP surface deletion cascade now removes the authorized
surface and its hostname reservations atomically. Hostname removal repeats the
authorized surface and original row identity after lock waits.

Tenant API problems retain the existing HTTP 422 codes, limit/observed and docs
fields. A refusal caused by another owner's policy reports only a proven lower
bound, without the foreign hostname, scope or exact count. Missing owner-bound
HTTP removal seams refuse without changing intent or emitting an audit event.

Local evidence:

- Full affected state, internal gateway and API unit suites passed 6,485 named
  cases: 2,077 state, 740 internal gateway and 3,668 API.
- Selected private PostgreSQL coverage passed all 527 named cases with no skips:
  371 state, 104 internal gateway and 52 API.
- Across accepted profiles, 6,892 distinct named cases passed: 2,401 state,
  823 internal gateway and 3,668 API. Another 1,010 guarded named cases remain
  without acceptance: 987 state, seven gateway and 16 API.
- All three affected packages, including tests, passed golangci-lint 2.4.0 with
  zero issues. SQLC 1.31.1 reproduces all four generated Go files.
- Frozen gate sources cover 12,456 files. The source database public schema
  remains unmigrated; fsync, synchronous commit and full-page writes are on.

Regressions cover foreign wildcard fallback, pending/deleted reservations,
refusal/repair and cancellation, bulk linking, plan/apply agreement, preserved
credentials/delegation/webhooks/receipts, immediate suspension, complete final
reconciliation topology, case-insensitive claims, observed lock waits, stale
removal authorization and single-connection recovery. Two PostgreSQL-backed
in-process HTTP peers check tenant-to-domain fallback over HTTP/1.1 and
negotiated HTTP/2 without notifications, immutable admitted policy and refused
stale dispatch. Their scheduler and forwarding remain test seams.

Diagnostics are preserved. Fixtures now use valid canonical bulk requests and
intent enumeration to inspect reservations on deleted surfaces. API assertions
use the existing RFC problem fields. Local disk exhaustion interrupted one run;
only old entries from this task's Go cache were cleared before unchanged checks
were rerun. A later interrupted PostgreSQL run was confirmed stopped by both
its missing handle and absent process; its partial log is retained and excluded
from acceptance. The safe final-topology regression found and fixed a memory
ownership check so an owned unlinked managed surface can be adopted and cleaned
in the same operation. No assertions were weakened, sources excluded or overlays
used.

Final lint identified an unused private domain reader after callers moved to
the combined claim reader. Only that dead helper and its unused import were
removed after the full gates; the previous gate file is exactly reconstructible
from the removal receipt. The post-removal tenant transition checks and full
lint passed. SQL, tests and assertions were unchanged by this removal. Remaining
post-freeze edits finalize documentation and remaining-scope wording.

Receipts, frozen hashes and compressed logs are in
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-tenant-transitions-20261001/`.
No schema, SDK or limit values changed. All six release guarantees remain
unchecked. App visibility/retirement/purge and broader alias/revision/operator
binding transitions remain to be audited and guarded across owners, together
with complete runtime/preview/synthetic/path agreement and observations, real
daemon fleet load/recovery, customer/staging acceptance and native
VM/restore/firewall/process-death/leak checks. No dedicated Linux x86_64 KVM
acceptance host is available.

### 2026-10-01 — app eligibility and retirement across binding owners

App visibility/status updates, CAS into or out of deleted status, direct restore,
rename, scheduled deletion (including activity), cascade deletion and physical
purge now use the shared cross-owner binding analysis. Both tenant-routing modes
are checked. Ordinary active/evicted restart CAS retains its existing lightweight
path because it does not change binding eligibility. Memory creation also checks
its app proposal through this analysis; bulk project/preview writers remain open.

Discovery includes the account's domain languages and tenant hostname rows,
including inactive reservations, then overlaps and enabled global route owners.
App purge additionally discovers foreign domain rows whose legacy redirect FK
points to the app. The native guard acquires the global session lock and sorted
account sessions before a repeatable-read snapshot, rereads discovery under
account row locks and retries stale discovery without holding a pool connection.
The existing bounded analyses and central limits apply. SQLC projects the redirect
identity privately; no migration, API/SDK schema or limit value changed.

Withdrawals of a verified public tenant binding can expose a foreign wildcard
domain. Refusal preserves the app, timestamps/grace deadline, reservations,
invocations, reserved async quota, command tasks, crons, deployment/build state,
cleanup handoffs and activity. Memory validates a proposed app/cascade before
publishing cleanup. Native mutation and activity share the guarded transaction.
Restore and rename retain the zero prior allowance for a newly serving binding.

Eligible invocation cancellation moved from the HTTP handler into scheduled and
cascade deletion. The SQLC query captures prior reservation ownership and releases
quota with cancellation in the same transaction. Memory preserves the same split.
Managed work already dispatching keeps its existing cancellation fence. A refused
HTTP deletion leaves invocations untouched and emits no audit or notification.
Accepted deletion is checked for cancellation and reserved-quota release.

Physical purge projects domain/tenant reservations and app-scoped routing children
before deleting any lifecycle intent. It includes a suspended tenant hostname
whose removal exposes a foreign domain, pending domains, deleted-surface hostname
reservations and a foreign legacy redirect claim. The memory cascade also clears
default-domain selections; later foreign reclamation cannot inherit the old
selection. The redirect owner app survives purge of its target.

A typed binding marker preserves internal error diagnosis while HTTP problems
report only `observed = limit + 1`, with no foreign witness, exact count or policy
scope. App update, deletion, restore and rename retain the existing 422 codes and
docs links. The modified delete/restore handlers are 42 lines and rename is 39.

All nine memory withdrawal forms reproduced the oversized foreign fallback before
the fix. Shared memory/Postgres cases now exercise refusal, canceled context,
complete intent preservation and policy repair. Additional cases cover rename
publication, physical purge, foreign redirect removal, inactive reservations,
default selection/reclamation and reserved invocation quota. HTTP aggregate,
projection and analysis refusals cover visibility, deletion, restore and rename,
plus repair and notification/audit privacy.

The final 12,461-file source freeze passed:

- Full state, internal gateway and API unit runs: 6,521 named passes and 1,362
  guarded/skipped results, 110.950 s; all three packages passed.
- Selected real private Postgres routing/lifecycle runs: 556 named passes, zero
  skips, 271.081 s; state 400, internal gateway 104, API 52.
- After duplicate results are removed: 6,955 distinct named passes (state 2,446,
  gateway 823, API 3,686), with 997 guarded results without acceptance evidence
  (state 974, gateway 7, API 16).
- SQLC 1.31.1 reproduced all four generated files. GolangCI-Lint 2.4.0 checked
  all three packages with tests and reported zero issues in 75.321 s. No overlays,
  exclusions or weaker assertions were used for the accepted gates.
- Source Postgres public schema remained empty; fsync, synchronous commit and
  full-page writes stayed enabled. The local Postgres process is a macOS test
  environment and supplies no native VM/network acceptance.

Failed fixture builds, three empty-output Go failures and earlier gate runs are
retained separately. The first completed gates preceded the default-selection
repair; both full gates were rerun on the final freeze. Only stale files from this
task's own Go cache were removed during disk pressure. Accepted counts exclude all
failed attempts. Evidence is under
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-app-bindings-20261001/`.

All six release requirements above remain unchecked. Next software work includes
project reconciliation and preview retirement, account/operator writers and the
remaining alias/revision transitions, then complete runtime/preview/synthetic/path
agreement and observations. Real daemon fleet/load/recovery, customer capability
and staging qualification, and native Linux x86_64 KVM VM/firewall/restore/
process-death/leak acceptance remain pending. The user has reported no acceptance
host available; no new host request or unsupported local acceptance was substituted.

### 2026-10-01 — project reconciliation and preview app batches

Project reconciliation, PR preview set replacement and creation-only preview
batches now use the shared cross-owner app binding guard. The native transaction
validates its authoritative final app topology before committing existing cleanup,
crons, project metadata or preview receipts. Discovery includes account-owned
custom-domain languages and tenant hostname reservations, overlapping owners and
enabled global route owners; both tenant-routing modes use the existing bounds.
No schema, SQL query, generated SQLC output, API/SDK shape or limit value changed.

Memory batches stage complete app and cron maps plus project/preview metadata
under the existing mutex. Preview creation uses the shared app constructor and
quota counter against the proposed app map. One final binding analysis precedes
publication. Direct app creation keeps its existing constructor and publication
validation. Existing preview quota-neutral swaps, collision precedence and shared
member rules remain covered; preview resource cleanup still belongs to the janitor.

The memory baseline reproduced both gaps: project removal and preview replacement
accepted an oversized foreign wildcard fallback. Shared native and memory
regressions now reject those withdrawals, retain the private binding-error marker,
preserve app IDs, leases, tombstone slugs, crons, project metadata, invocations,
reserved quota, builds, cleanup and preview receipts, and permit retry after policy
repair. Cancellation also preserves that intent. A project batch that removes and
restores the same workload succeeds despite its unsafe intermediate deletion:
its final binding is unchanged. This batch covers routing publication and preserves
existing accepted cleanup behavior. Broader memory/native cleanup parity remains
open for project and preview resource-cleanup tables.

The final 12,463-file freeze passed:

- Full state, internal gateway, API, reconciliation and GitHub service unit runs:
  6,899 named passes, 1,370 guarded/skipped results,
  242.033 s; all five packages passed.
- Selected real Postgres traffic/domain/tenant/project/preview/reconciliation runs:
  620 named passes, zero skips, 388.176 s;
  state 456, internal gateway 106, API 52, reconciliation 6.
- After duplicate results are removed: 7,394 distinct named passes
  (state 2,506, internal gateway 823, API 3,686, reconciliation 77,
  GitHub service 302), with 951 guarded results without acceptance
  evidence (state 928, gateway 7, API 16).
- SQLC 1.31.1 reproduced all four generated files. GolangCI-Lint 2.4.0 checked
  all five packages with tests and reported zero issues in 152.315 s.
  Runbook SQL, text encoding, shell quoting and ADR numbering policy gates passed.
  No overlays, exclusions or weaker assertions were used for accepted gates.
- Source Postgres public schema remained empty; fsync, synchronous commit and
  full-page writes stayed enabled. macOS Postgres supplies no native VM/network,
  real daemon fleet, load or staging acceptance.

The initial fixture compile failure, disk-pressure termination, expected failing
baseline and focused runs are retained separately. Only older files from this
task's own Go cache were removed during shared disk pressure. Failed or terminated
runs contribute no accepted passes. Only the operations document and this tracker
changed after the final gate freeze. Evidence is under
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-app-batches-20261001/`.

All six release requirements above remain unchecked. Remaining software work
includes account/operator cleanup, captured ownership and alias/revision writer
and resolver coverage, then complete runtime/preview/synthetic/path agreement and
observations. Real daemon fleet/load/restart/outage/recovery, customer capability
and staging qualification, and Linux x86_64 KVM VM/firewall/restore/process-death/
leak acceptance remain pending. The user reports no KVM acceptance host available;
that availability remains recorded as pending acceptance.

### 2026-10-01 — account retirement and captured app ownership

Physical account retirement now uses coordinated before/after binding analysis
for its complete routing cascade. Private claim discovery includes app ownership
of foreign surfaces and account ownership of legacy redirect targets. These
identities remain within the existing metadata/input bounds and carry no action
bodies or credentials. The guard discovers overlapping custom-domain/tenant
owners and enabled global route owners and checks both tenant-routing modes.
No migration, schema, API/SDK shape or limit value changed. Four SQLC queries
were added; the bounded private binding-claim projection gained two owner fields.

Native deletion takes the shared binding transaction and validates pending
account status under the account lock. Legacy redirect-domain removal,
invocation deletion, reserved-slot release, child cleanup, the account sentinel
and deletion audit commit together. Invocation rows previously prevented an
otherwise accepted retirement through their app/cron/account foreign keys;
the new cleanup removes owned invocations and invocations referencing retiring
apps and refunds reserved slots in surviving account quota rows. Physical
retirement does not establish cancellation or rollback of external side effects.

Memory deletion projects owned apps, domain/tenant reservations, account rules
and presets, environments, platform-tenant links and policy cascades before its
first deletion. It publishes the accepted routing changes, clears invocation
reservations, then retains its existing lifecycle cleanup and audit sequence.
Builder cleanup handoffs are removed with their deleted builds. This batch
verifies routing retirement and relevant queued cleanup; it does not claim full
memory/native parity for every resource table or native VM cleanup acceptance.

Captured app guards repeat the expected owner predicate under an app row lock
before mutation. A stale owner refuses without intent changes; the row lock
blocks a concurrent ownership rewrite until the guard ends. Node reassignment
changes placement and does not transfer customer account ownership. Unsupported
direct SQL remains outside coordinated publication.

Shared regressions cover tenant, verified-domain, reserved-global and foreign
redirect withdrawal plus a foreign surface whose app FK points into the retiring
account. Refusal preserves account/app/hostname intent, secrets, API keys,
invocations, quota, cleanup and audit. Cancellation preserves the same intent.
Recovery after refusal remains available; a restored active account cannot be
retired by a stale grace sweep. After policy repair, deletion removes the account
and its routing reservations while preserving surviving foreign accounts/apps.
Native regressions also verify the captured owner and app row lock.

The final 12,466-file freeze passed:

- Full state, internal gateway, API, reconciliation, GitHub service and grace
  unit runs: 6,928 named passes and 1,376 guarded/skipped
  results in 214.123 s; all six packages passed.
- Selected real Postgres traffic/domain/tenant/project/preview/account and
  reconciliation runs: 640 named passes, zero skips, in
  392.094 s; apid 52, gatewayd-internal 106, reconcile 6, state 476.
- Removing duplicate results gives 7,442 distinct named
  passes (apid 3,686, gatewayd-internal 823, githubd 302, grace 22, reconcile 77, state 2,532), with
  938 guarded results without acceptance evidence
  (apid 16, gatewayd-internal 7, reconcile 0, state 915).
- The focused final state run passed 43 named cases without skips. SQLC 1.31.1
  reproduced all four generated files. GolangCI-Lint 2.4.0 checked all six
  packages with tests and reported zero issues in 115.953 s.
  Runbook SQL, text encoding, shell quoting and ADR numbering checks passed.
  Accepted gates used the complete package source with no overlays, exclusions
  or weakened assertions.
- Source Postgres public schema remained empty; fsync, synchronous commit and
  full-page writes stayed enabled. These macOS Postgres runs do not provide
  native VM/network, real daemon fleet/load or staging acceptance.

Initial digest fixtures could not encode compound-key maps; the private digest
encoding was corrected. The valid tenant/domain/redirect memory baseline
reproduced unsafe acceptance. The initial global baseline used an empty app ID;
final native/memory global cases retain a valid app-scoped route row, matching
Native NOT NULL and UUID constraints, with the original refusal assertions.
Native cleanup failures exposed the invocation FK gap described above. Empty
UUID/NULL global fixtures and an unused-import compilation failure were also
corrected; failed runs contribute no accepted passes.

The first broad Postgres run exhausted shared disk space, then fixture setup
failed with SQLSTATE 53100, recovery mode and an EOF. That entire run is excluded.
After its terminal result, only three older large files in this task's Go cache
and 77 idle generated templates in its verified private Postgres instance were
removed, with receipts. The identical source, scope and flags passed on retry.
No sibling cache or process was changed. Only the operations document and this
tracker changed after the gate freeze. Evidence and diagnostics are under
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-account-bindings-20261001/`.

All six release requirements above remain unchecked. Next software coverage
includes legacy alias shadowing and fallback, the remaining alias/revision and
operator writers/resolvers, then complete runtime/preview/synthetic/path
agreement and observations. Real daemon fleet/load/restart/outage/recovery,
customer capability and staging qualification, and Linux x86_64 KVM VM/firewall/
restore/process-death/leak acceptance remain pending. The user reports no KVM
acceptance host available; that answer remains recorded and no host request is
repeated.


### 2026-10-01 — alias reservations and safe primary fallback

A stored alias now reserves its configured apps-domain hostname even when its
owner or target cannot serve. Failed, cancelled or cleared deployments and
deleted or internal owners return a routing miss instead of falling through to
another app's legacy primary slug. Terminal pipeline outcomes remain writable.
Genuinely absent aliases retain primary-slug fallback.

Alias removal, physical app purge and account retirement discover raw alias
reservations and potential `tag-` primary identities within the existing bounds.
Before/after owner analysis applies alias precedence; a newly exposed primary
receives no legacy overload allowance. Native alias setters and removers use the
shared binding transaction and repeat captured app ownership under the app row
lock. Memory mutations validate proposed mappings before publication. No schema,
migration, customer API/SDK shape or central limit value changed. SQLC gained a
reservation query and the private binding projection gained alias/primary arrays.

Public PostgreSQL resolution reads raw reservation state in its host-policy
snapshot, fingerprints the result and refuses fallback if that read fails.
Existing dispatch verification rejects a previously resolved alias after its
mapping or serving eligibility changes. Safe fallback after actual removal and
transaction release are verified. The reservation-reader failure is injected;
this is not real fleet/store outage or recovery acceptance.

Shared memory/native regressions cover direct alias deletion, app purge and
account retirement exposing an oversized foreign header policy. The policy
owner is not an enabled global route owner, so discovery must find the hidden
primary owner directly. Refusal and cancellation preserve alias mapping and
owner/cleanup intent; policy repair allows removal and preserves the foreign app.
Runtime tests cover failed, cancelled, cleared, deleted-owner and internal-owner
states. Native metadata tests retain unavailable reservations, omit target/error
bodies and refuse oversized transfer through both scalar bounds. API tests check
stable 422 problems, foreign-evidence privacy, retained mapping, activity silence
and successful removal after repair.

The final 12,471-file source freeze passed:

- Full state, internal gateway, API, reconciliation, GitHub service and grace
  unit runs: 6,944 named passes, 1,386 guarded/skipped results, 202.007 s.
- Selected PostgreSQL-enabled traffic/domain/tenant/project/preview/alias/account
  and reconciliation profile: 660 named passes, zero skips, 341.738 s;
  state 482, internal gateway 120, API 52, reconciliation 6. This profile includes
  memory-backed cases alongside real PostgreSQL fixtures.
- Removing duplicates gives 7,469 distinct named passes: state 2,542, internal
  gateway 836, API 3,690, reconciliation 77, GitHub service 302, grace 22.
  There are 937 guarded results without acceptance evidence: state 914,
  internal gateway 7, API 16.
- SQLC 1.31.1 reproduced all four generated files. GolangCI-Lint 2.4.0 checked
  all six packages with tests and reported zero issues in 72.753 s.
  Runbook SQL, text encoding, shell quoting and ADR numbering checks passed.
  Accepted gates used complete package source without overlays, exclusions or
  weakened assertions.
- Source PostgreSQL public schema remained empty and durability settings stayed
  enabled. These macOS runs do not provide native VM/network, real daemon fleet,
  deployed load or staging acceptance.

The original memory baseline accepted unsafe alias deletion and account
retirement; both refusal assertions remain. An exploratory API build reused an
existing test-helper name; the new helper was renamed. The focused rerun passed
before the final freeze and is retained separately from accepted aggregate counts.
Shared disk pressure required removal of 13 older large files (11,917,501,534
bytes) from this task's own Go cache, retaining the four newest large files.
The first broad unit gate started before the cleanup tool closed; only that own
gate was terminated and its entire run is excluded. The identical source, scope
and flags passed after cleanup completed. No sibling cache or process was changed.
Only the operations document and this tracker changed after the gate freeze.
Evidence is under
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-alias-bindings-20261001/`.

All six release requirements remain unchecked. Next software work includes
immutable revision URL aggregate projection, remaining operator and alias/revision
writer/resolver coverage, then complete runtime/preview/synthetic path agreement
and observations. Real daemon fleet/load/restart/outage/recovery, customer
capability and staging qualification, and Linux x86_64 KVM VM/firewall/restore/
process-death/leak acceptance remain pending. The user reports no acceptance host
available; no further host request is needed until availability changes.

### 2026-10-01 — immutable revision publication and captured deployment ownership

The aggregate projection now includes each canonical positive stored revision
URL on a public, non-deleted app and an eligible non-deleted deployment. Its
deployment suffix is independent from the primary/alias apps-domain setting.
Pending, building, imaging, snapshotting and live revisions participate;
superseded, failed, cancelled and deleted targets do not. A shared hostname
writer/parser preserves runtime grammar, including supported legacy slugs.
Revision zero does not gain a rank-based URL. Ordinary revision compilation
retains matching sibling rules and charges shared referenced presets once.

Deployment creation and creation with activity use the shared binding guard.
Memory creation stages the new row and any prior pending supersede before
publication; PostgreSQL keeps allocation, supersede and activity inside its
guarded transaction. A new revision hostname has no legacy overload allowance.
Positive status writes, mark-live and app restore, visibility and rename validate
the same projection. An unchanged eligible URL retains policy repair behavior.
Dark promotion uses the guard and keeps its explicit-zero-traffic pending fence.
Native mutations repeat captured app ownership and deployment membership under
account, app and deployment locks. Alias revival applies raw reservation
precedence so a hidden primary cannot grant a false before-publication allowance.

Creation refusals map to the existing stable 422 traffic-policy problems and
withhold foreign witnesses, scopes and exact counts. Refusal and cancellation
retain deployment, predecessor, cron, snapshot, activity and webhook intent;
repair permits retry. No schema, migration, customer API/SDK shape or central
limit value changed. SQLC adds bounded revision identities and captured
deployment ownership/membership reads.

Native runtime checks retain exact revision ingress and prevent primary or
synthetic fallback when a target or owner becomes unavailable. Stale resolution
cannot reach dispatch; lookups and refusals release their transactions. The
valid pre-change baseline accepted unsafe creation in all four memory/native
plain/activity cases. Original refusal assertions remain.

The first broad unit run exposed a real deadline decision-evidence race: an
ordinary response was correctly truncated, but transport completion preceded
observable context cancellation and recorded `edge_response`. Sealed ordinary
evidence now consults the budget wall clock. Detached streams keep their separate
lifetime cause. The original real-gRPC assertion remains, and ten focused
repetitions plus a deterministic expired-budget regression passed before the
final freeze.

The final 12,477-file source freeze passed:

- Full state, hostname, gateway, internal gateway, API, reconciliation, GitHub
  service and grace unit scope: 9,238 named passes, 1,411 guarded/skipped results,
  171.953 s.
- Selected four-package PostgreSQL-enabled traffic, deployment, ownership,
  domain/tenant/project/preview/alias/account, runtime and API profile: 857 named
  passes, one pre-existing skip, 437.544 s. State 632, internal gateway 127,
  API 92 and reconciliation 6. The skipped EXPLAIN access-path test was already
  disabled by ADR-091 / PR-D; it supplies no acceptance evidence. The profile
  includes memory-backed cases alongside real PostgreSQL fixtures.
- Removing duplicates gives 9,913 distinct named passes: state 2,723, hostname
  26, gateway 2,226, internal gateway 843, API 3,694, reconciliation 77, GitHub
  service 302 and grace 22. There are 851 guarded results without acceptance
  evidence: state 828, internal gateway 7 and API 16.
- SQLC 1.31.1 reproduced all four generated files. GolangCI-Lint 2.4.0 checked
  all eight packages with tests and reported zero issues in 147.813 s.
  Accepted Go/lint gates used complete package source without overlays,
  exclusions, changed limits or weakened assertions.
- The source PostgreSQL public schema remained empty, with fsync,
  full-page writes and synchronous commit enabled. These macOS fixtures do not
  establish native VM/network, real daemon fleet, deployed load or staging
  acceptance.

Initial activity fixtures lacked required activity fields; corrected fixtures
reproduced the unsafe creation baseline before production edits. An expanded
fixture used a nonexistent app-update slug field and was corrected to call
RenameApp. Dark promotion fixtures were corrected to satisfy their existing
zero-traffic fence. A post-change native focused run hit SQLSTATE 53100 while
cloning fixture databases; after shared disk space recovered, identical source,
scope and flags passed. These failed/setup runs and pre-freeze focused runs
are retained separately from accepted aggregate counts.

Shared disk/load pressure later drove existing memory fixtures past the unchanged
two-second analysis bound. Only this task's Go/test processes were terminated;
that entire gate is excluded. Owned-cache cleanups removed three older large
files (3,536,339,030 bytes), then six (5,387,562,890 bytes), retaining the four
newest each time. Each cleanup tool closed before subsequent Go/lint work began.
No sibling cache, process or PostgreSQL cluster was changed. An unchanged-source
unit retry hit the existing API Argon2id timing-ratio assertion; that failed run
is also retained and excluded. No auth code, timing limit or assertion changed.
The complete scope then passed against the same source freeze.

Only this tracker changed after the final gate freeze. Operations documentation
was included in that freeze. Evidence and exact staged/committed source receipts
are under
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-revision-bindings-20261001/`.

All six release requirements remain unchecked. Remaining work includes operator
and alias/revision writer/resolver coverage, complete runtime/preview/synthetic
path agreement and observations, real daemon fleet/load/restart/outage/recovery,
customer capability and staging qualification, and Linux x86_64 KVM
VM/firewall/restore/process-death/leak acceptance. No KVM host is currently
available; another host request is unnecessary until availability changes.

### 2026-10-01 — durable invocation version view and delivery ownership

Async enqueue, scheduler drain and single synthetic delivery now resolve app
identity and scoped release/revision eligibility from one committed view.
PostgreSQL reuses the short read-only repeatable-read transaction; memory holds
its existing mutex across the reads. Selection finishes before enqueue, wake or
forwarding. The app projection contains only identity, preview parent, status
and deletion metadata; it excludes environment, credentials, service policy
and guest artifacts.

A nonempty saved invocation account must still own the app. Deleted status or
deletion time refuses even an unpinned invocation. The scheduler now preserves
that account in the trusted single-dispatch HTTP body, and the synthetic decoder
retains it for the post-wake owner check. Older account-free envelopes keep their
existing compatibility. Independent pool-only adapters refuse project and
revision pins; a snapshot failure cannot publish a partial selection or fall
back to those reads. Accepted UUID spellings resolve to the stored deployment
identity and canonical release header. Memory retains its historical compact
deployment IDs when comparing the scheduler target.

The existing delivery recheck refuses a disabled/expired release or revision
before forwarding to a pre-woken instance. Release target checks retain account,
scope, live status and deletion predicates. Fresh requests observe a new graph;
a queued header never silently selects it after its saved graph expires.

Verification against the final 12,480-file source freeze:

- Complete state, internal gateway, scheduler and gateway unit scope: 6,879 named
  passes and 1,399 guarded/skipped results in 118.641 s. Package pass counts are
  state 2,156, internal gateway 756, scheduler 1,741 and gateway 2,226.
- Selected PostgreSQL profile: 29 named passes, no skips, 12.034 s. Six real
  PostgreSQL fixture roots account for 16 named results; the other 13 are memory
  cases in the same profile. Concurrent committed cutover/expiry and direct-pin
  disable preserve the captured view, while fresh delivery refuses withdrawn
  pins. Owner/deletion, compact/uppercase/URN/braced UUIDs, minimal private
  projection, callback refusal and connection release are checked.
- Direct adapter and actual synthetic HTTP endpoint cases cover saved ownership,
  pre-woken deployment mismatch, disabled pins, deleted apps and legacy envelopes.
  The scheduler HTTP client retains the account. Retirement between the async
  app lookup and snapshot selection publishes no invocation.
- Deduplicated acceptance: 6,895 named passes, with 1,393 guarded results without
  acceptance. Guards do not count as passes or native VM/network evidence.
- SQLC v1.31.1 reproduces all four generated files. Lint v2.4.0 checks all four
  packages with tests and reports zero issues in 48.356 s. Its first run flagged
  the legacy Observe DNS branch: the unchanged regression requires logging and
  returning nil, while production RefreshApp reports resolution failure. One
  documented nilerr directive preserves that contract; no assertion or runtime
  egress behavior changed. The four repository policy gates pass in 21.095 s.
- PostgreSQL's source database remains unmigrated in public, with fsync,
  synchronous_commit and full_page_writes enabled. No schema, quota, customer
  configuration field, test overlay, source exclusion or weakened assertion.

The baseline reproduced three unsafe unpinned accepts (foreign account, deleted
status and deletion time). Further diagnostics reproduced noncanonical pin
identities and a wrong-account synthetic HTTP forward caused by the discarded
account field. These failing runs are retained and excluded from accepted counts.
The first baseline exited without test output under observed shared disk pressure;
its stop attempt found it already terminal. A later focused compile recorded
no space left on device. After that owned run closed, cleanup removed three older
large files (2,702,373,228 bytes) from this task's exact Go cache, retaining its two
newest large files. No sibling cache, process or PostgreSQL cluster was changed.
Earlier passing source iterations and the first lint failure are retained as
diagnostics; only the final runs enter acceptance counts.

Only this tracker changed after the final gate freeze. Evidence and source/commit
receipts are under
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-invocation-view-20261001/`.

All six release requirements remain unchecked. Operator and alias/revision
writer/resolver coverage, full synthetic/trigger/public policy and admission
agreement, preview and runtime observations, real daemon fleet/load/restart/
outage/recovery, customer capability/staging qualification, and Linux x86_64 KVM
VM/firewall/restore/process-death/leak acceptance remain pending. No KVM host is
currently available; another host request is unnecessary until that changes.

## Synthetic target verification and security lifetime — 2026-10-01

Normal synthetic HTTP now checks every target claim, including unpinned
standalone invocations. Account ownership, app version, running instance,
node membership and scoped live deployment are read in one committed view.
PostgreSQL uses the existing read-only repeatable-read transaction; memory
holds its mutex. Snapshot or target failure publishes no version/owner and
cannot enter the shared placement cache. Cache publication follows the owner,
target and security checks. Scheduler-selected unpinned delivery remains
unchanged; project/revision headers retain their selected graph through wake.

The internal gateway wires its existing security registry into synthetic
dispatch before listener startup. Account/app scopes enroll before gateway-owned
wake; deployment scope enrolls before forwarding. Nested delivery joins the
outer lifetime and cannot release its registrations early. Revocation, missed
revoke/release pairs and store failure cancel the exchange while registrations
remain owned through forwarding cleanup. A late forwarding success or partial
gRPC response cannot publish an invocation result after cancellation. Trigger
batch production and decoding preserve the trigger's saved account in every
record; older trusted account-free envelopes remain supported.

Pre-woken delivery enrolls after the scheduler returns its target. Scheduler
wake fencing still requires complete-path work and acceptance. Debug mirror
replay keeps its separate validated mirror owner. Public edge rules and public
request rate accounting are not newly advertised for background work. Node
admission remains on the synthetic forwarding RPC; this batch adds no node or
VM lifecycle changes and does not establish full synthetic node-cap acceptance.

Verification against the final 12,482-file source freeze:

- Complete state, internal gateway, scheduler and gateway unit scope: 6,906 named
  passes and 1,401 guarded/skipped results in 121.650 s. Package passes are
  state 2,156, internal gateway 783, scheduler 1,741 and gateway 2,226.
- Selected PostgreSQL profile: 52 named passes, no skips, 11.832 s. Five real
  PostgreSQL fixture roots account for 25 named results; 27 memory/transport
  cases share that profile. Target membership/refusal and concurrent committed
  target mutation retain one snapshot, while fresh delivery sees the new state.
  Connection ownership ends with the snapshot.
- Two actual synthetic HTTP endpoints use independent pools/registries against
  one PostgreSQL fixture. With notifications absent, a missed suspension/release
  pair cancels both active exchanges and fresh released requests succeed. Closing
  one endpoint's pool cancels it and refuses new delivery while its peer serves.
  Forwarding is a fixture and both endpoints share a test process; this is not
  deployed fleet or KVM evidence.
- Direct and actual batch HTTP tests check before-wake refusal, saved account,
  target/cache refusal, account/app/deployment revocation, store outage, missed
  pairs, late-success rejection and registration ownership through cleanup.
  A real gRPC transport through the production forwarding proxy cancels and
  discards its partial result. Its vmmd server and security store are fixtures.
  The existing real daemon startup test verifies synthetic registry wiring.
- Deduplicated acceptance: 6,931 named passes, with 1,396 guarded results without
  acceptance. Guards do not count as native VM/network acceptance.
- SQLC v1.31.1 reproduces all four generated files. Lint v2.4.0 checks all four
  complete packages with tests and reports zero issues in 111.984 s. Repository
  SQL, encoding, quoting and ADR-number gates pass in 36.887 s. PostgreSQL's source
  database remains unmigrated in public, with fsync, synchronous_commit and
  full_page_writes enabled. No schema, plan quota, test overlay, source exclusion
  or change to an existing assertion.

The baseline reproduced five unsafe unpinned HTTP deliveries: missing instance,
wrong node/deployment, stopped instance and superseded deployment. One new batch
test initially expected an obsolete broker-error status; it now asserts the
existing retry/invoke_error contract and zero wakes/forwards. One new startup
assertion initially compared the registry against a copied dependency struct;
it now checks the originally empty adapter pointer was wired by startup. Entire
failed runs and preliminary source iterations remain diagnostics, excluded from
accepted counts. Only final frozen-source runs enter acceptance.

Only this tracker changed after the gate freeze. Evidence and source/commit
receipts are under
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-synthetic-lifetime-20261001/`.

All six release requirements remain unchecked. Remaining operator/alias/revision
writer/resolver coverage, scheduler wake lifetime, complete synthetic/trigger/
public control contracts and node admission evidence, decision observations,
preview/runtime agreement, deployed daemon fleet/load/restart/outage/recovery,
customer/staging qualification and native Linux x86_64 KVM VM/firewall/restore/
process-death/leak acceptance remain pending. The user has no KVM host available.

## Scheduler invocation wake and security handoff — 2026-10-01

Production schedd now verifies the existing security store before lifecycle
startup and runs its bounded registry repair worker. Normal durable invocation
delivery enrolls the verified account/app before waiting for wake. A pinned
version also enrolls its selected deployment before wake; an unpinned version
adds the verified returned deployment. The drain rechecks the exact admitted
generations after wake and before recording success. Cached account allows cannot
substitute for admission. Original scheduler context owns durable claim failure,
retry and completion writes; security cancellation does not strand a writable
claim until lease expiry. Existing finite attempt budgets remain in force.

Delivery can withdraw its wait even when it initiated a shared wake. The existing
coordinator owns the independent bounded leader, which other callers can still
join. Pinned wake receives the delivery context. Security registrations remain
owned until gateway result handling and forwarding cleanup return. A canceled
exchange cannot publish a late success. Guest effects already performed are not
rolled back. A missing gateway refuses secured delivery instead of completing it
through the legacy in-process seam. Debug mirror replay retains its separate
owner and legacy scheduler account gate.

The trusted single-dispatch body transfers the exact admitted baseline in
`security_snapshot`. The shared canonical v1 codec retains the public response
wire format and its existing 4 KiB/16-scope bounds. Trusted memory-store IDs are
normalized for transport without changing registry ownership keys. The receiving
gateway requires the exact verified owner/app/target scope set and checks the
sender's generations. It cannot replace an old baseline with a newer released
generation. Missing owner storage, missing registry or malformed metadata refuse
forwarding. Legacy absence remains compatible. Roll updated consumers before
producers, drain dispatch during the cutover and resume on matched versions;
older consumers ignore the optional field. No mixed-version capability negotiation
is claimed. Operations documentation was frozen with the code.

Verification against the final 12,488-file source freeze:

- Complete state, internal gateway, scheduler, gateway, trafficrevocation, schedd
  and public gateway unit scope: 7,167 named passes and 1,426 guarded/skipped
  results in 145.267 s. Package passes are 2,156, 795, 1,778, 2,226, 33, 86 and
  93 respectively. Guards are not native acceptance.
- Selected PostgreSQL profile: 33 named passes, no skips, 23.000 s. Eight real
  PostgreSQL fixture roots account for 21 named results; 12 memory/transport cases
  share the profile. Actual schedd startup refuses an unavailable/unmigrated store
  before lifecycle work. Its real drain, PostgreSQL store and HTTP producer emit
  the owner baseline and persist completion. VMM and receiving gateway are fixtures.
- Independent PostgreSQL sender/receiver registries and an actual synthetic HTTP
  endpoint refuse an old baseline after an unobserved suspension/release pair,
  accept a fresh baseline and release registrations/connections. Existing two-peer
  missed-release/outage, target snapshot and daemon startup/shutdown checks pass.
  These are local fixture processes, not deployed fleet or native VM/network proof.
- Scheduler fixtures cover initial warm/cold account/app/pinned-deployment refusal,
  canceled pinned wake, canceled waiters with independently finishing shared wake,
  account/app/deployment withdrawal and outage through forwarding cleanup,
  post-wake and pre-completion exact checks, missed pairs without notifications,
  late-success refusal and durable retry writes. Shared codec tests check canonical
  bounds, ambiguity, trusted identity aliases and immutable context snapshots.
- Deduplicated acceptance: 7,187 named passes, with 1,417 guarded results without
  acceptance. SQLC v1.31.1 reproduces all four generated files. Lint v2.4.0 checks
  all seven complete packages with tests and reports zero issues in 59.151 s.
  Repository SQL, encoding, quoting and ADR-number gates pass in 24.613 s.
  PostgreSQL's source public schema stays unmigrated with fsync,
  synchronous_commit and full_page_writes enabled. No schema, quota, test overlay,
  source exclusion or weakening of an existing assertion.

The original baseline reproduced five unsafe synthetic handoffs (stale generation,
foreign account/app/deployment and missing owner). An early compile found an unused
import after codec extraction. New pinned fixtures initially omitted the existing
revision-retention configuration; enabling it exercises security admission rather
than version refusal. A new PostgreSQL result check initially compared JSONB bytes
instead of their semantic object. Lint required explicit inherited context returns
through admission and handoff. Entire failed and preliminary source iterations are
retained as diagnostics and excluded from accepted counts.

Shared disk pressure caused a full gate to fail compilation/linking with no space
left on device. Only this task's Go/test processes were stopped; the entire run is
excluded. After owned runs closed, two cleanups removed four older large cache
files (3,899,251,826 bytes), then three (3,563,524,842 bytes), retaining the two
newest large files each time. No sibling cache, process or PostgreSQL cluster was
changed. Final gates ran against the final unchanged source freeze. Only this
tracker changed afterward. Evidence and exact staged/committed source receipts
are under
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-scheduler-security-20261001/`.

All six release requirements remain unchecked. Remaining operator/alias/revision
writer/resolver coverage, complete synthetic/trigger/public control contracts and
node admission evidence, decision observations and preview/runtime agreement,
deployed daemon fleet/load/restart/outage/recovery, customer/staging qualification,
and Linux x86_64 KVM VM/firewall/restore/process-death/leak acceptance remain pending.
The user has no available KVM host; another host request is unnecessary until
availability changes.

## Verified synthetic ingress authentication — 2026-10-01

The three synthetic HTTP routes now verify a fresh app ingress mode before wake
or dispatch, including pre-woken single invocations and empty batches. Production
attaches its actual startup store before serving. The SQLC query reads only
`public_auth_mode` for an existing, non-deleted app; it loads no environment,
credentials or manifest body. The shared gate applies the existing 250 ms policy
read deadline inside the inbound context, checks cancellation before and after
the read, and rejects an adapter's late success after expiry.

Read errors, missing/deleted apps, expired reads and empty/unknown modes return
503 `traffic_policy_unavailable` with `Retry-After: 1`. Raw store errors are never
returned. These refusals cause no wake or dispatch. Each later attempt rereads
current policy without caching either a refusal or an allow. `internal_only`
retains its existing internal-service token gate; other declared modes retain
the established trusted background-delivery scope. Workflow authorization still
runs first. Legacy in-process string callbacks remain available, with empty and
unknown results now refusing. Nil lookup compatibility is limited to fixtures;
production supplies the verified read. An ordinary mode change is observed by
the next envelope. Emergency withdrawal retains the separate security registry
lifetime; per-record batch policy changes and public control equivalence are not
established by this initial ingress check.

Verification against the final 12,494-file source freeze:

- Complete state, internal gateway, scheduler, gateway, trafficrevocation, schedd
  and public gateway unit scope: 7,223 named passes and 1,428 guarded/skipped
  results in 151.042 s. Package passes are 2,159, 795, 1,778, 2,279, 33, 86 and
  93 respectively. Guarded results are not native acceptance.
- Selected PostgreSQL profile: 82 named passes, no skips, 33.758 s. Eleven real
  PostgreSQL fixture roots account for 26 named results; the remaining 56 are
  memory/transport checks. The new state fixtures verify fresh updates, missing
  and independently deleted app forms, cancellation, a locked-query timeout,
  recovery and connection release. Existing target/security/handoff and scheduler
  lifecycle regressions also pass.
- The actual daemon startup fixture serves all three synthetic routes under both
  local and central counter modes. Its real Postgres mode reader observes changes,
  refuses missing apps and table-lock timeouts before dispatch, retains the token
  requirement after recovery, and accepts a fresh open-mode request. Guest
  execution is a counting dispatcher and token verification is a fixture. This
  proves startup ingress wiring and refusal/recovery, not guest execution or
  deployed fleet acceptance.
- Gateway route fixtures cover empty/unknown modes, read failure and redaction,
  fresh recovery and mode changes, all five declared modes, missing/invalid/valid
  internal tokens, unwired verifier, inbound context propagation, parent/read
  deadlines, late-success refusal, pre-woken claims, empty batches and workflow
  authorization order. Existing nil-lookup synthetic fixtures retain compatibility.
- Deduplicated acceptance is 7,248 named passes, with 1,416 guarded results without
  acceptance. SQLC v1.31.1 reproduces all four generated files. Lint v2.4.0 checks
  all seven complete packages with tests and reports zero issues in 104.571 s.
  Repository SQL, encoding, quoting and ADR-number gates pass in 53.989 s.
  Postgres's source public schema remains unmigrated, with fsync,
  synchronous_commit and full_page_writes enabled. No schema, quota, test overlay,
  source exclusion or weakening of an existing assertion.

The baseline reproduced six unsafe accepts: empty and unknown modes reached
wake/single/batch dispatch. One focused build exhausted disk, and another found
an incomplete new fixture verifier interface. The full preliminary unit run hit
existing two-second analyzer bounds while repository gates ran concurrently and
disk space dropped; only that run's Go/compiler processes were stopped. These
whole failed/preliminary runs, plus the passing focused iteration, are retained
as diagnostics and excluded from accepted counts. Final unit and PostgreSQL runs
then ran serially against unchanged source. SQLC and repository gates had already
passed against that same source. Only this tracker changed after the freeze.

Three cleanups occurred after owned handles became terminal. They removed only
older entries from this task's cache: 15 files (3,393,101,526 bytes), 99 files
(6,492,166,434 bytes), then 760 files (3,331,013,996 bytes). Recent artifacts were
retained. No sibling cache, process or Postgres cluster changed. Exact source,
staged/committed content and gate receipts are under
`/Users/poyrazk/dev/Cloud/gregale/outputs/traffic-synthetic-ingress-auth-20261001/`.

All six release requirements remain unchecked. Remaining operator/alias/revision
writer/resolver coverage, complete synthetic/trigger/public policy, rate, deadline
and node-admission contracts, decision observations and preview/runtime agreement,
deployed daemon fleet/load/restart/outage/recovery, customer/staging qualification,
and native Linux x86_64 KVM VM/firewall/restore/process-death/leak acceptance remain
pending. The user has no available KVM host; acceptance remains pending until
availability changes.
