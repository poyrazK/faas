# ADR-1263: Opt-in automatic first recovery

Status: Accepted (2026-10-12)

## Context

ADR-1262 discovers and prepares durable first-recovery reviews without execution.
Operators still submit each captured failure individually. Automatic first
recovery needs independent execution authority and retained outcome proof.

## Decision

Add the explicit automatic_initial policy mode. It requires initial_failure as
its only trigger, explicit workflow and failed/dead filters, an hourly execution
budget and a consecutive-failure pause threshold. Enabling it through HTTP also
requires the native workflow runtime. Preview remains execution-free and
saved-failure automatic_retry retains its existing trigger and behavior.

The existing policy scan prepares initial reviews. The existing automatic worker
submits the oldest prepared review using the same transaction and fresh checks as
operator submission. Hold account authority before policy authority; repeat
version, expiry, evidence, safe-resume, ownership, capacity and native quota
checks. Each submitted review becomes one durable native job with its review UUID
as request ID. Concurrent polls and repeated requests cannot create or charge a
second job. Native pending admission repeats policy and expiry checks and refuses
an automatic review if the policy is paused.

Automatic first job creation charges a separate execution budget. Count automatic
initial submissions and saved-failure execution charges together across versions
and automatic modes, preserving cooldowns when policy authority changes. Preview
manual submissions keep their previous action-budget semantics. Preparation
continues to share the existing action budget and cooldown with parent reviews.

Allow only one unresolved automatic first recovery per app. Initial and existing
saved-failure automatic outcomes, pending native recovery jobs (including paused
jobs), and running or unproven retained native recoveries block new automatic
first submission. A policy update cannot release an unresolved automatic slot.

Store worker evaluations and a final automatic_result on the initial review.
Only saved terminal proof of the exact generation-one resume or final native
admission refusal can close the slot. Missing, unsaved or superseded evidence
remains outstanding. Unresolved initial submissions survive normal 30-day review
pruning; confirmed results become eligible for normal retention again. The
worker also observes outstanding first recoveries when switching to
saved-failure automatic_retry, preventing overlapping automatic modes.

Reuse consecutive failure counters, explicit pause/resume controls and
application-scoped automation.recovery_policy_paused notifications. Only results
from the current policy version change its failure counter. A policy update
explicitly clears the pause without refunding charges or removing unresolved
reviews. Queue snapshots include initial review IDs, worker waiting reasons,
budgets and unresolved slots. Suppression of a pending initial queue entry is a
version-fenced durable cancellation; applied entries cannot be suppressed.

## Consequences

Customers can opt into bounded first recovery without inventing a parent job or
bypassing native admission. Prepared reviews remain inspectable and cancellable.
Automatic first recovery performs only the generation-zero to generation-one
transition; further retries require separate saved-failure policy authority.
Unknown outcomes deliberately block further automation and require investigation.
Existing native completion notifications report execution results; there is no
new duplicate completion event. Existing health incident monitors consume the combined queue, including first
recoveries. Dedicated blocked-evidence notifications remain focused on
saved-failure retry actions. First-recovery waiting reasons are available in
durable reviews and queue snapshots. Each automatic first-recovery pause cycle
gets a distinct deterministic event ID after operator resume.
