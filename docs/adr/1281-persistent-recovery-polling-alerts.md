# ADR-1281: Persistent recovery polling alerts

Date: 2026-10-13

## Context

Manual and automatic polling defer recognized deterministic errors independently.
Operators otherwise need to inspect reviews or policies to discover persistent
monitoring failures and determine whether polling resumed.

## Decision

Add `automation.recovery_polling_status` with `failed` and `recovered` states.
Centralize the initial alert threshold at three consecutive failures. With the
existing backoff, failure observations normally reach this threshold after three
minutes. Until then, failures remain available through the existing metadata.
After alerting, unchanged error code and policy version remain quiet despite
attempt-count or retry-time changes. A changed code or policy version can capture
a fresh failed transition once the threshold is met.

Store the last notification separately from error metadata: on manual initial
reviews, in review JSON; on automatic policies, in a nullable checked column.
Preserve the policy receipt across updates, pauses, resumes and mode changes.
Only a committed successful poll emits recovered. Intent updates can clear the
error metadata but never prove monitoring recovered. An idle successful poll can
recover monitoring without a new execution or final result. `monitoring_only`
is always true; there is no claim of workflow success in this notification.

Stable event identity derives from app, scope, manual review and persisted
sequence. Freeze the safe code, app, version, attempts, observed time, retry time,
inspection URL and enabled app-level receivers at capture. `no_receivers` is a
captured transition and never replays to later subscribers. New transitions use
current receivers. Durable `polling_failed`/`polling_recovered` history retains
the full receipt and event ID independently of source pruning.

Capture failure deferral or successful polling, last receipt, history and outbox
in the same PostgreSQL transaction. Notification capture failures propagate;
they cannot silently commit an alert-less deferral or fake recovered transition.
Memory automatic rollback includes notification, history and outbox state. No
new scheduler pass, execution authority, budget charge or delivery implementation
is introduced. Existing signed webhook delivery and replay remain authoritative.

## Consequences

Apply migration `20261014092444767` before deploying. Downgrade refuses to remove
retained receipts, history, outbox or delivery evidence. The status describes
capture, not successful delivery, and inspection does not refresh it. Both stores
cover threshold, quiet repeats, no receivers, late subscribers, restart and
monitoring recovery without a workflow result. PostgreSQL late receipt failure
coverage verifies atomic rollback of failure clock, alert history and outbox.
