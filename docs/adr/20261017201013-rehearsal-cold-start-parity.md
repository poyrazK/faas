# ADR-20261017201013: Contained cold-start parity with original-owner receipts

Status: Accepted, rollout disabled pending native qualification

## Context

The ordinary cold scenario profile drains previews and invalidates their
snapshots. Contained rehearsals also need proof that the original relay owner
settled before that drain, and that each subsequent serving execution actually
cold booted. A successful park response or a guest assertion supplies neither.

## Decision

Cold contained enrollment pins a versioned startup budget and the logical
workload inventory. Deployment priming verifies the artifact without starting a
serving VM; the scenario trigger owns the first boot and its startup failures.
The existing preview-only fresh park arms one preparation
per workload against its sole deployment and at most one running serving VM.
Preparation refuses outstanding jobs, commands, tail work and unsettled recorder
requests. Schedd owns retirement of the selected instance. The original vmmd
recorder fences new requests, joins its relays, and acknowledges retirement only
after normal physical teardown succeeds. Ordinary destruction cannot supply
this acknowledgement. Uncertain teardown is never repeated as a planned action.

After the old instance is terminal and all snapshots are invalidated, apid
marks preparation ready. The next serving VM must have a different identity,
must reject snapshot inputs before boot, and must retain its actual boot method
and elapsed startup time through its original recorder owner. Commands continue
using their own contained task VMs and cannot substitute for serving boot proof.
The sealed action snapshot retains preparation, deployment, instance, wake and
recorder identities. All managed job attempts must use that verified serving VM.

Comparison excludes generated identities and raw timing jitter. It compares
startup-budget outcomes alongside independently collected database, object,
notification and managed-work evidence. Missing retirement, boot, inventory or
collector proof remains inconclusive. Startup-budget violations remain visible
as behavioral failures.

## Boundaries and validation

V1 supports `real-vm --profile cold`, one preparation and one post-preparation
serving VM per declared workload, and one deployment per workload. Warm behavior
is unchanged. Restore/resume, multiple serving replicas, and combining cold
preparation with worker termination require separate protocols and qualification.
Setup must not admit managed jobs before preparation begins.

`FAAS_REHEARSAL_COLD_START=1` gates new enrollment on apid, independently of
earlier rehearsal gates. Durable preparation and receipt handling continues
when admission is disabled. Portable tests cover both stores, owner loss,
in-flight effects, cleanup failures, wrong boot identity, startup budgets and
behavior differences. Rollout additionally requires the dedicated native x86
KVM, containment, credential-delivery and cleanup acceptance gates.
