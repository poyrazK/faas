# ADR-1490: Application issue event-frequency alert rules

Date: 2026-10-15

Status: Accepted

## Context

Customer-impact alerts use verified customers and a fixed 24-hour crossing.
Operators also need short-window occurrence thresholds, environment filters,
and bounded repeated notifications for sustained application errors. Tracker-sync
incident reminders do not monitor application error frequency.

## Decision

Add up to ten app-level named frequency rules. Each rule selects all environments
or an exact environment, a threshold of 1–100,000 events, a rolling 60–86,400-second
window and a 60–604,800-second cooldown. Bounds live in `pkg/api/limits.go`.
Existing Issues plan entitlement, app permissions and deployment-write/MFA/CSRF
gates apply. The API replaces the complete rule set with a required expected
revision; dashboard forms edit one rule using the same comparison. Stale writes
return 409. No-op writes preserve the revision and cooldown receipts.

Evaluate only on new accepted issue captures, under the existing app lock and
inside its transaction. Count retained occurrences in the current canonical group
in `(now-window, now]` by occurrence time. Duplicate ingestion, old/future-dated
captures outside that window, attribution enrichment, lifecycle changes and
configuration writes cannot trigger alerts. A matching capture fires when the
count is at least the threshold and the rule/issue cooldown has elapsed. Sustained
failures can alert again on the first qualifying capture after cooldown; there is
no timer-driven repeat, recovery notification, or automatic lifecycle mutation.

Suppress evaluation while the canonical issue's ignore period remains active;
collection continues. After reopening/expiry, the next matching capture can alert
if above threshold and outside cooldown. Suppressed occurrences are not replayed
as individual notifications. Frequency alerts differ deliberately from the existing
customer-impact crossing semantics.

Persist a semantic rule hash and last-alert timestamp per rule/current issue.
Unchanged rules, reordering and disable/re-enable preserve cooldowns. Changing
threshold, environment, window or cooldown starts a new cycle on the next matching
capture. Removing or semantically changing a rule removes its old receipts. Evaluate the maximum matching receipt
across merged members to prevent a merge bypassing cooldown. After a split, counts
and cooldowns follow the resulting current group identities; no notification is
created by regrouping itself. Receipts are bounded by rules times retained issue
identities, and cascade on policy/app/issue deletion.

Create `frequency_threshold_reached` activity, snapshot webhook recipients, and
record cooldown in the same capture transaction. A failed activity/outbox/receipt
write rolls back the capture and permits a clean retry. Use the existing durable
signed webhook fanout/retry ledger with `issue.frequency_threshold_reached` and
safe rule/count/window/environment/deployment metadata. No network I/O occurs in
ingestion. Delivery remains at least once and receivers deduplicate transition IDs.
Native application-error Slack delivery remains a separate increment.

## Consequences

At most ten bounded indexed occurrence counts are added to ingestion. Counts
reflect accepted retained telemetry, not unsampled traffic volume or unique users.
Late events inside a window may contribute; sampled, rejected and expired events
do not. The JSON policy is validated at both API and state boundaries; optimistic
revision plus the app lock serializes rule changes and capture evaluation. No new
scheduler, metric aggregation service or per-occurrence unbounded state is added.
