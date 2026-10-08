# ADR-734: Recovery capacity diagnostics

Date: 2026-10-08
Status: Accepted

## Context

Recovery health distinguishes capacity waits from scheduler stalls but discards the admission error's scope. Operators cannot tell whether an account, application or captured consumer limit is blocking the current item, or distinguish a brief wait from prolonged contention.

## Decision

Preserve the typed EventDeliveryCapacityError scope when an execution replay is deferred. Actual admission gates report account, app or consumer pending-delivery limits; execution lane locks are not falsely labelled as capacity limits. Untyped capacity exhaustion is recorded as unknown. Do not store error strings, payloads, invocation input, resolved work keys or numeric limits inferred after the observation.

Persist capacity_scope, capacity_wait_started_at and capacity_wait_observed_at on the job in the same outer transaction as its pending retry schedule and spent permit. The replay savepoint still rolls back admission side effects. Repeated capacity observations retain the episode start and refresh scope and observation time, even when the blocking scope changes. Advancing the current item through admission or skip clears the episode. These diagnostics never become an item skip reason, nor do they advance last_progress_at.

Pause preserves the last observation for inspection but suppresses capacity alert counts. Effective resume clears the old wait episode so intentional pause time does not count toward a new capacity alert; repeated resume requests change nothing. Progress and non-capacity deferrals clear capacity fields. Memory storage follows the same rules under its mutex. Existing waits are left unknown until a fresh scheduler observation; historical start times are not guessed.

Extend recovery health with an optional capacity_wait object: scope, stable gate code, a safe explanation, start time, last observation time and age. next_attempt_at and eligible_at continue to expose scheduled and rate-adjusted retry times. Diagnostics describe the latest observed gate, not a live guarantee that it remains full. Paused jobs may retain old diagnostic age, while their health status remains paused.

Add current capacity_waiting_jobs and prolonged_capacity_wait_jobs counts. Count only running jobs whose status is capacity_wait and whose observation is within the five-minute freshness grace. A prolonged episode lasts at least fifteen minutes. Paused, terminal, expired and stalled jobs are excluded; stale capacity observations never masquerade as fresh contention. Existing scheduler-stall and expiry alerts remain independent.

Add app-scoped webhook-only alert metric event_recovery_capacity_wait_jobs for the prolonged count. Existing comparisons, windows, cooldowns, recovery notifications and degraded-read handling apply. A rule window does not aggregate these current observations. No rules or external notifications are created automatically.

Append a migration adding the fields, a scope vocabulary and all-or-none observation constraint, and extending alert metric constraints. Register all three columns as operational in the clone schema registry. Rollback requires removing capacity alert rules, restores prior alert vocabularies, and drops the diagnostic columns.

## Consequences

The existing recovery-health API, CLI and SDKs surface actionable pending-delivery contention before expiry. Fifteen minutes is a centralized limit in pkg/api/limits.go. This covers execution-recovery admission waits; routing recovery admission does not reserve execution slots and does not fabricate capacity diagnostics. Receiver HTTP delivery and handler execution capacity remain separate concerns.
