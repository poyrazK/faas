# ADR-610: Process-local forwarding activity for private runtime upgrades

Status: accepted · 2026-10-06

## Context

ADR-609 requires a reviewed gateway roster. Membership, installed weights and
healthy candidate traffic cannot establish that a predecessor's requests have
finished. The daemon shutdown tracker is process-wide and stops counting new
entries after shutdown starts; it cannot establish per-deployment activity on a
live gateway. A WebSocket handshake also does not end its forwarding lifetime.

## Decision

Add a bounded process-local activity tracker at the shared internal gateway VM
forwarding factory. Construct one tracker before exposing any factory, using the
fresh process session UUID already generated for private routing confirmations.
The existing `FAAS_RUNTIME_UPGRADE_ROUTING_CONFIRMATION=1` enables it; the flag
remains default off and still requires the explicit reviewed slot configuration.
Without the flag the forwarding factory is unchanged. No public API, worker
admission, database migration, durable receipt or production launch is added.

Track canonical app/deployment UUID pairs at actual `ServeHTTP` entry rather
than target selection. Finish with an idempotent deferred completion after the
forwarder returns, including error and panic paths. Forwarding ordinary HTTP,
streaming bodies, synthetic cron/queue/async/delayed invocations, production
mirrors and raw HTTP upgrades all use this factory. The raw bridge returns only
after the hijacked connection and its body pump end. Pass the original response
writer through unchanged to preserve streaming, Flush, Hijack and accounting.
Request cancellation alone does not decrement activity before forwarding ends.
Both bridge implementations close their request-pump result channel and always
cancel/join it on return or panic, even after the normal outcome reader consumed
its result. This teardown guarantee also applies when private tracking is off.

Observations contain the process session, a monotonic activity version, coverage
known/unknown with a bounded diagnostic reason, this pair's active count and the
process total. Every start and finish changes the version, including untracked
activity. Thus a zero/busy/zero
cycle cannot reuse an earlier observation. Unrelated app activity conservatively
invalidates it too. Version exhaustion never wraps: it makes coverage unknown.
This is an activity version, not a routing revision or a database-clock lease.

Centralize budgets in `pkg/api/limits.go`: at most 4,096 active pairs and 65,536
tracked concurrent forwards per process. Remove idle keys immediately. Missing,
noncanonical or nil identities and either exhausted budget make coverage
permanently unknown for the current process session. Requests continue normally;
an unknown observation must never be interpreted as complete drain coverage.
Completions, available capacity or idle-key eviction cannot repair lost coverage.
Invalid observation reads return unknown without poisoning valid observations.
A nil or uninitialized tracker cannot claim coverage.

## Consequences

This supplies forwarding activity observations, not connection-drain receipts.
A known zero says only that this process's wrapped VM forwarding seam currently
has no active forwards for that app/deployment. Another request can enter next.
Legacy URL transports, raw TCP/UDP service bridges, direct vmmd callers and
guest-side connections are outside this observation's coverage. Cached responses
that never enter a VM bridge do not count as predecessor VM activity. Gateway
observations do not prove scheduler admission or VM-side quiescence.

Before retirement, follow-up work must prove actual ingress coverage, close
predecessor admission at the required owners, bind durable receipts to the exact
reviewed process roster and current routing revision, and recheck those fences
at publication and cleanup. In particular rollback followed by reactivation
must invalidate earlier receipts even if candidate/predecessor IDs are the same.
The activity version alone does not solve that database ordering problem.

Existing routing/health verification remains unchanged. Historical verification
does not acquire retirement authority. No deployment, instance, artifact, traffic
weight or cutover row is changed by activity observations. Public
`execution_available` remains false. No backend PR or public Apply is created.
Native Linux amd64 KVM acceptance, `test-metal` and final `leakcheck` remain gates
before enablement; local socket and gRPC fixtures are not native VM acceptance.

## Validation

Tracker contracts cover deployment separation, stale zero observations, returned
traffic, restart identity, canonical identities, both real capacity bounds,
sticky unknown coverage, idle churn, concurrent starts/duplicate completions and
version exhaustion. Factory contracts cover dispatch timing, streaming writer
preservation, cancellation before pump completion, panic/error completion and
disabled tracking. Streaming gRPC fixtures cross both HTTP hops before request
EOF and retain counts until the bridge returns. Panic fixtures hold the body pump
open through cancellation and require activity to remain counted until it exits.
Real TCP upgrade fixtures retain counts through 101 and binary bidirectional
traffic until disconnect. Detached mirror fixtures exercise
the HTTP-to-vmmd gRPC bridge after source cancellation. Internal daemon contracts
exercise both node-cache factories and synthetic invocation sources without
launching a daemon or changing customer traffic.
