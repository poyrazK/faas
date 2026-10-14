# ADR-1278: Independent manual observation and automatic execution polling

Date: 2026-10-13

## Context

The automatic recovery store method first observed a due manual initial review
and returned. With a continuously replenished manual backlog, the scheduler's
bounded automatic batch could contain only observations and never execute an
authorized automatic policy.

## Decision

Expose `WorkflowInitialManualCompletionStore` with a dedicated
`ProcessNextWorkflowInitialManualCompletion` method on memory and PostgreSQL
stores. The automatic policy method processes only automatic policy work.

The scheduler runs automatic execution first, followed by a separate bounded
manual observation pass. Centralize `WorkflowInitialManualCompletionPollBatch`
in the API limits table, initially matching the existing automatic poll batch.
Each pass owns its allowance: a full backlog, empty queue or non-cancellation
error in either pass does not consume the other pass's work allowance or skip
later recovery passes. Errors end and log only the affected pass. Shared parent
context cancellation still governs shutdown and request deadlines.

Keep manual observation's existing due ordering, persisted next-observation
clock, account/policy/review lock order, exact-generation proof checks, repair
wake-up, attention capture and history atomicity. It continues while current
policies are changed, disabled or paused. Neither lane gains execution authority
or changes automatic accounting rules. No SQL, schema or public API changes are
required.

## Consequences

A sustained manual backlog cannot displace automatic execution slots, and an
automatic backlog cannot displace the manual observation allowance. Each pass
can process up to its own cap per scheduler cycle, increasing the combined
maximum recovery work per cycle. Observation failure isolation applies across
passes; it does not promise that an unavailable database or canceled parent
context can continue processing. Regression coverage includes sustained queues,
errors and empty queues, plus a real manual backlog larger than its batch on
both stores alongside automatic submission.
