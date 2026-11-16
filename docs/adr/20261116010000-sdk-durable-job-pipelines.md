# ADR: Typed Jobs inside code-first workflows

Status: Accepted (SDK preview)

## Context

Typed Jobs, owned files and live progress exist independently. Composing them
requires application code for durable submission, waiting, artifact verification
and reconnecting to the exact run. The existing workflow runtime journals step
results and suspends on durable sleeps. Native Jobs keys have a bounded lifetime;
replaying a submission after that window can create another job.

## Decision

Add an authored SDK layer over the existing workflow step/sleep protocol. It
registers typed job stages, validates root input/output envelopes and records
small verified results. No scheduler, native API or platform table changes.

Each stage uses an application-owned PostgreSQL intent/checkpoint ledger. One
acknowledged autocommit intent precedes any Jobs POST. Duplicate drivers reread
the intent; uncertain acceptance never triggers another POST. Read-only run-ID
reconciliation matches the exact account, endpoint, contract and input manifest
before adopting the accepted checkpoint. Records must outlive possible replay.

Native Jobs confirms stage completion; native workflow status confirms pipeline
completion. File handoffs carry scope, owner, key/version, size and digest.
Application authorization precedes start, inspection, reconciliation and live
snapshot access. Backend credentials remain private. Node and Python share wire
receipts and journal results, while keeping native SDK units and type conventions.

The client supports typed start, attach-by-ID, inspect, result and native cancel.
Attach does not restart failed code workflows; that platform limitation remains.
Cancellation stops orchestration and does not automatically cancel accepted Jobs
or undo effects. Existing workflow, Job and file callers keep their APIs.

## Consequences and validation

Applications can assemble durable background processes with their existing
workflow deployment. Side effects remain at least once and need application
idempotency. Poll, operation and journal budgets are explicit. Large payloads
belong in file artifacts rather than repeated workflow results.

Shared fixtures and Node/Python tests cover completed-stage replay, uncertain
submission, lost store acknowledgements, concurrent drivers, deadlines and
isolation. A real PostgreSQL CSV journey alternates Node/Python workflow drivers
and task workers, drops submission/completion journals, verifies reports and
checks live readiness. Its workflow/storage/scheduling adapters are local
protocol fixtures; deployed capability acceptance remains separate.
