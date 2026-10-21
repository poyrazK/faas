# ADR-20261018030612 · Recover Node resources after warm capture

- **Status:** implemented for qualification; native execution pending; production off
- **Date:** 2026-10-17
- **Decision:** Extend ADR-20261018030611's qualified Node path to recover the continuing
  source VM under its existing destruction-joined capture flight. Migration
  capture remains refused.
- **Why:** Draining PostgreSQL pools makes terminal snapshots safe, but a source
  VM that continues serving must rebuild and probe its pools before admitting
  another request.

## Capture and recovery

Only `metal,function_background_native` builds with the trusted function
execution receiver enable this path for Node 22/24. Normal production wiring
and standalone HTTP applications retain their existing behavior.

Warm capture closes ordinary execution admission and requires zero owned
executions before invoking the all-interpreter checkpoint hook. Active request,
stream, background or cleanup ownership refuses capture. Callers may retry
after real completion; capture never cancels customer work to make room.

The original guest stays paused through memory, VM-state and private-drive
publication. It does not resume before upload. This deliberately trades a
longer qualification pause for a single recovery boundary; a future early-resume
optimization must retain equivalent admission and recovery evidence.

The same instance flight resumes the original lease and runs one acknowledged
entropy/clock/userspace reseed and after-restore pass. Every registered pool in
every loaded interpreter is rebuilt and passes its read probe before the custom
after-restore callback and batch cleanup complete. Ordinary execution remains
closed through the final ACK. Only then can probes restart and the flight open
admission. The capture retains its historical paused bytes; recovery does not
modify the captured generation.

Failed publication and cancelled capture RPCs may already have drained some or
all pools. Their registered recovery context survives the caller's cancellation,
is bounded by the existing resume-hook deadline, and is cancelled and joined by
Destroy. They run the same recovery before returning the original capture
error. Failed captures return no snapshot evidence. The scheduler retains its
existing policy of retiring a failed warm-capture source even when recovery
succeeded.

The host makes one recovery callback attempt. A lost ACK, failed probe, failed
resume, expired recovery deadline or unfinished checkpoint cleanup seals
execution admission before releasing the flight. The original physical VM is
retired through vmmd's incarnation-checked teardown. Unconfirmed cleanup keeps
the allocator and pending-cleanup ownership; the existing watchdog can retry
retirement. No callback, application request or SQL statement is replayed to
make an uncertain capture succeed.

Post-admission failures retain a distinct internal error identity across both
warm RPCs. A nested busy or unavailable cause cannot masquerade as a healthy
pre-capture refusal after the source was retired. Conversely, a measured warm
capture refused before effects keeps active execution and its VM reservation.
schedd releases its RAM/concurrency and transitions to STOPPED only after source
retirement is confirmed; a failed Destroy retains that ownership for retry.

## Authority and scheduler

Fresh measured warm grants can explicitly carry `before_checkpoint=true`;
the flag remains part of grant equality, response checks and durable
acknowledgment. Qualified measured captures refuse an omitted flag rather than
infer permission. Ordinary warm consumers still reject a checkpoint plus early
resume. Migration grants continue to forbid checkpoint callbacks.

The legacy warm RPC carries no callback flag. The qualified host imposes its
mandatory checkpoint requirement using its owned Node instance identity. An
artifact missing the callback configuration fails before capture and must be
rebuilt. A VMM without single-attempt resource recovery support is refused before
drainage. Application input cannot activate qualified wiring.

schedd remains the sole instance-state writer. Qualified functions can use the
existing plan/config/readiness-gated warm capture, including configured
checkpoint hooks; profiling still requires terminal capture. Warm recovery
opens execution admission, so schedd freezes again before terminal park or
snapshot-reuse destruction. A new execution that beats this second barrier
keeps its VM, concurrency reservation and RAM. Ordinary warm behavior is
unchanged. This change adds no plan entitlement, schema, billing or rollout.

## Validation boundary

Portable tests cover the real generated adapter and HTTP runner, four loaded
interpreters, eight pools, held/failed restore probes, repeated TLS/SCRAM SQL
drain/rebuild cycles and unchanged mutation witnesses. Host tests cover explicit
measured authority, successful/failed/cancelled capture, held recovery admission,
lost ACK, recovery deadline, unconfirmed cleanup, Destroy joining recovery and
the post-warm idle race. Local UDS framing is distinct from real vsock evidence.

The strict native SQL target now requires Node 22 and 24, a continuing warm
source, zero SQL/proxy sessions before capture, eight fresh sessions before held
recovery ACK, terminal restore, thaw and actual worker/runner death. Compilation
does not qualify these cases. Native capture/restore and remaining lifetime
fault/restart/private-environment acceptance are still required before any
activation change. See the [acceptance procedure](../ops/function-backend-concurrency-acceptance.md)
and [evidence](../ops/evidence/20261017-function-warm-snapshot-recovery/REPORT.md).


Follow-up: [ADR-20261018030613](20261018030613-function-migration-source-rollback.md) adds a separate
qualification-only source prepare/rollback path. Ordinary and measured migration
capture and destination adoption retain their gates.
