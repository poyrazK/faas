# ADR-20261019120014: Durable progressive deploy abort and guarded recovery

## Status

Implemented. Production progressive admission remains disabled pending native
qualification, including these controls.

## Context

ADR-20261019120013 canaries promote automatically or stop with `needs_operator`. Operators
need to stop a rollout and retry baseline verification without editing database
state, overwriting newer production intent, or moving already pinned jobs.

## Decision

Add `gregale deploy status|abort|recover <attempt-id>` and an authenticated
`POST /v1/project-submissions/{attempt_id}/{action}` endpoint. Deployment write
scope and MFA protect mutations; existing account-owned receipts protect access.
A decision requires a 32-character hexadecimal request ID and the inspected
checkpoint generation. A replay of the most recent identical decision returns
its current receipt without another mutation. Different decisions use generation
CAS; concurrent decisions cannot both commit. Reusing an identity with different
semantics conflicts. The request body is capped at `ProjectReleaseControlMaxBytes`
in `pkg/api/limits.go` and rejects unknown fields, including a force flag.

Persist action, identity, expected generation, time and pending/restored/blocked
status in the existing canary checkpoint and public receipt. Keep the existing
submission, configuration-environment and project lock order. Commit checkpoint,
receipt, graph, paired configuration and worker reconciliation outbox together.
No new SQL table, direct VM lifecycle operation or additional concurrency grant
is introduced.

At 5% or 25%, abort preserves the active baseline graph and configuration head,
ends candidate assignments and finishes the receipt as rolled back. At 100%, an
abort blocks new unassigned requests and first-delivery jobs, resets probe
checkpoints, and requests baseline restoration. Existing explicit pins and
claimed job pins remain on their retained graph and are never rewritten.

Full-traffic restoration requires fresh matching-generation attributed baseline
probes, eligible retained artifacts, qualified worker runtime configuration and
healthy baseline job evidence when a worker is included. Candidate health does
not authorize restoration. The existing graph/configuration, complete membership,
source and binding guards still apply; newer operator intent causes conflict.
Graph and configuration restore in the same transaction, using existing guarded
publication. A failed checkpoint write rolls back both.

Recovery is a retry of this health-gated restoration from a `rollback_failed`
receipt with a `needs_operator` checkpoint. It gives the controller another
bounded window using `ProjectReleaseVerificationTimeout`; it never extends the
retained graph TTL. Pending recovery retains eligible baseline/candidate worker
pools under normal quotas. Unknown or failing baseline evidence cannot restore;
window expiry marks the decision blocked and keeps new unassigned work closed.
The command does not manufacture worker jobs to overcome missing evidence.

Expose current actions in the receipt and print percent, generation, decision
status, blocking message and request ID in CLI status. Mutation commands return
on durable acceptance; a pending decision is not reported as completed recovery.
The CLI prints the request ID before writing and accepts `--request-id` and
`--generation` for retry after an ambiguous response.

## Validation

Memory and PostgreSQL suites cover early abort, full restoration, blocked recovery,
stale probes/generations, competing decisions, configuration ABA, immutable worker
job pins, lost-response replay and transactional restore write failure/restart.
HTTP scope and strict-body contracts and CLI routing/receipt contracts cover the
public boundary. Native KVM qualification remains required before enablement.
