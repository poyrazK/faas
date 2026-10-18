# ADR-1264: Initial-failure policy-change previews

Status: Accepted (2026-10-12)

## Context

Automatic first recovery is available, but policy-change candidate lists describe
only parent-job actions. Operators cannot assess first-failure eligibility and
execution pressure through the same preview used to review a policy replacement.
The memory preview also omitted retained initial reviews from shared preparation
pressure, while PostgreSQL already included their charges.

## Decision

Add an optional initial_failures section to policy-change previews for preview
policies with the initial_failure trigger and automatic_initial proposals. Keep
failure identities separate from parent-job candidates; do not invent a job ID.
Expose metadata, policy and fresh safety eligibility, preparation decision,
automatic submission decision, diagnostic blockers and an advisory target only
for eligible failures.

Inspect at most the centralized WorkflowRecoveryPolicyJobsPerPoll retained
initial failures. Prioritize matching workflow and failure-status filters, then
ascending failure UUID, so excluded rows cannot hide matching rows in a bounded
sample. Fetch one additional metadata row to signal truncation. Coverage is always
partial; eligible, excluded and safety-blocked counts describe the sample rather
than the full failure population.

Share live diagnostics and unresolved-recovery checks with native admission.
Observe account-wide active recovery job quota, initial review retention quota,
shared preparation charges and cross-mode automatic execution charges. Model the
proposed next policy version: replacing a policy clears its pause, while retained
charges, cooldowns and unresolved recoveries remain. Predict preparation and
submission in sample order after sampled parent preparations, consuming local
simulation budgets and slots so the preview never promises multiple immediate
submissions. Snapshot pressure fields describe the state before simulation.

PostgreSQL uses the existing read-only repeatable-read preview transaction. Memory
uses the existing store lock. Do not prune history, refresh failure eligibility,
create reviews or jobs, advance cursors or schedule notifications. Count review
quota as if normal pruning had occurred without deleting records; unresolved
automatic submissions remain retained. Include initial preparation charges in
memory to match PostgreSQL. The HTTP layer adds runtime availability and blocks
predicted automatic submission when native runtime is disabled.

## Consequences

Operators can review first-recovery decisions through the existing CLI, API and
SDK policy preview. Samples remain advisory, bounded and potentially incomplete;
actual worker selection and admission repeat current checks. Previewing does not
require mutation permission and cannot grant execution authority. Existing
parent-job candidates and policy-change effects remain separate and compatible.
