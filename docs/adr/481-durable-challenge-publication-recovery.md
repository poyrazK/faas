# ADR-481: Durable challenge-publication recovery

Status: accepted

Date: 2026-10-03

## Context

The post-readiness hosting verifier publishes an app-and-candidate-bound secret
through Postgres before requesting the candidate's public route. A publication
error means no request reached the app. Treating that error as an application
smoke failure discards a ready candidate during a temporary platform outage.
ADR-434 already makes interrupted verification and failed evidence writes
replayable through the durable snapshot-written notification.

## Decision

Classify challenge-publication errors with a typed, retryable platform error.
Keep the candidate snapshotting and its predecessor serving. Return the error
to the existing notification outbox; add no queue or public deployment state.
Publisher diagnostics can contain the secret, so the returned error and smoke
result use fixed safe text. The underlying cause is available for errors.Is.
Missing publisher configuration remains a terminal configuration failure.

Persist progress under stage_state.hosting_verification: started_at,
deadline_at, attempts, last_error_code, retry_not_before and completed_at.
Initialize the five-minute operational window before the first configured
probe and retain it across consumer restarts and redelivery. Count actual
verification attempts, including interrupted attempts, rather than notification
deliveries. An early redelivery does not increment attempts. Record the existing
outbox's exponential backoff as a retry eligibility floor, clamped to the
deadline; polling, queue load and outbox retries can deliver later.

Postgres row locks and the memory-store mutex guard progress changes. Retry
and completion updates must match the current attempt. Live or terminal rows
reject stale updates. Existing stage transitions retain this additive progress;
preparing a new rollback activation starts a fresh window. Activation remains
serialized by the existing per-deployment lock.

Bound publication by the verifier's overall timeout, request timeout, and on
recovery the persisted deadline. A successful publication restores the ordinary
HTTP probe budget; its recovery deadline cannot truncate an app-health check.
Consumer cancellation remains replayable. Application response and transport
verdicts retain their existing classification in this first slice.

When publication cannot recover within the persisted window, atomically commit
the failed receipt and terminal state using ADR-434. Expose deployment error
deployment_verification_unavailable and smoke error smoke_verification_unavailable,
distinct from deployment_smoke_failed. Keep the predecessor serving and emit a
failure event only after commit. A failed progress or verdict write remains
replayable. Completed_at records a completed attempt, including negative health
verdicts; the receipt and deployment status remain the verification outcome.

## Consequences

Temporary publication outages can recover the same candidate without rebuilding
or asking the customer to redeploy. The existing deployment stage_state response
exposes recovery progress without a migration or receipt schema change. Two
progress writes accompany an ordinary configured probe. Existing outbox attempt
limits and dead-letter policy remain in force during prolonged database or
consumer failures; the five-minute deadline is evaluated on delivery, not by a
new timer service. Operators should recover dead letters using the existing
outbox procedure.

TestHostingPublicationRecoverySurvivesHandlerRestart exercises publication
failure followed by candidate proof and promotion after a new handler starts.
TestHostingPublicationDeadlineFinalizesPlatformFailureAtomically pins bounded
recovery, failure-commit replay and post-commit events.
TestHostingPublicationRecoveryPreservesAppHealthFailure retains the app verdict.
TestHostingVerificationProgressSurvivesReplayAndStageChanges and
TestHostingVerificationDeadlineCannotBeRenewed pin PostgreSQL/memory parity.
TestPg_HostingVerificationCommitOutageRemainsReplayable checks rollback on a
real PostgreSQL commit fault. Challenge-publication tests pin secret redaction,
cancellation and the separation of publication and candidate request budgets.
