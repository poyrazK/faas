# ADR-1358: Contained in-place suspend/resume rehearsal

Status: Accepted, rollout disabled pending native qualification

## Context

Contained commands and declared managed workers have warm and cold-start
evidence. A pause can expose stale connections and delayed timers without
replacing the VM. Fixture credentials deliberately prohibit persistent VM
snapshots, so this protocol uses a resident pause and never changes that policy.

## Decision

An immutable, versioned plan selects workloads, a bounded pause duration and a
resume-readiness budget. V1 has one checkpoint: after setup and warm preparation,
before the trigger. Each selected workload must have exactly one deployment and
one running request-serving VM, no pending jobs, commands, tail work or effects,
and its original live recorder. The run must not have admitted managed jobs yet.

Apid records intent through an account-owned run endpoint. Schedd claims each
intent once under the app lock and moves its instance from RUNNING to FROZEN,
retaining the full memory reservation. Vmmd holds one destruction-joined flight
across fence, pause, wait, containment revalidation, resume hook and readiness.
It joins idle relay connections before pausing and retains the original recorder.
Only that owner can record the physical pause and resume. Schedd republishes the
same instance with its captured runtime configuration fence, then acknowledges
completion. No owner reconstructs observed proof from a persisted session row.

New recorded outbound effects are fenced while paused. Only the bound VM regains
authority during resume, allowing its hook to reconnect through the contained
adapters without becoming a routable serving target. Missing
owner, failed fencing, changed runtime configuration, unsupported capability,
uncertain RPC or incomplete cleanup cannot pass. A completed physical receipt
can finish publication after a lost reply; an uncertain pause is never repeated.

Comparison retains lifecycle identities for inspection and compares budget
outcomes alongside the existing data, object, notification and job evidence.
Missing receipts remain inconclusive. A measured resume-budget violation is a
behavioral failure even when both revisions exceed it.

## Boundaries and validation

V1 requires contained command execution and the warm profile. It supports one
pause per selected request-serving workload, including declared async HTTP
workers after they resume. Pausing in-flight jobs, arbitrary processes, private
network members, replicas, cold-start/worker-termination combinations, persisted
snapshot restore and credential rotation during the pause require other protocols.
Existing isolated database sessions retain their normal transaction semantics;
pausing a VM does not pause its fixture database. Database collector completion
checks and object mutation receipts continue to define their evidence boundaries.
The pause is bounded to 30 seconds and resume evidence to 120 seconds; these are
protocol bounds, not account quotas. The resume hook always runs before readiness.

`FAAS_REHEARSAL_SUSPEND_RESUME=1` independently gates new enrollment. Durable intent
handling continues after admission is disabled. Both-store, owner-loss, race,
comparison and RPC tests are portable. Rollout additionally requires dedicated
native x86 KVM lifecycle, containment, credential and cleanup acceptance.
