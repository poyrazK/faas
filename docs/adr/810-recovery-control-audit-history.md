# ADR-810: Recovery control audit history

Date: 2026-10-08
Status: Accepted

## Context

Recovery jobs can be discovered, paused, resumed, rate-adjusted and cancelled, but operators cannot identify the credential that changed a job or reconstruct its control decisions.

## Decision

Append a job-local audit trail for creation, effective pause/resume/rate changes, cancellation and automatic expiry. Each entry captures a monotonically ordered ID, timestamp, action, server-resolved actor kind and ID, optional operator reason, previous/new admission state and previous/new rate. Creation has no previous state and previous rate zero; an empty selection can be created already completed. This is control history, not a handler attempt or natural-completion history.

Resolve account or API key identity from authenticated middleware context, never client actor fields. Store callers outside HTTP receive an explicit internal/store actor. Expiry always uses system/recovery_expiry, even when a user control detects expiry and returns 409 after committing cleanup. API key IDs identify credentials, not necessarily individual humans; no emails, key labels or credential values are recorded.

Store reasons only in audit rows, separately from the frozen selection. Creation and rate requests accept optional `reason`; pause/resume/cancel accept an optional JSON control body. Existing empty-body clients continue working. Reasons are at most 512 UTF-8 bytes and cannot contain control characters. Read-only preview does not record reasons. Actor fields are not accepted in request bodies.

Insert PostgreSQL history in the same transaction as the action, under the existing per-job serialization lock. Cancellation and expiry capture the old row before updating it. Memory storage appends under the same mutex. Failed or rolled-back actions leave no entry, except the documented expiry cleanup that commits before returning an expired-control conflict. Repeated controls that change nothing leave no duplicate entry. HTTP idempotency protects creation as before.

GET `/v1/event-recoveries/{jobID}/history` verifies owned job/application and requires existing apps-read/admin scopes and MFA. Return oldest-first pages of at most 100 metadata-only entries. `after` is an exclusive entry ID, with `next_after` only when more rows exist. IDs are navigation keys and may have gaps. No execution observations or payloads are joined. PostgreSQL reads metadata and history in a repeatable-read transaction. Pages across requests remain live.

History follows job retention and cascades when the job is pruned or deleted. Existing jobs have no fabricated creation or past-control entries; only subsequent effective actions are recorded. The append-only migration creates the history table and job/ID index. Register every column as operational in the project environment clone schema registry. Rollback drops the history table and its data.

## Consequences

Operators can attribute controls and review their reasons without conflating delivery admission with handler outcome. Retention bounds how far back this trail can be inspected; it is not a permanent compliance log. API, Go/Node/Python SDKs, and `events recovery-history` expose the same contract. Add `--reason` to creation and write controls.
