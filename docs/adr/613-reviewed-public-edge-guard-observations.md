# ADR-613: Reviewed public-edge inventory and live guard observations

Status: accepted · 2026-10-07

## Context

ADR-612 binds one public forward to the receiving internal process. Internal
gateway membership cannot identify every public listener. Compute-node discovery
also cannot establish the public DNS/Caddy topology. A restarted public process
or changed proxy configuration must not borrow an old guard observation.

## Decision

Add a private PostgreSQL-only `PublicEdgeControls` administrative seam, with no
customer endpoint, CLI command, worker admission or public Apply. Review requires
the expected current public revision, exact current internal gateway roster,
a SHA-256 digest naming the external topology evidence, and every declared
public edge's stable slot, fresh process session and startup configuration digest.
Require canonical nonzero UUIDs, canonical lowercase digests, 1–64 unique slots
and processes, sorted copy-safe members. Review uses compare-and-swap; exact
retries preserve the revision. Changed reviews append immutable history and
atomically replace the head and clear operational facts. Missing edges never
disappear through heartbeat discovery or caller-selected observation subsets.

Add the default-off `FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_CONFIRMATION=1` flag to
gatewayd-public. It requires the already installed ADR-612 guard on BOTH HTTP
and raw-upgrade paths, PostgreSQL and a canonical stable
`FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_SLOT_ID`. Generate a new session at startup
and log only its public identity and nonsecret configuration digest for review.
Hash a versioned startup configuration: listener, node name, selected upstream
mode/target, H2C and canonical trusted-ingress CIDRs. Ignore unused fallback
settings when database discovery wins. Never hash or publish ingress secrets.
The database pool's changing endpoints are guarded on each actual connection;
this configuration digest does not freeze or enumerate them.

Start guard facts only when `http.Server.Serve` acquires the actual public
listener. Publish immediately and every existing 15-second repair interval,
using a 10-second cancellable database call. Stop on Shutdown and run/listener
exit before waiting for long-lived connections. Facts declare installed guard
and process liveness, independently of endpoint health or internal drain.
Unreviewed, restarted, changed-config or stale-internal-roster processes cannot
enroll themselves or publish. Failed writes retry without changing review.
Facts have an exact one-minute database-clock lease and true guard CHECK.

Review, publication and observation lock the internal roster head before the
public head. Review takes public exclusive access; publication/observation take
shared access. Read heads after waits under read-committed transactions. Keep
both fences until the transaction ends; no app, instance or VM locks are taken.
Publication obtains its database timestamp after lock waits. Database triggers
keep immutable, canonical review history and reject facts for a different current
slot/session/config/internal revision, including direct SQL writes.

Observation requires an explicit frozen public revision. Return pending on
changed public or internal membership. Read all declared members' current facts,
then the database clock, so blocked reads cannot extend liveness. Require every
exact process/config, enabled guard, nonfuture observation and at least one whole
second remaining. Cap validity by the earliest lease. A missing or expired edge
remains expected. `guards_observed` describes only this fresh private observation;
it is never persisted as a success journal or reused as retirement authority.

Register reviews/heads as platform configuration and facts as operational clone
schema data. Append a generated forward-only migration; regenerate the canonical
schema snapshot from a local migrated database and SQLC output. The new bound
belongs in `pkg/api/limits.go`; existing heartbeat budgets are reused.

## Consequences

This adds explicit reviewed public membership and invalidates old facts on either
roster transition. It does not certify that an administrator's declaration covers
the actual DNS/Caddy fleet: a topology digest names evidence, it does not validate
its contents. Native configuration collection and reconciliation remain required.
It does not fence previously admitted forwards, prove that removed/restarted
public processes have stopped serving, count raw TCP/UDP or private/synthetic
entrypoints, or close scheduler/VM admission. Public admission generation tracking,
withdrawal evidence, transition barriers and scheduler/VM quiescence remain
prerequisites for safe predecessor retirement. A cleanup writer must recheck
and fence all authorities at its own publication point.

Both flags remain disabled in deployment units. Public execution stays unavailable.
No PR, push, production daemon, deployment, customer traffic, forced disconnect,
retirement or artifact deletion is part of this change. Native Linux amd64 KVM
`test-metal` and final `leakcheck` remain pending before enablement. Local database
and listener fixtures establish control-plane contracts only.

## Validation

Real PostgreSQL contracts cover copy-safe/idempotent immutable review, canonical
bounds, concurrent CAS, exact process/config facts, complete expected membership,
future/expired/subsecond leases, direct SQL guards, public restarts, topology
changes and internal review invalidation/recovery. Blocked observation tests fence
both reviews and expire receipts using the later database clock. Blocked writes
fence review and timestamp facts after the wait. Existing roster, ingress and
drain tests and the migrated clone inventory remain dependencies. Public listener
contracts cover default-off behavior, installed HTTP/upgrade guard requirements,
fresh startup identity, selected configuration hashing, bounded immediate writes
after listening and cancellation during Shutdown.
