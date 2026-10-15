# ADR-1331: Retained GitOps workload retirement and reviewed recovery

- **Status:** accepted
- **Date:** 2026-10-15
- **Related:** ADR-568, ADR-1155, ADR-198

## Context

Removing a workload from a definition is blocked by the workload-pruning
adapter. Queue retention already preserves accepted work, but transferring
field ownership alone cannot close routes, scheduler admission or schedules.
Deleting a shared app would affect other environments and destroy recovery
identity. Retirement needs durable execution fences and independently observed
completion, including after controller failure or a superseding approval.

## Decision

1. Require source pruning and reviewed `workload_pruning_policy: retain` for
   whole-workload removal. The source must own its scoped presence. Active
   overrides block the complete retirement. Plans keep its configuration and
   identify retirement through its presence change. Unmanaged consumers and
   unreviewed incoming dependencies block retirement rather than granting
   authority over another manager's resources.
2. Retain the app, logical mapping, configuration, artifacts, queue bindings,
   accepted messages and historical receipts. Persist a scoped retirement
   journal bound to the original identities, approved revision, generation and
   reviewed plan. Retire only this environment's execution lanes. No app,
   storage object or accepted invocation is deleted by this feature.
3. Reserve retirement without withdrawing the old release. Qualify the
   remaining reviewed graph first, then close new request, queue, schedule
   and wake admission in its atomic release cutover. Gateways invalidate their
   cached scoped admission decision before acknowledging the withdrawal
   generation; drain waits for convergence of the exact current roster.
   Reuse schedd's deployment drain ownership and gateway route generations;
   apid never writes instance state or calls vmmd. Current and retained release
   selection must not provide a fallback around retirement. Retain execution
   fences independently of controller leases. A crash cannot reopen admission.
4. Report retirement as pending until the current registered gateway roster
   has acknowledged route withdrawal and original executions have drained.
   Persist progress and recheck scope, ownership and current approval under the
   source lock. Failed or ambiguous observations cannot certify completion.
5. Recover through reviewed `workload_recoveries`, keyed by logical name and
   pinning the original retained app UUID. Original queue identities still use
   the existing reviewed queue-recovery contract. Recovery obtains fresh
   preparation, qualification and activation; historical serving receipts and
   ordinary release promotion cannot reopen the lane. Retain retirement
   history after a successful recovery. Original deployment and layer references
   stay pinned through retirement; fresh reviewed activation releases this
   retirement-specific code pin without overriding other durable-work retention.

## Validation and rollout

Cover both memory and PostgreSQL stores, stale reviews and leases, scope and
ownership isolation, empty membership, interrupted retirement and recovery,
gateway roster changes, retained accepted work, and SQL guard bypass attempts.
Exercise removal and recovery through the real apid HTTP boundary. Extend the
opt-in native journey with work in flight, retirement, controller restart and
reviewed recovery. Native VM acceptance still requires an exclusive dedicated
x86_64 Linux KVM host. Production qualification and enforcement remain gated;
shared nested diagnostic execution cannot qualify these changes.
