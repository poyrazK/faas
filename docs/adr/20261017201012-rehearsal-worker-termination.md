# ADR-20261017201012: Checkpointed worker termination and independently observed recovery

Status: Accepted, rollout disabled pending native qualification

## Context

ADR-20261017201011 records managed retries but deliberately treats unobserved worker loss
as incomplete. A retry test must also establish when the platform terminated the
worker and whether its original collectors settled before replacement execution.

## Decision

An optional `managed_work.worker_termination` names one async participant and one
of its HTTP simulation fixtures. The target must execute exactly one managed job.
Its first dispatch calls this cooperative checkpoint after the application's
chosen operation, for example a database commit. The host relay holds the
successful simulated response; the guest cannot receive it before termination.
Only that original, still-live relay/recorder owner may acknowledge the checkpoint.

The retained fault binds account, run, workload, deployment, job, first-dispatch
instance, simulation attempt and recorder session. Schedd claims it once, through
the normal bounded scheduler work pool and per-app lock. vmmd fences new effects,
joins relay handlers, destroys the VM through its normal physical cleanup owner,
and closes the original recorder only when every durable request has completed.
It then records termination. Schedd retires the instance row; ordinary managed
retry policy dispatches the existing job on a different VM in the same deployment.
The invocation journal and database/object/outbound collectors prove recovery and
effects independently. Checkpoint receipts are not evidence of database commits.

Dispatch is blocked while a fault is in progress or its old instance is still
live. Scheduler recovery may finish a terminated instance transition from its
durable receipt, but never reconstruct a missing recorder acknowledgement or
reissue an uncertain kill. Missing checkpoints, concurrent target jobs, early
checkpoint disconnects, unknown attempts, collector failures, incomplete cleanup,
and missing fresh-VM recovery remain inconclusive. Existing journal lease-expiry
and lost-owner rules remain unchanged. Admission closure still drains enrolled
jobs and retries before sealing collectors and data fixtures.

## Boundaries and validation

V1 supports one cooperative checkpoint/termination per run, on the first attempt
of a single managed async-route job, in the existing warm contained profile.
Checkpoint fixtures use POST, one successful response, no response fault or delay,
and no provider credentials. Applications must await the checkpoint call and
permit its bounded hold; arbitrary CPU instruction interception, process-only
signals, cold/resume profiles and dependency faults remain outside this increment.

`FAAS_REHEARSAL_WORKER_FAULTS=1` admits new fault plans only after preceding
rehearsal gates and native dedicated x86 KVM/network/secret/cleanup acceptance.
Portable coverage must exercise both stores, original collector continuity,
termination receipts, fresh retry, duplicate effects and failure/loss cases.
