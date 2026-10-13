# ADR-1003: Retain workflow recovery outcomes and notify execution completion

Status: Accepted

## Context

Live native recovery observations become unknown when run or resume history is pruned. Admission completion does not prove that resumed execution finished.

## Decision

Retain the first confirmed terminal outcome of each admitted native resume with its recovery item. PostgreSQL captures on workflow mutations and deletion in the same transaction; memory captures under the workflow mutation mutex. Backfill only retained scoped run/resume evidence. Store the run ID, admitted resume number and admission timestamp without a workflow foreign key, payload, output or provider error. Recovery-item deletion cascades the evidence at normal job retention expiry.

Readers prefer saved evidence and never mutate state. Failed/dead native runs report failed; cancelled terminal runs report cancelled. A later generation is superseded only if no earlier terminal outcome was saved. An earlier saved failure remains a failure even if a later resume succeeds. Missing or inconsistent evidence stays unknown and blocks completion capture.

Reuse the scheduler-owned `event_recovery.execution_finished` notification after admission is completed, cancelled or expired and every queued resume has a saved outcome. Unknown and active executions block capture. Zero-admission jobs produce no execution notification. Capture completion and its stable event ID/recipient snapshot atomically through the existing outbox; retries preserve the existing delivery semantics. Include an authenticated relative items link for retained run identifiers. Execution health and notification inspection include native jobs.

## Consequences

Saved outcomes survive workflow-history pruning and server restart for the owning recovery job retention period. The first terminal result is authoritative for that resume, so it does not describe later resumes. Reads remain side-effect free. Webhook capture is once per job; network delivery retains the existing retry behavior and does not guarantee exactly-once receiver processing. Jobs with lost evidence can remain unresolved until retention expiry.
