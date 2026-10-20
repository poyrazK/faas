# ADR-20261016110803 · Workflow Operation execution budget

- **Status:** accepted for implementation
- **Date:** 2026-10-16
- **Owner:** Operations maintainers
- **Related:** [ADR-1196](1196-operation-deadlines-and-budgets.md),
  [ADR-1194](1194-operation-waits-and-approvals.md)

## Context

ADR-1196 gave every Operation a deadline, and HTTP Operations an execution
budget, `max_execution_seconds`. Workflow Operations rejected the budget on the
grounds that their steps carry their own timeouts. A step timeout bounds one
step, though, not the Operation. And the deadline counts waits as well: a
workflow that waits days for an approval has no bound on the compute its steps
spend.

## Decision

Workflow definitions accept `limits.max_execution_seconds`, with the same plan
bound as HTTP definitions. It bounds the summed runtime of the Operation's HTTP
step attempts, across steps and recovery generations. Timer, approval and event
waits do no work and do not count.

- **Attempt deadline.** Each HTTP step attempt ends at the earlier of its step
  timeout and its start plus the budget still unspent. The scheduler cuts the
  attempt there, and the guest's control endpoint reports the same deadline. A
  success committed after it is refused, as for a step timeout.
- **A cut attempt is uncertain.** Like every failed step of a workflow
  Operation, it may have had an effect, so the Operation needs reconciliation.
  Gregale does not settle it `failed` with `budget_exhausted`.
- **Recovery.** A `safe_to_retry` recovery is refused once the budget is spent,
  as in ADR-1196. Recording an outcome still works.
- **Usage.** `usage.execution_ms` and `usage.attempts` count the run's finished
  HTTP step attempts. They are recomputed from the step attempt ledger whenever
  the Operation's projection syncs, so the reported usage is what the recovery
  check reads.

`max_attempts` stays HTTP-only. Workflow steps never repeat automatically:
workflow definitions require reconciliation recovery, so a failed step always
waits for a decision.

## Consequences

- Compute per workflow Operation has a bound independent of how long its waits
  last.
- No schema change: the limit and usage live in the Operation record, and
  attempt runtime comes from the existing step attempt ledger.
- An attempt that starts with little budget left is cut early and needs
  reconciliation. Developers should size the budget for the whole chain.

## Rejected alternatives

- **Automatic step retries bounded by `max_attempts`.** They would need
  `recovery: safe_retry`, which workflow definitions forbid. Changing that is a
  separate decision.
- **Settling a cut attempt `failed` with `budget_exhausted`.** The attempt may
  have taken effect; only reconciliation can say.
- **Refusing to start an attempt below a minimum remaining budget.** It adds a
  second threshold to explain, and an operator still has to decide.
