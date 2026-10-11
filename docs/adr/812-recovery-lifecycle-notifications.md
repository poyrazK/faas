# ADR-812: Durable recovery lifecycle notifications

Date: 2026-10-08
Status: Accepted

## Context

Recovery health alerts identify stalled or expiring jobs, while completion still requires polling. Operators need durable notifications for the final admission outcome without coupling recovery progress to webhook HTTP delivery.

## Decision

Add app webhook events `event_recovery.completed`, `event_recovery.cancelled`, and `event_recovery.expired`. Producers enqueue into the existing app_webhook_event_outbox within the same PostgreSQL transaction as terminal job state, item changes, pacing and applicable audit history. Memory storage captures the same event under its existing mutex. Failed transactions expose neither the terminal transition nor its notification.

Emit completion when the final pending item is queued or skipped, when the worker completes a running job with no pending items, and when an empty selection is created already completed. Emit cancellation only on an effective active-to-cancelled transition. Automatic expiry, including cleanup discovered by a control request, emits expired rather than cancelled. Both expiry and cancellation retain the stored state cancelled. Pauses, resumes, rate changes, repeated cancellation of terminal jobs and historical terminal jobs do not produce lifecycle notifications.

Freeze a metadata-only payload containing a stable event_id, job/app IDs, routing/execution mode, state, outcome, final admission counts, creation/expiry/terminal timestamps. Counts partition the frozen selection and pending is zero. No application payloads, errors, operator reasons, actor credentials, execution observations or work keys are included. Completed means admission finished; admitted routing retries and handler executions can continue or fail afterwards. The receiver can inspect recovery status/items for their current execution outcomes.

Derive event_id as UUIDv5 in the URL namespace over `gregale:event-recovery:<job-id>:<outcome>`. Use it as the outbox ID, with job ID as source_id. Recipient capture selects matching enabled app-scoped webhooks for the same account/app at terminal transition; empty filters include these lifecycle events. Account release and platform tenant receivers are not selected. If no recipient matches, no outbox row is created and later subscriptions do not receive a retrospective event.

Keep the existing terminal transition guards and per-job locks. Outbox uniqueness on event/source_id protects transaction retries while an event is pending. Once relay deletes it, the immutable terminal state prevents no-op controls or workers from recreating it. Memory storage similarly marks terminal capture even when there were no recipients, preventing recapture after relay or later hook creation.

The existing relay atomically creates one delivery per captured receiver and deletes the outbox row. Its source-event/receiver uniqueness protects fan-out retries. The existing dispatcher owns signing, JSON/CloudEvents formatting, independent retry budgets, dead letters and operator retry. Notifications are at least once, with no added cross-job or receiver ordering guarantee. Payload event_id is shared across receivers and retries; receiver delivery IDs remain distinct. Dedupe business effects before acknowledging receipt.

Append a migration extending the outbox event constraint without adding tables or columns. Keep the expanded event vocabulary on rollback so already committed notifications remain deliverable, matching earlier lifecycle outbox migrations. The clone schema registry requires no change. Update public webhook filter vocabularies and API/SDK payload models.

## Consequences

Existing wildcard app receivers receive these events after rollout. Explicit filters can select the three events independently. Existing webhook creation, inspection, attempts, dead-letter and retry APIs/CLI commands are sufficient; no recovery-specific subscription API is added. No receivers are created and no network notifications are sent during implementation. Persisted notifications and delivery payloads follow existing webhook retention, independently of recovery job retention.
