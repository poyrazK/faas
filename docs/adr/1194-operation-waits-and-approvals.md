# ADR-1194 · Operation waits and approvals

- **Status:** accepted for implementation; admission stays behind the ADR-521 preview gate
- **Date:** 2026-10-13
- **Owner:** Operations, scheduler and apid maintainers
- **Related:** [ADR-521](521-customer-operations.md), [ADR-262](262-durable-workflow-timers.md),
  [ADR-263](263-authenticated-workflow-callbacks.md), [ADR-676](676-workflow-customer-operations.md)

## Context

A workflow Operation is a linear chain of HTTP steps. Real work pauses: it waits
for a cooling-off period, or for a person to approve it. The native workflow
engine already parks a run on a timer (ADR-262) or a one-time callback
(ADR-263) and resumes it later, with no resident app instance in between. The
operation adapter rejects those steps, so an application keeps its own polling
row and approval table, and the customer sees `running` for work that is idle.

## Decision

The operation workflow adapter admits two wait steps in its linear chain:

- `wait_for_duration` — a **timer** wait with a fixed duration;
- `wait_for_callback` — an **approval** wait, which the definition must declare:

```yaml
operations:
  - name: refund
    workflow: refund-chain
    approvals:
      - step: manager-approval
        approver: account     # or: customer
```

Every callback step must be a declared approval and every approval must name a
callback step. Event waits, condition checks, branches and outbound steps stay
excluded. Progress stages still equal step names, so a wait is a visible stage.

**Waiting state.** `waiting` is a new non-terminal, active operation state. While
the current step of the generation is a parked wait, the operation is `waiting`
with a `waiting` object: `kind` (`timer` or `approval`), `step`, `since`, `until`
(the timer's wake time or the approval deadline) and, for approvals, the
`approver`. Parking emits a `waiting` event; leaving the wait emits `resumed`.
Parking now synchronizes the projection in the same transaction as the park, as
step transitions already do. Every predicate that keeps active work — pending
quota, code pins, retained idempotency, history visibility and scheduler pickup —
treats `waiting` like `running`. A waiting operation holds no app instance.

**Approvals.** The configured approver decides through
`POST /v1/apps/{slug}/operations/{id}/approvals/{step}` (account,
MFA, deploy write scope) or
`POST /v1/platform-tenant-self/customer-operations/{id}/approvals/{step}`
(customer, operations manage scope), with `expected_generation`, `decision`
(`approved` or `rejected`) and an optional bounded comment. Only the declared
approver kind may decide. The decision completes the step's callback with a
platform-written payload that records the decision, the actor kind and the
deciding key or token ID, and the comment. Repeating the same decision returns
the original; a different decision conflicts. Generic callback completion is
refused for operation-linked runs, so no other path can write that payload.

An approved step succeeds and the chain continues. A rejected step fails
without retry and the operation settles `failed` with `approval_rejected`. An
approval whose step timeout passes settles `failed` with `approval_expired`. Wait
steps never perform external effects, so their failures are certain: they never
enter `requires_reconciliation`. The decision and its actor appear as an
`approval_decided` event in the operation's history.

**Cancellation and recovery.** Cancelling a waiting operation stops it without
uncertainty; its parked step is skipped. An approved step is a confirmed fact:
an evidenced `safe_to_retry` resume after a later failure keeps it and does not
ask again.

## Consequences

- Approval and cooling-off flows need no application polling, table or
  long-lived process, and the customer sees `waiting` with its deadline.
- Clients that switch exhaustively over operation states must handle `waiting`.
- The bound on a wait is the plan's existing wait cap (ADR-262).

## Rejected alternatives

- **Let any callback complete an approval.** The payload would be forgeable by
  any caller with callback access, and the actor would not be recorded.
- **Express rejection with a `when` branch.** Branches stay excluded from the
  first adapter; a rejected approval has one meaning, so a fixed failure code is
  clearer than a declared branch.
- **Keep `running` and add a flag.** Customers and quotas would still treat idle
  work as executing, and history filters could not select waiting work.
