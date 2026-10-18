# ADR-1275: Observe manually approved initial recovery completion

Date: 2026-10-13

## Context

Preview-mode initial reviews record preparation and approval, but their final
outcome was absent from the shared recovery history. Automatic policy execution
cannot own this observation: operators may disable, change or pause the policy
after approving a job.

## Decision

The existing scheduler recovery poll first observes one due submitted review
whose frozen policy mode is preview. This step grants no execution authority.
A per-review next-observation timestamp, indexed for unresolved manual reviews,
uses the existing policy poll interval and persists across restart. Account,
policy and review locks follow the manual decision lock order. Deleted apps are
excluded. Observation and history append share one transaction.

Reuse the exact initial job/run/generation-one evidence classifier. Saved native
execution results and verified terminal admission refusal may complete the
review; missing, superseded or unpersisted proof remains unresolved. Store the
verified outcome in the optional `result` field, preserving `automatic_result`
for automatic-initial execution. Append one durable `initial_recovery_result`
with the frozen version, job/run links and system observer actor. Replay and
reads do not append events. Submitted reviews without either result survive
normal review pruning and remain accessible to operators.

Manual observation ignores current policy mode, enabled state and automatic
pause. It does not submit jobs, capture automatic alerts, charge automatic
budgets, change consecutive failures or grant retry authority. The automatic
queue retains its automatic-only scope. Existing automatic result behavior is
unchanged; its observation while paused remains outside this increment.

## Consequences

Operators can inspect manually approved completion in the review, CLI and shared
history. Unresolved manual reviews consume existing retained-review capacity
until evidence resolves. This increment does not broaden evidence repair
eligibility beyond automatic-initial reviews. Missing manual proof therefore
remains unresolved; it must never be inferred from an unrelated generation.
