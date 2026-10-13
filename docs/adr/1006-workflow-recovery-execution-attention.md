# 939: Native recovery execution attention

Native recovery admission can finish before execution does. Operators need an advisory signal for an admitted generation that stops making observable progress or exceeds its execution deadline.

Plans may set `execution_deadline_seconds` (zero disables it) and `execution_stall_seconds` (zero defaults to 900 seconds). Nonzero thresholds range from 60 seconds to seven days. Gregale exposes `--execution-deadline` and `--execution-stall` on `automations recovery-start`; durations must contain whole seconds. Settings participate in the frozen request idempotency comparison.

Monitoring observes only the generation admitted by the job, including while admission is pending or paused. Retained terminal results take precedence. Missing or superseded generation evidence remains unknown. Last progress uses the run update timestamp; lease extensions do not count as progress. Scheduled waits, parked waits, retry backoff and capacity waits suppress stalled alerts. Deadlines include these waits and preserve the underlying reason.

A bounded scheduler poll captures one `event_recovery.execution_attention` event per job. The stable event ID, receiver snapshot, receipt and outbox write share a transaction. An empty receiver selection is also final. Existing per-job notification reports, delivery acknowledgement and explicit notification retries apply. Aggregate notification health counters remain scoped to admission and completion notices. Completion remains a separate event. Detection never cancels, resumes or retries work.

Health includes active native recovery jobs and distinguishes stalled, deadline-exceeded, capacity and scheduled waits. Before admission completes, the existing health `completed_at` field and wait age use job creation as their baseline. Run and diagnostics links identify affected executions. Underlying wait counts may overlap deadline counts.

The migration adds an indexed poll timestamp. Rollback refuses to remove monitoring while captured attention receipts or attention outbox events remain retained.
