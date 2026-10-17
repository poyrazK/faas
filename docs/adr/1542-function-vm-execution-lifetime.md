# ADR-1542 · Acknowledged function VM execution lifetime

- **Status:** accepted implementation; native qualification pending; production off
- **Date:** 2026-10-16
- **Decision:** Extend ADR-1541 with acknowledged, incarnation-scoped execution
  reservations owned by vmmd, a guest-init authenticated proxy and a VM-wide
  interpreter lifecycle barrier. Enable the wiring only in dedicated
  `metal,function_background_native` acceptance builds. Ordinary production
  builds have no runtime opt-in through environment, request or manifest.
- **Why:** Interpreter completion alone cannot prevent a VM being paused,
  snapshotted or destroyed through an idle snapshot-reuse shortcut after the
  HTTP response finishes. Terminal-only tail events do not establish ownership.
- **Ownership:** vmmd owns physical execution reservations and retirement;
  schedd remains the sole writer of instance lifecycle rows. This contract
  does not mutate `instances.tail_count` or emit legacy tail terminal events.

## Registration and completion

The Go Node runner acquires ownership before dispatching customer code. Its
fixed Unix socket is owned by root with the application group allowed to
connect. Guest-init verifies `SO_PEERCRED` against the main runner PID, stamps
its monotonically increasing runner generation, then proxies a bounded framed
RPC to host vsock port 1033. Workload interpreters cannot supply the runner PID,
owner generation, VM identity or plan. The host listener captures the full
allocator lease, including slot and process generation, before accepting a
connection. Reusing an instance ID cannot authorize old connections.

Frames are length-prefixed JSON, at most 1024 bytes, with a closed operation,
version and reply-status set. Each hop has a two-second deadline and bounded
concurrency. A successful local write or transport EOF is never an admission
acknowledgement. The runner retries an uncertain RPC twice with the same
reservation ID and requires the host's explicit reply and authoritative plan.
Acquisition failure returns 503 before handler dispatch and sends a completion
tombstone for any uncertain registration. Customer code is never replayed by
this protocol.

The host stores active sequence IDs and deadlines on the exact live Instance.
Duplicate registration neither increments activity nor extends its deadline;
duplicate completion never decrements twice. Completion also tombstones an
unseen sequence so a delayed registration cannot resurrect it. Begin calls are
serialized in the runner; finish calls can arrive out of order. Memory grows
only with active reservations, bounded by the existing interpreter footprint
and per-plan worker concurrency bound. A startup ledger covers health handlers
before live publication and moves into the published Instance under the same
lock. Cold fallback replaces it only after the old process is confirmed dead.

Ownership covers handler execution, response streaming and all negotiated
post-response promises. The runner sends completion only after its worker
release fence proves completion, acknowledged cancellation cleanup or actual
worker retirement. Request cancellation, HTTP EOF and stdout EOF do not release
ownership. A lost completion acknowledgement may retain occupancy and retire
the VM later; it does not authorize a retry of the function or its SQL.

Reservations feed the existing activity tracker used by Stats, quiet pause,
freeze and idle reaping. HTTP forwards and execution reservations overlap for
foreground work; the reported occupancy is their maximum to avoid counting
the same request twice. It is a conservative idle signal, not an exact sum of
two independently changing populations. Request throughput counters count only
forwards. Quiet time begins only when both populations reach zero.

## Pause, park and lifecycle hooks

Ordinary Begin obtains a short incarnation-pinned host flight. It registers
activity before checking quiet pause and receives its ACK only after any
quiet resume completes. The same flight prevents a replacement VM or a final
quiet pause from racing dispatch.

Capture owns the existing live-operation flight. Active or uncertain execution
ownership refuses capture before effects. The host admits only the configured
`before_checkpoint` callback within that flight. A final pause barrier atomically
closes callback admission and requires zero remaining reservations before the
Firecracker pause. Warm capture, terminal capture, migration capture, frozen
pause and quiet pause all observe execution occupancy.

In the acceptance build, schedd establishes an idle freeze before changing a
Node function's state or dropping its serving-concurrency reservation. This
also protects reuse of an older snapshot, RAM/config retirement and ephemeral
secret retirement. An ordinary function Begin cannot enter a frozen VM. For
real terminal capture, vmmd resumes the frozen VM only while its capture flight
keeps ordinary admission closed. This park skips an optional warm capture,
which would otherwise reopen admission before terminal park. A pre-capture
refusal thaws only a still-RUNNING instance with the same node and wake ID.
Independently, a busy warm-capture refusal preserves the live VM and scheduler
capacity rather than treating admission refusal as a failed snapshot.

The qualified runner reserves every loaded interpreter and sends each lifecycle
callback once. New worker acquisitions wait for the entire group to finish its
completion/cancellation fences. A `before_checkpoint` HTTP acknowledgement
also waits for the host's Finish acknowledgement. Restore callbacks run under
the host boot/thaw flight before public admission and need no separate lease.
Lifecycle handlers must await resource cleanup; `ctx.waitUntil()` is forbidden
inside them. Socket-derived lifecycle markers remain mandatory. A callback
failure follows the existing failed-checkpoint/failed-restore policy, including
retirement where prescribed by ADR-351. This is not proof that arbitrary
unregistered promises or unmanaged sockets have drained.

## Failure and recovery

The central limits table bounds a reservation by the foreground request
budget, plan post-response budget and two existing cancellation-grace intervals
(transport reconciliation and cleanup). A watchdog scans once per second.
Expiry seals admission; it never decrements occupancy by itself. A runner
generation change with active reservations likewise cannot adopt orphaned
interpreters. Guest-init sends owner retirement after observing parent exit;
unconfirmed transport retains the host's deadline bound.

The watchdog retires the original physical VM through the existing
incarnation-checked teardown reservation, with bounded operation and cleanup
contexts. Failed cleanup retains native recovery and allocator ownership.
Confirmed cleanup publishes a completed stop result while replacement boot
remains fenced; the synchronous schedd liveness callback can join that result
without deadlock. Only then is the stop gate removed. vmmd restart does not
reconstruct an empty ledger for an unproven VM: existing native recovery
quarantines unknown processes. No automatic function or SQL replay is added.

## Qualification boundary

Portable tests cover real generated Node adapters and HTTP/1 + HTTP/2 response
completion, acknowledged ownership, lost replies, duplicate/late messages,
stale VM and runner generations, startup, watchdog retirement, cancellation
fences, simultaneous freeze/admission and multi-worker callback broadcast.
A separate TLS/SCRAM PostgreSQL test holds a write behind an independent lock,
observes early HTTP output, requires busy park/freeze refusal, then observes one
committed row after explicit release. Its Firecracker boundary is replaced;
it is not native SQL evidence.

`make test-function-background-native` builds matching guest-init and Node
runner artifacts and propagates the native tag to all harness daemons. Its
strict deployed-function gate checks held work in three interpreters, capture
and park refusal, exact-once file witnesses, all-worker callbacks, frozen thaw
and terminal capture/restore. Missing host prerequisites fail rather than skip.
The [acceptance procedure](../ops/function-backend-concurrency-acceptance.md)
and [evidence](../ops/evidence/20261016-function-vm-lifetime/REPORT.md) record the
current result. This workspace lacks root/KVM/native loop devices.

Production activation still requires passing native lifecycle and leak gates,
native transport/runner/worker/host-failure injection, and real PostgreSQL pool
draining across all interpreters. Private environment-qualification restores
do not yet expose this port and are not qualified by this implementation.
Python background work, durable execution, billing changes and fleet
performance/telemetry qualification remain outside this decision. Durable
queues/workflows are required for work that must survive VM replacement.

**Rejected alternatives:** Releasing on timeout/EOF can snapshot executing
code. Trusting an application PID, plan or success flag bypasses the owner.
Checking activity without holding an admission flight leaves a pause race.
Running one callback misses other loaded interpreter pools. Destroying a VM
to reuse an old snapshot must not bypass the same idle barrier as capture.
