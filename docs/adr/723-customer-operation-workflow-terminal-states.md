# ADR-723: Declare terminal business workflow states

## Status

Accepted

## Context

Applications can report a workflow's current state and declare allowed state
transitions. Business-reference reads expose that current state, but callers
cannot tell whether the application considers the workflow complete or still
active without interpreting state names in application-specific code.

## Decision

Workflow declarations may include `terminal_states`, an optional list of
states from the declared `states` vocabulary. A terminal state cannot have an
outgoing transition. If the list is omitted, no reported state is classified
as terminal. The application remains responsible for reporting state in the
same transaction as its business write.

Gregale pins the list in each immutable Operation workflow mapping. Current
workflow-state reads return a `terminal` boolean evaluated against the pinned
definition that reported the current state. The dashboard and CLI display the
classification. A state not in `terminal_states` is active. The classification
does not change Operation lifecycle, trigger actions, or infer state from
milestones.

## Consequences

- Business workflow reads distinguish application-declared terminal states
  from active states without hard-coding business vocabulary in clients.
- Invalid terminal declarations and outgoing transitions from terminal states
  are rejected during definition compilation.
- Applications keep authority over workflow progress and completion; Gregale
  only validates and displays the declared meaning.
