# ADR-1001: Durable recovery of reviewed native workflow runs

Status: Accepted

## Context

Saved automation run plans pin explicit failed/dead run IDs and resume counts, but local execution stops with the CLI process. Native runs can originate from schedules or manual starts and have no event acceptance receipt.

## Decision

Reuse the durable recovery job lifecycle and scheduler, including account quotas, request identity, pause/resume/cancel, rate limits, expiry, audit history and completion notifications. Add explicit `workflow_runs` selection with workflow mode. No event filters or receipt protection are accepted. The existing recovery endpoints expose these jobs; the CLI submits saved run plans with `automations recovery-start`.

Run items carry their immutable run ID and reviewed count in expected progress, with a null outbox identity and empty event fields. A database constraint separates native run targets from receipt targets, and an index prevents duplicate runs per job. No artificial event receipts or foreign keys to prunable runs are introduced. Migration downgrade is blocked while run recovery jobs remain retained.

Creation validates every selected run's account, application, workflow, terminal status and reviewed count. Idempotent retries resolve a retained job before selecting or admitting work; a changed normalized plan or initial rate conflicts. Workers never refresh reviewed counts or select additional failures.

The existing transactional native resume implementation performs eligibility, deployment, tenant, cancellation and count checks, preserves completed work, resets only the safe continuation, and enqueues the run. Resume and item outcome commit in the same transaction as job controls and rate accounting. Concurrency pressure keeps the original item pending with the existing bounded capacity retry. Changed targets are skipped; unsafe, missing or unavailable targets are skipped as target unavailable. Cancellation stops pending admissions; it does not cancel already resumed runs.

## Consequences

Recovery survives CLI disconnection and worker restart. Shared quotas limit all recovery modes together. `queued` proves resume admission, not successful workflow execution; inspect workflow runs for execution outcomes. Preflight reports native eligibility as unknown because its event capacity estimator does not model workflow resumes. Jobs expire after the existing 24-hour lifetime and are retained for the existing 30-day period; pause does not extend either limit. Original CLI journal execution remains available.
