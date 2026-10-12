# ADR-1016: Automatic retry queue evaluation evidence

## Decision

Record the last committed execution-worker evaluation on each inspected automatic retry intent. Store its evaluation time, reason, and the first observation of the current consecutive wait reason. Preserve that wait start across repeated checks and restarts; reset it when the reason changes. Creation, proven final-result capture and selected-intent worker suppression clear the wait start. Operator controls, policy replacement and notification capture preserve this historical evidence. Existing intents begin without an evaluation; migration does not infer earlier worker observations.

Expose the evaluation on retained review actions and queue entries. Queue entries include the frozen review preparation timestamp and whole nonnegative seconds since preparation, relative to the queue snapshot's observed time. Include waiting age only when a worker wait has been recorded. Clamp future timestamps to zero age for clock skew. Ages measure retained intent age and an observed wait, not continuous proof of a block or running time.

The worker records budget, cooldown, quota and missing-evidence waits when evaluating the selected pending intent. When an unresolved applied child occupies the serial slot, it records admission wait, execution wait, running, unknown evidence or inspection error on that child and an outstanding-retry wait on the next current-version pending intent. Other pending intents remain unevaluated until selected. Saved final proof remains the only authority to complete an applied intent or credit a failure. Evaluation writes participate in the existing execution transaction and memory fences; they never create a child, refund a charge or release a slot.

Queue reads remain read-only and cannot advance evaluations. A paused or disabled policy retains the last observation, and reason changes may occur between polls. The CLI shows observation time so operators can recognize stale information. The policy's existing last-evaluation timestamp continues to describe plan preparation, separate from per-action execution evaluation.

## Consequences

Operators can distinguish old queued work, persistent waits, uninspected intents and stale observations before suppressing a retry or changing policy. Evaluation evidence follows retained review/action lifetime. Additive migration rollback refuses to discard recorded evaluations. No new polling frequency, quotas or execution authority are introduced.
