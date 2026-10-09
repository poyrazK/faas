# ADR-901: Business workflow observations and transactional evidence

## Status

Accepted for the customer Operations HTTP implementation.

## Context

Versioned workflow contracts (ADR-900) establish transition continuity and
milestone evidence. Applications also need to explain business decisions,
blocked actions, dependencies, confirmed effects, and recovery without moving
their business rules or external side effects into the platform.

## Decision

Gregale retains application-reported blockers, deadlines, terminal outcomes,
and concrete workflow dependencies. Scoped attention, outcome, impact, trace,
readiness, and action-preview reads expose those observations through the API,
CLI, dashboard, and SDKs. Dependency evaluation and graph traversal are bounded;
missing observations remain unknown rather than becoming implicit success.

Immutable transition contracts can require exact policy, invariant, and effect
evidence. Versioned milestone envelopes carry decisions, passed/failed/unknown
invariants, and pending/failed/confirmed effects. Validation binds evidence to
the workflow instance, Operation, state, and declared contract. Invariant and
effect evidence cannot be reused by another report for the same Operation.

Go, Node, and Python application transaction helpers validate queued evidence
before commit and publish retained milestones before workflow state reports.
Readiness guards pin the observed state revision and contract version; they do
not reserve platform observations or replace application database locks.

Explicit reconciliation permissions allow applications to publish authoritative
snapshots with reconciliation evidence. Compensation observations reference a
retained confirmed effect in the same account, application, customer, and
environment. Applications authorize cross-subject relationships, execute
reversals, and explicitly report their workflow state.

## Consequences

- Business state and execution remain owned by the application; Gregale does
  not execute policies, external effects, or compensation actions.
- Publication lag and retention constrain observations. Readiness is a bounded
  assessment of reported facts, not authorization or a distributed transaction.
- Seven platform migrations add workflow projection fields and indexes.
  Applications must install the updated customer transaction/outbox schema.
- Contract changes require new immutable versions. Existing contracts preserve
  their dependency semantics when transition selectors are omitted.
- Compensation source validation can fail after application commit if the
  source expires before publication; recovery must handle incomplete publishing.
