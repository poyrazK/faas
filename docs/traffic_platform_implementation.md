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
shell syntax check. Deployment evidence remains required. The node admission,
full policy snapshot and release
evidence deliverables remain outstanding.
