# ADR-394: Durable app admission inside vmmd boot and resume flights

Date: 2026-10-01

Status: Accepted; customer activation remains disabled

## Context

ADR-393 joins boots already inside the manager. A boot RPC still in transit can
arrive after Destroy returns and recreate a source writer. Checking only in the
RPC handler leaves another gap between that check and manager registration.
Warm-pool and migration resumes can also race destruction (spec §6 and §11).

## Decision

Install a read-only app admission callback in the Manager. Register the boot
flight before reading the app's durable PostgreSQL fence, and read before lease,
network, secrets, artifact prefetch or VMM boot. Never cache this result or read
from a replica. A configured callback fails closed on read errors and on a held
fence. The existing sqlc admission query supplies the check; vmmd writes no
instance rows or customer intent. schedd retains lifecycle ownership.

Register ResumeVM, WarmSnapshot and migration SnapshotKeepAlive in the same
per-instance flight registry before checking admission. Destroy and SignalAndKill
cancel and join these operations before looking up the live guest for teardown.
Concurrent resumable operations on one instance are rejected. A timeout retains
the flight and live resource identity for cleanup/retry. Snapshot error recovery
keeps a separate context that survives an expired RPC but is cancelled by the
same flight stop, preventing recovery from resuming after Destroy.
SignalAndKill stops the liveness/readiness monitors after the join as well.

The required ordering is: commit the durable app fence, then drain all relevant
instance IDs. Requests that read admission before the fence are already registered
and joinable; requests arriving later read the fence themselves. The flight covers
VMM implementations that return success late or ignore cancellation. No instance
ID tombstones or long-running database locks are needed for this ordering.

Production wires the callback from the control-plane PgStore before serving.
Open a configured DB URL on default-local nodes too, without requiring compute
node self-registration. Every fresh Manager uses a fresh durable read, so a held
fence remains effective after a daemon restart. The database remains authoritative
and completed cutover cancellation reopens admission. App-less disposable execution
and independent job tasks keep their existing isolation/boot path. On a configured
node, ordinary app boots/resumes without an app identity fail closed; legacy wire
requests cannot silently bypass the check by omitting that identity.

Boot/resume RPCs expose `database_cutover_fenced` as FailedPrecondition / HTTP 409.
Admission read failures expose `app_admission_unavailable` as Unavailable / HTTP
503 with a fixed safe error detail; database diagnostics never
cross that RPC boundary. The scheduler preserves the typed admission-fence
sentinel when lifting the wire error, including app-task restore failures.

## Limits and acceptance

Legacy DB-less nodes and unconfigured Manager embedders retain existing behavior
and cannot contribute cutover drain evidence. Before customer activation, the
controller must prove that every relevant node has the guard wired to the same
authoritative database and enforce ownership across restarts and node replacement.
Removing that configuration is not a supported way to release a fence.

The check applies while the durable fence is held. It does not attest the
deployment or credential generation carried by an RPC after admission reopens.
Activation must reject stale source envelopes and owners before releasing the
fence, as well as invalidate snapshots; this boolean check is not that protocol.

This change does not issue a drain receipt, serialize concurrent teardown, prove
successful destruction after a failed kill, or recover guests surviving a daemon
crash. Existing writers, delayed task command dispatch, route convergence and
external SQL sessions still require explicit draining. Fence cancellation reopens
admission; a resumed stale owner must remain fenced by the future drain protocol.
Atomic binding publication, snapshot invalidation and customer activation remain
unavailable. Draining does not copy source writes after the restore point.

Portable tests cover stale successful admission reads, rejection before resource
allocation, delayed boots after destroy/restart, other-app admission, cancellation
release, live resume/capture fences, late successful resumes and snapshot recovery
after cancellation. Native x86_64 Linux KVM `test-metal` and `leakcheck` remain
required by spec §14 and CLAUDE.md before declaring lifecycle acceptance complete.

Metal regressions boot real guests with an admission guard, reject live captures
and delayed boots while fenced, and confirm that reopening permits a cold boot.
A second test pauses a real guest and delays its resume past Destroy's deadline;
the manager retains its identity and a teardown retry removes all guest resources.

[Internal KVM diagnostic evidence](../ops/evidence/20261001-managed-postgres-vmmd-admission/README.md)
records passing regressions and leak checks, the repaired socket-path invocation,
and a concurrent boot failure reproduced on the pre-change baseline. The GCE
node uses nested virtualization, so supported native acceptance remains pending.
