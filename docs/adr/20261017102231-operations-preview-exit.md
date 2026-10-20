# ADR-20261017102231 · Leaving the Customer Operations preview

- **Status:** proposed
- **Date:** 2026-10-17
- **Owner:** Operations maintainers
- **Related:** [ADR-521](521-customer-operations.md),
  [ADR-666](666-operation-execution-preview-admission.md),
  [ADR-20261016123221](20261016123221-customer-operations-telemetry.md)

## Context

Customer Operations admission is closed. ADR-666's preview policy admits
named cohorts for a bounded window (30 days since the ADR-521 pilot amendment),
and each execution kind (`http`, `workflow`,
`job`) needs native KVM qualification receipts before a cohort may include it.
No production cohort has ever run, and nothing says what evidence ends the
preview or how admission opens beyond named cohorts: the policy deliberately
has no wildcard.

The v1 feature set is now complete: webhook submission, waits and approvals,
signals from accounts, customers and providers, deadlines and budgets, one
timeline, and fleet telemetry with alerts (ADR-20261016123221).

## Decision

Admission opens in three stages, one execution kind at a time. Each stage has a
gate of recorded evidence for the exact release being promoted. A gate that is
not met keeps the earlier stage; nothing advances by default.

**Stage 1 — first cohort (HTTP).** Gate:

- Native qualification on a dedicated x86_64 KVM host for that release:
  `test-metal`, `leakcheck` clean, and the native Operations acceptance covering
  one Operation end to end. The scenario is webhook admission, the attempt on a
  Firecracker worker, a lost response with a reconciliation decision, a deadline
  cut, cancellation, the completion callback, and a scheduler restart mid-attempt.
- The ADR-20261016123221 alerts are routed to on-call, and the dashboard reads live
  series from the production fleet.
- An admission rollback drill: close the policy, verify closure on every serving
  node, and confirm accepted work continues.

The cohort is one app and at most ten named customers, in a policy window of
at most 30 days that the operator renews. The native scenarios are the
`customer-http-operations-only` lane, which selects every
`customer_http_operations*_metal_test.go` scenario.

**Stage 2 — native kinds.** `workflow` and `job` join a cohort only with their
own Stage 1 receipts. A workflow receipt adds an approval wait, a signal by
subject, an event-wait timeout and a budget cut. A job receipt adds a job run
and its cleanup.

**Stage 3 — general admission per kind.** A new operator setting,
`operations_general_kinds` in apid and gatewayd-internal, lists the kinds
admitted for every account whose plan allows Operations. Plan limits,
authentication and definition rules apply unchanged. Kinds not listed stay
behind the preview policy. An empty or absent setting means none, as today.
Gate for a kind, over at least 14 consecutive days of cohort traffic on it:

- at least 1,000 accepted Operations of that kind across at least three
  accounts;
- `FaasCustomerOperationDeadlinesOverdue` never fired;
- every `FaasCustomerOperationOutcomesUnknown` firing has a written cause, and
  none was a platform defect left unfixed;
- zero leaks reported by `leakcheck` on the fleet's compute nodes;
- no Operation needed a database repair or break-glass SQL.

Rollback is removing the kind from `operations_general_kinds`. It closes new
admission for non-cohort customers within one policy read, and never cancels
accepted work.

## Consequences

- Each launch decision points at recorded evidence for one release and one
  kind, not at a feature checklist.
- General admission needs one new config key read where the preview policy is
  read today; it ships with Stage 3, not before.
- The 14-day window and the volume floor delay general admission for kinds with
  little cohort traffic. That is intended: the telemetry has to have seen real
  work.

## Rejected alternatives

- **A wildcard cohort.** ADR-666 rejected it so that a policy could never open
  admission to everyone by mistake; general admission is a separate, explicit
  setting per kind.
- **Opening every kind together.** Workflow and job executions have their own
  lifecycles and cleanup; one kind's evidence says nothing about another's.
- **Time-boxed preview without evidence gates.** A calendar date does not show
  that deadlines are enforced or that attempts are not lost.
