# ADR-1002: Observe the admitted workflow recovery generation

Status: Accepted; terminal retention and notifications extended by [ADR-1003](1003-workflow-recovery-terminal-results.md)

## Context

Durable native run recovery reports admission as queued. Operators must also see whether resumed runs remain pending, run, fail, finish or are resumed again. Admission completion cannot establish execution success.

## Decision

Extend existing read-only recovery job and item execution observations to workflow mode. Scope every lookup to the owning job account, application and workflow, and require the retained resume record for the job's original reviewed count plus one. Batch PostgreSQL observations through sqlc in the existing repeatable-read transaction; memory observations run under the same store mutex as workflow mutations.

Use `source: workflow_run`, with zero invocation dispatch attempts. Native failed and dead states both count as failed; awaiting-event counts as running. Cancelled terminal runs report cancelled. A later resume reports superseded without attributing its terminal state or completion time to the job. Missing, pruned, future or inconsistent evidence is unknown. Pending, skipped and cancelled admission items are not tracked as resumed execution.

Reads perform no writes, resumes or history refreshes. Admission counters and job lifecycle remain independent of execution observations. Retained terminal invocation results and their notifications retain their existing behavior.

## Consequences

Job status and paginated items expose live native execution progress using existing DTOs, CLI rendering and SDK endpoints. Native observations are not durable terminal results: saved_results stays zero, and pruned workflow history becomes unknown. Native execution-finished notifications and permanent terminal capture remain follow-up work. Job discovery lists retain their existing metadata-only behavior; inspect individual job status for execution summaries.
