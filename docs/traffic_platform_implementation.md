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
