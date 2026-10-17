# ADR: Python durable retry and dead-letter acknowledgment intents

- Status: Accepted
- Date: 2026-10-17

## Context

Python can atomically stage completion intents and recover the shared Go/Node
negative actions, but its failure methods still send immediate callbacks. An
explicit failure decision needs durable staging so a crash can resume the chosen
callback without rerunning business work or changing the selected disposition.

## Decision

Add `save_retry_intent` and `save_dead_letter_intent` to the async worker. Callers
must establish known rollback, stopped work and exclusive job ownership, then
durably persist their explicit action/reason. No exception or missing receipt
automatically authorizes a negative decision.

Hold the existing consumer advisory transaction lock, compare retained receipt
scope/input without loading its result, and block rejection on a matching receipt.
Preserve the receipt conflict error for mismatches. Save the negative intent under
the existing invocation lock in the same transaction. Reuse the canonical DDL,
immutable digest/supersession protocol, cancellation and unknown-COMMIT handling.
The save invokes no handler, platform renewal or HTTP callback. Completion staging
continues through the same shared writer with its original action/reason/body.

Expose committed metadata only. Repeat saves preserve active claims, counters,
due times and confirmation. Changed action/reason conflicts. Unknown COMMIT
returns no confirmed status and closes the connection. Recovery already supports
the selected negative actions, rechecks receipts and fences callback settlement.

Extend the protected queue example with an explicit fenced transition from
version-2 `python-durable` to version-3 `python-durable-failure`. Preserve original
delivery/owner/ID; freeze the selected action/reason. The adapter must atomically
persist and refresh the owned record before resolving. Failure records are
processed only by a saved-decision helper and rejected by business-processing
entry points. Ambiguous queue persistence requires retiring/reloading the handle.
Only confirmed intent COMMIT transfers callback responsibility and permits fenced
ingestion-job acknowledgment.

## Consequences

Successful completion and explicit failure decisions can use the same durable
recovery host. Existing immediate APIs and completion job formats remain intact.
The application owns queue persistence/fencing, rollback knowledge, stopped work,
timeouts, protected storage and retention. Database/HTTP and queue/intent storage
remain separate transactions; the durable phase preserves the decision between
them. Neither receipt absence nor locks grant durable-job ownership or stop
external effects. No platform migration, endpoint, schema/SDK registry change or
external exactly-once guarantee is introduced. This extends the completion-only
staging scope of the [Python intent ADR](20261017140016-python-durable-acknowledgment-intents.md).

## Validation

Use compilation/lint, Python 3.11 syntax checks, isolated generation without test
files, wheel/sdist inclusion, canonical schema and documentation/static checks.
No tests/assertions are added/changed and no local tests are run. Hosted receipt
contention, supersession, cancellation, unknown COMMIT, restart and callback
qualification remains pending before release. See the
[failure-decision contract](../event-acknowledgment-python-intents.md#persist-explicit-failure-decisions).
