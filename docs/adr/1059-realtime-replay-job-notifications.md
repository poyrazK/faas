# ADR 1059: Replay-job lifecycle notifications

Status: Accepted; migration and runtime validation deferred.

## Context

Background replay jobs recover failures gradually. Operators need to know when jobs finish or processing repeatedly fails without polling the inspector.

## Decision

Add opt-in app webhook events `realtime.replay_job.completed`, `realtime.replay_job.cancelled` and `realtime.replay_job.processing_failed`. An AFTER UPDATE job trigger queues terminal events only on state transitions, atomically with job progress/replay or cancellation. A processing warning is queued only when cumulative recorded processing errors first reach three. It is one warning per job, not consecutive errors or a terminal failure; the job remains running and retries. Ordinary per-item skips do not count as processing errors. Pause/resume, identical updates and creation emit nothing. Existing jobs already above the threshold are not backfilled.

Snapshot enabled matching app-scoped webhook recipients belonging to the endpoint's app/account at transition time, following other realtime notifications. No subscriber backfill and no account/platform fan-out. The outbox unique key `(event, source_id)` uses the immutable job UUID to prevent duplicate logical notifications. Each committed outbox event has a UUID event_id retained in its payload across dispatch retries. Receivers deduplicate event_id; webhook transport is at least once. Rollback rolls back the notification and associated progress. If no recipient matches at the transition, no event is queued.

Payload includes app/endpoint/group/job IDs, channel/group names, state, rate, selected/processed/replayed/skipped/remaining counts, cumulative error count, fixed safe error code, threshold, occurred_at and a relative inspector link. Counts describe the committed transition. Percent-encode every UTF-8 byte of variable URL components, avoiding assumptions about channel/group names. Job selections, individual outcomes, message payloads, worker identities and credential secrets are excluded. The OpenAPI documents the payload and both SDKs expose event choices and the payload model.

Use the existing signed app webhook dispatcher, retry schedules, dead-letter handling and delivery inspector/API. No new network sender or connection layer. Delivery order is not guaranteed; consumers should use event type and immutable job ID rather than assuming warnings precede terminal events. Inspector links can outlive retained job history or group deletion. Queued notifications survive job pruning because the outbox references account/app rather than job rows.

## Consequences

Migration `20261012102700536_realtime_replay_job_notifications.sql` follows the replay-job migration and adds the trigger plus outbox event vocabulary. Apply before accepting subscriptions and upgrade API replicas together. Downgrade removes the trigger but retains event vocabulary for queued deliveries/history; old binaries may reject new subscription choices. Runtime tests, migration application and UI rendering remain deferred at the user's request. Go/Node builds, Python syntax and static schema checks do not establish database execution evidence.
