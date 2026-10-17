# ADR-20261017201015: Cooperative running-job suspend/resume rehearsal

Status: Accepted, rollout disabled pending native qualification

## Context

ADR-20261017201014 pauses before a trigger. ADR-20261017201012 already locates a running managed
job at an application-chosen HTTP simulation checkpoint, held by its original
host relay. Keeping that response held permits a bounded in-place pause without
manufacturing a new invocation or attempt.

## Decision

`suspend_resume.checkpoint: managed_job` selects exactly one async HTTP worker
and names `worker_checkpoint`, a POST simulation fixture with one HTTP 200
response and no fault or delay. Exactly one job may exist in the run. Its first
dispatch must reach this checkpoint on the original, sole running worker VM.
The completed broker obligation arms a waiting receipt. A still-retiring trigger
command may settle while the original response stays held; no new command may
enter. Only the original relay
holding that exact response can make it eligible for schedd.

The receipt pins the invocation, attempt number, checkpoint simulation attempt,
deployment, instance and original recorder. Schedd and vmmd use ADR-20261017201014's
single-claim, destruction-joined pause/resume and configuration fencing. A new
activity check permits only the enrolled dispatch to remain in flight. Ordinary
Freeze retains its zero-in-flight guard. The recorder retains the checkpoint
connection and joins its other connections; all prior recorded effects must be
settled. New work is refused while the receipt is pending.

The guest receives its original simulated response only after observed physical
resume, readiness, and scheduler publication of the same VM. The original relay
must still hold the response. A restart cannot reconstruct this acknowledgement.
Lease renewal continues through the existing dispatch owner. A retained pending
checkpoint blocks new dispatch attempts, including direct claims after lease
loss, until a complete resume; uncertain failed checkpoints stay fenced. No timeout is
extended for the application and no retry is injected or excused. Real retries
remain in the invocation journal and comparison. Loss, early disconnect, changed
attempt, concurrent work, missing checkpoint or uncertain ownership cannot pass.

The final sealed evidence binds the checkpoint to its independent completed
simulation and the first journal attempt. Database, object and external-effect
collectors keep their own completion boundaries. A checkpoint's name is not
proof of a committed SQL transaction; the fixture database continues running
during the pause. Resume timing and notification multiplicity are compared.

## Boundaries and validation

This extends warm contained rehearsals, using the existing suspend/resume
admission switch plus `FAAS_REHEARSAL_RUNNING_JOB_SUSPEND=1`, off by default. It supports one cooperative checkpoint on one first-dispatch
managed HTTP job on the original VM and collector, including any later retries.
Replacement-worker retries require a separate ownership protocol and cannot pass
this evidence scope. Setup commands must finish first. Other workers, queued jobs,
replicas, arbitrary process interception, worker termination combinations,
persisted snapshots and dependency failure injection remain outside this scope.
The application must await the checkpoint and allow its bounded hold. Existing
dispatch deadlines and lease-loss rules remain authoritative.

Validation covers both stores, same-attempt continuity, actual retry and duplicate
effects, original relay and forward ownership, early disconnect, lost replies,
configuration changes, cancellation and destruction. Native x86 KVM lifecycle,
containment, credential delivery and leak acceptance remain rollout requirements.
