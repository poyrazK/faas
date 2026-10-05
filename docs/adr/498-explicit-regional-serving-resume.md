# ADR-498: Explicit regional serving resume

Date: 2026-10-05
Status: Host and transport implemented; seat issuance integration and activation pending.

## Context

Paused warm guests have a physical permit but no serving authorization. The
existing resume RPC carries only an instance ID, while the resident retains
its original placement. A resumed guest must use an exact issued serving seat
without changing its physical attempt or reopening an old seat fence.

## Decision

Add canonical bounded placement metadata to the warm-resume request and echo
the exact placement in the response. The regional client requires this explicit
acknowledgement; an older server's instance-only reply is insufficient. The
server and router refuse a missing regional-resume capability instead of
falling back to independent resume. Independent requests preserve their surface.

The host accepts the explicit placement only for an existing resident with
the exact full physical permit, tenant, deployment and actual lease plan.
Require the current cell authorizer and issued receipt check before native work.
Register its serving seat in the host journal under an exclusive resume barrier:
no physical or serving operation may overlap registration/resume, and any
previous registered seat must already be irreversibly fenced and drained of
host operations. This barrier is not native pause or stop proof.

Guard the resident placement against concurrent readers. Pin the new seat
before the native resume call and mark serving pending. Paused, pending or uncertain
resumes do not expose the resident through NetnsFor or ordinary runtime
admission. Require an explicit native serving-resume capability. A paused
snapshot restore must complete its deferred guest entropy/clock resume hook,
including the application after-restore hook, before probing readiness. Use
the resident's retained HTTP or gRPC startup configuration and budget. Other
resident resumes still probe readiness without replaying a completed restore
hook. Plain native unpause is insufficient serving acknowledgement. A failed
deferred completion remains pending for an exact-seat retry; a known completed
native phase is remembered even if a later authority check rejects publication.
Recheck cell authority and host fences after native success before
clearing pending state and restarting monitors. On error keep the new seat and
physical hold; exact-seat retries can recover, but no retry restores an old
fenced seat. The placement-less public resume refuses regional residents.
Independent warm restores use the same deferred completion capability on their
first resume. Ordinary independent pause/snapshot recovery retains its plain
unpause primitive. Successful independent and explicit regional resumes clear
the host's paused bookkeeping before readiness monitoring restarts.

Scheduler warm promotion must still durably identify each serving authorization,
issue its fresh seat, transport it, and recover an uncertain dispatch using
the same seat. Migration rollback must use the same protocol. Native pause,
resource/PID identity, startup inventory and teardown/recovery verifiers must
precede operational signed stop emission and seat reuse. Issued receipts,
operation barriers, native-call return, mock cleanup and an empty live map do
not establish stop proof. Request-forwarding admission also remains required
before runtime quota activation.

All accepted quota heads remain frozen. The full multi-region goal retains
task-attempt fencing, signed compute admission, explicit quota consent,
canonical global billing, deployment provisioning/orchestration, and ingress
to all selected healthy eligible regions without a global-directory request
dependency. Dedicated native Linux x86_64 KVM test-metal, leakcheck and recovery
acceptance remain required; shared nested GCE unit checks do not qualify.

## Validation

Exercise fresh warm seats, immutable physical identity, old-seat fences,
exclusive barriers, physical-only operations, exact-seat retries, uncertain
native errors, and post-native cell/seat/physical fences. Check pending serving
and native resume/hook/readiness ordering, NACK, cancellation, probe failure,
retained gRPC configuration and completed-hook retry behavior. Exercise
concurrent placement reads and network lookups. Verify protobuf generation,
strict acknowledgement and no legacy fallback across RPC/client boundaries.
Run affected race suites, diff lint and repository policy gates.
