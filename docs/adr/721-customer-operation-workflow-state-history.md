# ADR-721: Customer Operation workflow state history

## Status

Accepted

## Context

Applications report workflow state in the same transaction as their business
write. Gregale already retains each idempotent report and projects the newest
revision for a workflow run. The current projection helps answer what state a
run is in, but it does not show how the application reached that state.

## Decision

Business-reference milestone reads return `workflow_state_history` when the
caller selects one exact `(workflow, workflow_instance_id)` pair. Entries are
ordered by app-assigned revision, then publication time and stable report IDs.
Each entry includes its state edge when present, occurrence and publication
times, and the Operation that published it.

History uses a separate bounded opaque `workflow_state_cursor`. Its digest binds
account, customer, app, environment, business reference, workflow run, and feed
role. The milestone cursor remains independent. Both account operator and
tenant-self reads enforce their existing ownership boundary and omit expired
settled Operations using result retention. No schema migration is needed; the
existing retained workflow state report ledger contains the required history.

The customer dashboard links observed runs to their history and each report to
its Operation. Go, Node, generated Python, and CLI interfaces expose the
separate cursor.

## Consequences

- Applications can inspect an explicit, revision-ordered state trail without
  inferring transitions from milestones or Operation lifecycle events.
- Delayed report publication does not change order because app revisions are
  the primary keyset sort.
- The history remains observational and follows each Operation's existing
  retention and access rules.
