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
