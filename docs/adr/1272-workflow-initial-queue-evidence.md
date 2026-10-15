# ADR-1272: Initial recovery evidence in the automation queue

## Status

Accepted, 2026-10-13.

## Context

Retry queue entries expose exact parent/child evidence diagnosis and inspection
commands. Initial recovery entries still require operators to find their review
and invoke the separate initial evidence endpoint themselves.

## Decision

Add optional `initial_evidence` to queue entries without changing retry `evidence`
or its diagnosis type. Its object includes `stage`, the existing
`WorkflowInitialEvidenceDiagnosis`, `evidence_url` and `cli_command`. Initial
recoveries use their retained review identity and app/account scope; do not adapt
them into a retry plan or invent a parent/child lineage.

Prepared reviews report `stage: prepared`, the planned resumed generation and
`not_submitted_automatic_initial`. No job has been submitted, so repair is false
and the CLI explains that evidence repair is unavailable. Submitted reviews
report `stage: submitted` and reuse the same exact-generation observation as
GET initial review `/evidence`, including verified terminal proof, running,
missing history/jobs, superseded generations and conflicts.

Link to `/v1/apps/{slug}/initial-recovery-reviews/{reviewID}/evidence` and the
matching `gregale automations failure-recovery-evidence --app ... --review ...`
inspection command. Never append a repair flag or fabricate audit request IDs.
Current policy controls and last worker waits remain separate from fresh proof;
a policy pause cannot turn a prepared review into a submitted or repairable job.

Enrich only the already bounded returned queue page under the existing memory
lock or PostgreSQL read-only repeatable-read transaction and request timeout.
Diagnosis uses the queue observation time. Missing/expired review scope reports
existing `evidence_unavailable_reason`; other storage errors fail the read. Queue
reads do not prepare/submit reviews, save proof, evaluate a worker, emit alerts,
append history or spend/refund budget. Worker aggregation remains unchanged.

OpenAPI, Node and Python SDK models add optional entry fields and a dedicated
initial queue evidence model. Existing queue endpoint authorization and MFA
continue to apply. No new route, migration or limit is needed.

## Consequences

The queue displays current evidence guidance for initial recovery and retries
with their correct diagnosis routes. Old responses remain valid and old clients
ignore the new optional field. Prepared generation is a target, not admission
proof; submitted diagnosis remains a snapshot rather than authority to execute.
