# Async deadline count watches in Python

The Python SDK exports `awatch_event_subscription_deadlines(client, target, options)`.
It reads the existing complete deadline-window endpoint. It does not sample counts,
change subscription controls, replay deliveries, create files or send notifications.
Use the SDK's authenticated generated client, normally `client.inner` from a
configured `FaaSClient`.

```python
from faas_sdk import (
    EventDeadlineTarget,
    EventDeadlineWatchOptions,
    awatch_event_subscription_deadlines,
    validate_event_deadline_checkpoint,
)

async def on_progress(progress):
    # Application-owned functions: persist atomically before attempting delivery.
    await save_checkpoint(progress["checkpoint"])
    await deliver_alerts_idempotently(progress["alerts"])

async def poll_acknowledgements(checkpoint):
    return await read_acknowledgements()

result = await awatch_event_subscription_deadlines(
    client.inner,
    EventDeadlineTarget(
        app="orders",
        app_id="11111111-1111-4111-8111-111111111111",
        subscription_id="22222222-2222-4222-8222-222222222222",
    ),
    EventDeadlineWatchOptions(
        within_ms=300000,
        upcoming=50,
        upcoming_recovery=20,
        expired=1,
        expired_recovery=0,
        upcoming_reasons={"subscription_paused": 10},
        upcoming_reasons_recovery={"subscription_paused": 2},
        sustain_ms=60000,
        recovery_sustain_ms=120000,
        interval_ms=5000,
        timeout_ms=600000,
        max_age_ms=30000,
        # resume=validate_event_deadline_checkpoint(saved_json),
        on_progress=on_progress,
        poll_acknowledgements=poll_acknowledgements,
    ),
)
await save_checkpoint(result["checkpoint"])
```

Persistence, notification and acknowledgment reader functions in the example are
application-owned. Callbacks must be async; they are awaited serially and receive
detached dictionaries. Run one writer per checkpoint and serialize offline changes
with any active watcher. Reuse the async client in its owning event loop; this is
an async helper rather than a synchronous wrapper around `asyncio.run`.

## Counts and confirmation

Supply the application slug plus expected stable application and subscription UUIDs
as strings. UUIDs are canonicalized to lowercase, and every response must match.
`within_ms` is mandatory: integer milliseconds 100..86400000. The horizon applies
only to upcoming deadlines; expired counts cover all deadlines at or before the
observation. At least one positive total or reason warning is required.

`upcoming` and `expired` configure total warnings. `upcoming_reasons` and
`expired_reasons` select independent warnings for known labels, listed in
`EVENT_DEADLINE_REASONS`. Zero disables a warning; unknown labels are rejected even
with zero. Counts must be integers 0..9007199254740991; booleans and floats are
rejected, despite Python's bool/int relationship and unbounded integer support.

Optional `upcoming_recovery` and `expired_recovery` configure inclusive total
recovery. `upcoming_reasons_recovery` and `expired_reasons_recovery` configure
inclusive boundaries for selected reasons. Omitted settings default to warning
minus one; explicit zero requires zero. Recovery must be strictly below its enabled
warning in the same cohort/reason. Total and reason settings do not inherit from
each other. The helper normalizes option maps into detached deterministic rules
before its first await.

Opening and recovery each require at least two fresh, advancing observations
spanning `sustain_ms` or `recovery_sustain_ms`, both defaulting independently to
60000. With 50/20, an incident opens at >=50, remains firing at 21..49, and resolves
only after sustained observations at <=20. Leaving a candidate band resets its
timer. After confirmation is lost, fresh warning proof reestablishes the existing
incident without another opening. Middle-band observations leave resumed incidents
unknown until warning or recovery is freshly confirmed.

The evidence validator requires complete read-only coverage, matching scope and
horizon, exact known-reason partitions, consistent counters/deadlines/control
metadata and fresh timestamps. It validates original JSON timestamps at nanosecond
precision instead of relying on generated model datetimes rounded to microseconds.
Local wall time establishes freshness; monotonic time measures confirmation and
gaps. Missing, invalid, stale, future, repeated or regressing evidence and sample
gaps cannot open or resolve incidents. Failed or unsupported reads stay unknown,
without a sampled fallback. Counts above the preview's 1000-row cap remain covered.

Progress contains `confirmations`, condition state/boundaries in `checkpoint`, and
valid response evidence when available. Diagnostics explain fresh sample counts,
elapsed/remaining confirmation, resume waits, unknown evidence and notification
capacity blockage. Remaining duration is a snapshot; time alone does not confirm
anything. Counts on unknown conditions are historical. `evidence_status` describes
response validity/freshness; individual confirmations additionally enforce advancing
observations and local gaps. Unknown evidence is a progress diagnostic, not an
opened/resolved lifecycle notification.

## Timing, cancellation and callback errors

`interval_ms` defaults to 5000 and accepts 100..60000. `timeout_ms` defaults to
600000, `max_age_ms` to 30000, and `request_timeout_ms` to 10000. These three and
confirmation durations accept integer milliseconds 100..86400000. Normal timeout
returns a result with `stop_reason="timed_out"`. Individual reads are bounded by
the smaller of the remaining watch time and request timeout. Sleeps are bounded
by remaining time too. Callbacks are awaited and may extend the timeout; the SDK
does not abandon an in-progress persistence callback in the background.

Cancel the asyncio task to interrupt a pending read or sleep. Cancellation raises
`EventDeadlineWatchCancelled`, a subclass of `asyncio.CancelledError`, with a
detached `.result` and `stop_reason="interrupted"`. Persist its checkpoint using
your application's cancellation-safe shutdown path, then re-raise if appropriate:

```python
from faas_sdk import EventDeadlineWatchCancelled

try:
    result = await awatch_event_subscription_deadlines(client.inner, target, options)
except EventDeadlineWatchCancelled as interrupted:
    await persist_during_shutdown(interrupted.result["checkpoint"])
    raise
```

Task cancellation can interrupt an application callback too; preserve the latest
checkpoint and replay pending alerts after restart. If either callback raises an
ordinary exception, `EventDeadlineWatchCallbackError` exposes `.result` with
`stop_reason="callback_failure"` and retains the cause. No callback mutation can
alter watcher state. The helper does not assume notification succeeded merely
because `on_progress` returned.

## Checkpoints and alert acknowledgment

Python uses kind `gregale.python.event-deadline-watch`, version 1. It is a distinct
format: **Go/CLI and Node checkpoints are not accepted**. Checkpoints contain JSON
primitives, effective target/policy, historical condition values, session/sequence,
incident identities, and pending/acknowledged notifications. Timer/sample proof is
excluded. Validate loaded JSON with `validate_event_deadline_checkpoint`.

Resume requires the same application/subscription, horizon, enabled warnings,
effective recovery counts and confirmation durations. Omission and explicit default
recovery are equivalent, and map insertion order is irrelevant. Poll timing can
change. Resume resets live proof and condition state to unknown; saved observations
and downtime do not establish fresh confirmation.

`alerts` contains transitions created by the latest observation, while
`checkpoint["pending"]` retains all unacknowledged `opened` and `resolved` alerts.
Persist a checkpoint before publishing notifications. After a crash, replay pending
alerts with downstream deduplication by session/incident/sequence. Notification
delivery is at least once. The helper has no external notification transport.

Acknowledge exact `{"incident_id": ..., "sequence": ...}` pairs through
`poll_acknowledgements` or offline
`acknowledge_event_deadline_alerts(checkpoint, pairs)`, then persist the returned
checkpoint. Validation is all-or-nothing. Duplicate pairs within a batch, mismatched
identities and unknown sequences are rejected. Repeating an acknowledgment is
idempotent while its receipt remains among the latest 1024 acknowledged alerts;
evicted receipts are rejected. A resolution requires the opening to be acknowledged
first or in the same batch.

Pending alerts plus reserved closing slots are bounded at 1024. Active incidents
retain one slot to close. Capacity or safe-integer sequence exhaustion blocks new
openings with `notification_blocked` progress, requiring continued fresh proof.
Acknowledgments free slots. After draining incidents at sequence exhaustion, start
a new checkpoint. Do not place authentication secrets or event payloads in saved
checkpoints.

These are rolling aggregate counts. Reason changes, expiry, retention, overrides
or successful completion can lower them. Resolution does not certify business
completion. See [deadline coverage and triage](event-subscription-deadline-window.md).
