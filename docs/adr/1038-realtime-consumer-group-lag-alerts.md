# ADR 1038: Consumer-group lag alerts

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Store one account-admin-configured policy per immutable consumer-group UUID.
GET/PUT `.../groups/{group}/lag-alert` use app ownership and MFA; PUT requires
admin scope. Configure enabled status, unassigned backlog threshold (decimal string,
0–1 billion), oldest pending age (0–7 days), dead-letter threshold (0–128), and
cooldown (30 seconds–1 day, default 300 seconds). Zero disables a metric; an
enabled policy requires at least one enabled metric. Trigger when any enabled
threshold is met or exceeded and recover when all enabled thresholds clear.

The preview background evaluator runs every 15 seconds, scanning at most 64 due
policies per pass with a 20-second pass budget. Group locks precede policy locks;
SKIP LOCKED and a durable next-check time coordinate replicas. Check enabled
endpoints, live account/app status and realtime plan eligibility. Disabled policies
and unavailable endpoints/accounts are not evaluated. Backlog is the head's latest
sequence minus assignment cursor, including unrecovered retention gaps. Oldest
pending age uses original event timestamps from ready/leased snapshots and retained
unassigned events, excluding dead snapshots. Eligible exhausted expired leases
count in dead letters before a later claim materializes their dead transition.
Sampling is an observation, not a processing receipt or latency guarantee.

Persist observations and atomically enqueue `realtime.consumer_group.lag_exceeded`
on initial crossing, plus reminders while breached after cooldown, and
`realtime.consumer_group.lag_recovered` on recovery. Actual breach/recovery transitions
bypass reminder cooldown; rapidly flapping conditions can emit transitions more
frequently. Capture explicitly opted-in enabled app webhook recipients in the
existing outbox and use its signed delivery/retry ledger. Payloads contain group,
channel, metrics, thresholds and pause/drain status, not event bodies or credentials.
Webhook recipients deduplicate delivery IDs. Event-decision timestamps update even
when no receiver opted in; there is no historical delivery backfill.

Paused/draining groups remain monitored. Identical policy updates preserve state;
changed policies rearm and clear observations without a synthetic recovery event.
Disable silences evaluation and resets state; deleting a group cascades its policy.
Already queued webhook events retain their existing delivery lifecycle. Failures
roll back observation and enqueue together, leaving the policy due for retry.
Inspection shows nullable observations and last event decision for the selected
group; generated Node/Python clients expose configuration/status operations.

## Deployment and validation

Apply `20261012102700487_realtime_consumer_group_lag_alerts.sql` after group/drain
and webhook-outbox migrations before activation. Scan throughput is bounded: busy
fleets, lock contention and outages can delay evaluation beyond one interval. Go
and Node builds and static specification/generated Python checks are the validation
scope; runtime, migration and inspector-rendering checks remain deferred by request.
