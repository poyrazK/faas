# ADR 1041: Consumer-group dead-letter retention

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Keep dead snapshots indefinitely by default (`dead_letter_retention_seconds: 0`).
Opt in at creation or through the admin/MFA GET/PUT `dead-letter-retention` route,
using `retention_seconds: 3600..2592000` (one hour to 30 days). Ordered groups must
use the immutable `skip` dead-letter policy. Reject retention for ordered `block`
groups so cleanup cannot silently release blocked successors. Scoped worker
credentials cannot configure retention.

Persist the start of each dead state and a warning decision timestamp. Existing
dead snapshots start their clock when the migration runs. Changing the period
preserves the start but rearms warnings; identical PUTs preserve warning state.
Disabling cancels future cleanup, but does not retract queued webhook deliveries.
Replay clears both timestamps, and a subsequent failure starts a new period.
Exhausted expired leases remain nullable in dead-letter listings until a claim
operation materializes their dead state.

Sample eligible groups every 15 seconds, at most 64 per pass with a 20-second
budget. Persist an expiry warning and snapshot eligible signed webhook recipients
atomically. The `realtime.consumer_group.dead_letter_expiring` payload contains
IDs, channel/group, decimal sequence, attempts, failure code, nominal `expires_at`,
`cleanup_after`, and occurrence time; no message body or claim credentials.
Delete only after both nominal expiry and 300 seconds after the warning decision.
Outages or shortened policies therefore still grant a full five-minute grace.
Webhook delivery success does not gate deletion; retries may arrive after cleanup.
No recipients still means a persisted warning decision, with no subscriber backfill.

Group locks serialize configuration, replay, claims and cleanup. A replay that
commits first protects ready/leased work; cleanup that commits first makes replay
fail because the snapshot is gone. Pause and drain do not stop cleanup. Disabled
endpoints, deleted apps, ineligible plans and suspended accounts stop evaluation.
Lock contention, pass bounds or outages can delay deletion beyond its earliest time.

Expose nullable retention start, expiry, warning and cleanup dates on dead letters
and show upcoming expiry in the inspector. A cumulative decimal-string
`expired_dead_letters` group counter tracks removed failures; removal frees bounded
snapshot capacity, does not count as successful completion, and preserves fencing.

## Rollout and verification

Apply migration `20261012102700494_realtime_dead_letter_retention.sql` before
running the new server. Upgrade all replicas before enabling policies. Migration
rollback removes policy/timestamp/counter state and cannot restore deleted messages;
queued expiry webhook vocabulary is retained. OpenAPI and generated Node/Python
SDKs expose the account API and response fields. Builds and static checks are used
for this change; runtime tests, migration execution and inspector rendering are
deferred at the user's request.
