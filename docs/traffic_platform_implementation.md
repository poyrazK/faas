# Traffic platform implementation tracker

Objective: implement the six delivery steps in the 2026-09-29 gap-closure plan.
Base: `56618879c`; branch: `codex/traffic-platform-gaps`; decision: ADR-375.

## Requirements and acceptance

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
