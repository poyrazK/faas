# ADR-1261: Durable first-recovery reviews

Status: Accepted (2026-10-12)

## Context

ADR-1259 provides reviewed first recovery and ADR-1260 adds candidate-policy dry
runs. Local plans have no saved policy authority, review lifecycle or submission
budget reservation. Existing recovery-policy reviews require a parent job that
initial failures do not have.

## Decision

Persist initial reviews separately, keyed deterministically by app, captured
failure and saved policy version. Preparation requires an enabled preview policy
with explicit initial_failure trigger, workflow and failure-status filters, and
fresh safe-resume diagnostics. Preserve the evidence-linked generation-zero
target, complete policy snapshot and actor-attributed transition history.

Reviews expire after the centralized 24-hour native-job lifetime. Retain them for
the existing 30-day recovery history window, with the existing 1000-review limit
applied to the initial-review pool. GET/list reads derive freshness without
writing. Decisions persist cancellation, expiry or stale evidence dispositions;
terminal decisions do not permit another submission. Repeated preparation returns
the same retained review and repeated submission returns the same job.

Preparation shares the existing policy hourly-action budget and app cooldown with
parent-job reviews. Explicit first-recovery submission independently reserves the
same hourly-action budget and cooldown across initial submissions, including old
policy versions. Cancellation and unsuccessful admission do not refund charges.
This does not introduce automatic-execution authority or an execution-budget field
into preview policies.

In PostgreSQL, initial review mutations acquire the existing account recovery
lock, then the app scope and policy authority. Policy updates and preparation
workers fence authority through the policy row. Submission rechecks live evidence,
filters, safe resume and pressure, and atomically creates the native job, reserves
budget and records the transition. An error rolls back all three. Memory mirrors
this under its store lock. All generation-zero native run-job creations reject a
second pending job for the same app/run, after stable request-id lookup. Direct
manual resume races remain fenced atomically by the native generation check.

Native job preflight and pending admission check review expiry and saved policy
version again. Admission locks policy authority through the resume transaction;
capacity, ownership, safety and generation are still enforced by native resume.
Native queue claims use NO KEY UPDATE so parent-review foreign-key checks can
finish while holding policy authority; concurrent native claims remain mutually
exclusive. This prevents the job/policy lock inversion from causing a deadlock.
Later policy changes do not cancel runs already admitted. Submitted review state
is historical; job completion and terminal notifications use the native job APIs.

Capture ready notifications atomically through automation.recovery_review_ready,
with initial_failure_id instead of a parent job_id. Existing parent-review
payloads retain job_id. Delivery rechecks live review freshness and captured
receiver/payload identity. Cancellation, stale evidence or policy changes suppress
ready delivery. No receiver backfill or automatic submission is introduced.

## Consequences

Operators can inspect, paginate, submit and cancel durable first-recovery reviews
without exporting local authorization files. Durable transition history is on the
review; admitted-job history and completion receipts remain on the native job.
Expiry reads may show an inferred disposition before a mutation records its audit
transition. A terminal or expired review cannot be renewed under the same policy
version; changing the saved policy version creates a new review subject to retained
budgets. Automatic first recovery and worker-driven preparation remain future work.
