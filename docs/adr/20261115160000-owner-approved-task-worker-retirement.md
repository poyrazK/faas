# ADR-20261115160000: Owner-approved Task worker retirement

Status: accepted

## Context

The agent Task release journal retains a permanent executor fence, a one-time
web restore and a separately observed candidate claim quarantine. Quarantine
leaves worker instances running. Reusing restore or quarantine authority for
shutdown would give an earlier verdict an additional effect.

## Decision

Add a separate immutable `retire` qualification, fixed one-minute owner approval
and local `gregale_apply_task_tool_retire` tool. The private CLI holds the Task
release lock and uses only the observer database role to inspect exact retained
worker IDs under the claim transaction lock. Qualification and parking require
observed claim fences, source and role bindings, restored web/catalog/policy,
replacement workers, observer evidence and native startup/shutdown attestations.
The native worker stop grace must exceed its reported Task shutdown deadline.

The dedicated native retirement route delegates to conditional parking. Concrete
memory/PostgreSQL storage compares the approved native snapshots, policy, VM set,
worker generation, active owner/requester and deadline; approval consumption and
the actual app park transition commit together. Schedd remains the sole instance
lifecycle writer. No fallback to unconditional parking is permitted.

`retirement_pending` is a durable park request, not completed shutdown. Retries
never park again. Read-only reconciliation can continue after approval expiry or
revocation. It requires the same native park transition and deployment, no live
native instances, and absent or expired Task registrations before retaining
`retired`. A lost reply is reconciled through this journal. A retained result is
historical evidence and never undoes a later wake or changes traffic.

## Consequences

Customer Task SQL observations and native state do not share a transaction.
Trusted CLI observations attest customer registrations; the native transaction
independently checks drain and transition identity. Fences remain retained.
Active Tasks finish/abort through normal graceful shutdown; no Task cancellation,
lease deletion, schema mutation or traffic operation is added. Restarted workers
and changed generations require owner investigation rather than substituted IDs.
Dedicated native KVM acceptance remains required for VM lifecycle validation;
local database and adapter tests do not replace it. Live staging stays skipped.
