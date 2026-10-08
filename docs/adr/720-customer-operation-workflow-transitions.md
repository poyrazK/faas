# ADR-720: declared customer Operation workflow transitions

Status: accepted for the internal HTTP implementation.

## Context

ADR-719 lets applications report a state from the same transaction as a
business write. A declared state vocabulary catches misspelled values, but it
does not describe which state changes belong to the workflow.

## Decision

Applications may declare allowed `from` and `to` state pairs alongside the
workflow state vocabulary. When a workflow declares transitions, the Node
transaction API requires `workflowTransition(workflow, instanceID, fromState,
toState)` for state updates and rejects direct `workflowState` reports for
that workflow. The report stores its source state and destination state in the
application transaction and durable outbox. Gregale validates that the pair
appears in the immutable workflow definition before the application
transaction commits.

The application checks the source state against its business row while
holding its database lock. Gregale validates the declared edge and preserves
the report; the application's database remains authoritative for concurrent
business updates. Workflows without a transition declaration may continue to
report a state directly with `workflowState`.

## Consequences

Transition changes are pinned with each affected Operation definition.
Invalid edges fail before the business transaction commits. Reports retain
both endpoint names for idempotency and audit. Apps can serialize concurrent
updates using their existing business row locks, while Gregale preserves
revision fencing and recovery publication behavior.
