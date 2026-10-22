# ADR-1196 · Operation deadlines and budgets

- **Status:** accepted for implementation
- **Date:** 2026-10-15
- **Owner:** Operations maintainers
- **Related:** [ADR-521](521-customer-operations.md),
  [ADR-1194](1194-operation-waits-and-approvals.md), ADR-1195 (unified timeline)

## Context

A durable Operation carries an identity and permissions, but nothing bounds how
long it may take or how much execution it may consume. Each HTTP attempt is
capped by the plan's request timeout, and retries by the plan's queue attempt
ceiling. An Operation that keeps failing, waits for an approval nobody gives, or
is recovered again and again has no business deadline. A developer cannot say
"this refund must settle within an hour" or "never spend more than five minutes
of compute on one export".

## Decision

An Operation definition may declare `limits`:

```yaml
limits:
  deadline_seconds: 3600       # wall clock from acceptance, waits included
  max_attempts: 5              # automatic attempts of one HTTP execution
  max_execution_seconds: 300   # attempt runtime summed across generations
```

Every field is optional and bounded by the plan (`pkg/api/limits.go`). The
definition revision changes only when `limits` is present. Limits are fixed at
admission and copied onto the Operation, so a later definition never changes
work already accepted. Per-submission overrides are out of scope.

**Deadline.** Admission records `deadline_at = accepted_at + deadline_seconds`.
It is absolute. Waits, retries, backoff and recovery all count against it, and
recovery cannot extend it.

- An HTTP execution's invocation deadline becomes the earlier of the plan's
  async deadline and the Operation deadline. The existing claim fence refuses a
  late claim, and the existing claim context cuts a running attempt there.
- The existing invocation deadline reaper settles a queued or retrying HTTP
  execution past its deadline. Workflow runs carry no deadline of their own, so
  a new sweep, run in the same 60-second schedd tick, stops every active
  workflow Operation (`accepted`, `running`, `waiting`) past its deadline. It
  takes the cancellation path, which also ends timer and approval waits; an
  indexed `customer_operations.deadline_at` column finds due work.
- The Operation settles `failed` with `failure_code: deadline_exceeded`, emits its
  `failed` event and sends its completion callback.
- An attempt cut while running keeps the existing uncertainty rule. Unless the
  definition declares `recovery: safe_retry`, it settles `requires_reconciliation`.
  A deadline never claims to have undone an external effect.

**Attempts.** `max_attempts` becomes the HTTP execution's retry policy, which the
drain already enforces under the plan ceiling. Exhaustion keeps today's outcome,
`failed` with `execution_failed`, and the timeline shows each attempt. Workflow
steps keep their own per-step retry declarations, so `max_attempts` is rejected
on workflow definitions.

**Execution budget.** For HTTP Operations, `usage.execution_ms` sums attempt
runtime, from claim to the attempt's end, across generations. A claim caps the
attempt at the remaining budget, so the attempt is cut when the budget runs out.
A claim with no budget left is refused. When an attempt ends with the budget
spent, or with its retry due after the deadline, the retry is cancelled. The
Operation then settles `failed` with `budget_exhausted` or `deadline_exceeded`.
Workflow steps are already bounded by their own step timeouts, so workflow
definitions take a deadline but reject `max_attempts` and
`max_execution_seconds`. ([ADR-20261016110803](20261016110803-workflow-operation-execution-budget.md)
later added `max_execution_seconds` for workflow definitions.)

**Recovery.** Recovery is refused once the deadline has passed or the budget is
spent; the operator sees `operation_limit_exceeded`. Generations share one
deadline and one budget.

**Jobs.** Job Operations have their own run deadline and are out of scope;
`limits` on a job definition is rejected.

**Surface.** `OperationResponse` gains `limits`, `deadline_at` and
`usage{execution_ms, attempts}`. The timeline shows the `failed` event with the
limit's failure code. `gregale customer-operations get` prints the deadline and
the remaining budget.

## Consequences

- Developers can bound business time per Operation, and compute per HTTP
  Operation. Abandoned approvals and retry loops end on their own.
- One new indexed `customer_operations.deadline_at` column feeds the workflow
  sweep. Usage lives in the Operation record and needs no extra writes beyond
  the existing transition.
- Workflow Operations settle up to one sweep (60 s) after their deadline; HTTP
  attempts are cut at the deadline itself.
- A deadline cut is still subject to reconciliation. Gregale promises a bound on
  its own work, not exactly-once effects.

## Rejected alternatives

- **A budget in money.** Money depends on plan RAM and price changes; runtime is
  what the platform controls. Plan RAM × runtime gives cost for display.
- **Pausing instead of failing at the deadline.** A paused Operation is still
  open work. Failure with a stable code is what callers and callbacks can act on.
- **Per-submission overrides now.** They touch idempotency fingerprints and every
  admission path (API, gateway route, webhook, tenant). That is a follow-up once
  definition limits are proven.
