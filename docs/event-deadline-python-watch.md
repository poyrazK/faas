# Async deadline count watches in Python

The Python SDK exports `awatch_event_subscription_deadlines(client, target, options)`.
It reads the existing complete deadline-window endpoint. It does not sample counts,
change subscription controls, replay deliveries, create files or send notifications.
Use the SDK's authenticated generated client, normally `client.inner` from a
configured `FaaSClient`.

Use [`ainvestigate_event_deadline_alert`](event-deadline-python-investigation.md)
with a saved Python checkpoint and exact incident/sequence pair to inspect current
matching recipient identities. It derives filters and reads one bounded page
without modifying checkpoints or delivery state.

```python
from faas_sdk.preview import (
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
observation. At least one positive count or expired-ratio warning is required.

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
from faas_sdk.preview import EventDeadlineWatchCancelled

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

Python uses kind `gregale.python.event-deadline-watch`. Count-only selections use
version 1; selections containing an expired ratio use version 2, as described below.
Both are distinct formats: **Go/CLI and Node checkpoints are not accepted**. Checkpoints contain JSON
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

## Expired backlog ratio conditions

```python
from faas_sdk.preview import EventDeadlineRatioOptions, EventDeadlineWatchOptions

options = EventDeadlineWatchOptions(
    within_ms=300000,
    expired_ratio=EventDeadlineRatioOptions(
        warning_basis_points=1000,  # >=10%
        recovery_basis_points=500,  # <=5%
        minimum_recipients=100,
    ),
    sustain_ms=60000,
    recovery_sustain_ms=120000,
)
result = await awatch_event_subscription_deadlines(client.inner, target, options)
```

`expired_ratio` is an optional keyword-only policy setting, preserving existing
positional argument order. Its denominator is all retained captured pending
application recipients with effective delivery-age deadlines, and its numerator
is those already expired. Both come from one complete snapshot, independently of
the upcoming horizon. It is a backlog ratio, not a historical expiry percentage or
delivery success SLO. Ratio-only and combined count/ratio watches are supported.

`warning_basis_points` requires an integer 1..10000, where 100 basis points = 1%.
`recovery_basis_points` must be a nonnegative integer below warning. Omission/None
defaults to warning minus one basis point; explicit zero requires a zero numerator
in a qualifying cohort. `minimum_recipients` is required and must be an integer
1..9007199254740991. Boolean, float, negative and oversized option/sample counts
are rejected. `EVENT_DEADLINE_RATIO_BASIS_POINTS_MAX` represents 100%.

Rules are copied before the first await, and the ratio is appended after the
existing count conditions. Up to 31 conditions can be selected. All share existing
warning/recovery durations, complete partition validation, timestamp freshness and
one window read. No extra request, endpoint, generated API or migration is added.

A zero or below-minimum denominator resets ratio proof and reports
`insufficient_deadline_recipients`; other eligible count conditions continue
independently. Python retains an active incident identity while its evidence is
unknown and requires fresh warning or recovery confirmation when the cohort
qualifies again. An empty or small cohort never fabricates successful recovery.
This preserves Python/Node lifecycle behavior rather than adopting Go's closing
`confirmation_lost` notification. No timers or sample counts survive resume as live
proof.

### Exact comparisons and stored samples

Python integer cross-products decide the warning and recovery boundaries:

```
expired_recipients * 10000 >= warning_basis_points * deadline_recipients
expired_recipients * 10000 <= recovery_basis_points * deadline_recipients
```

Counts are bounded safe integers, but the products use Python's exact integers.
Ratio conditions and alerts retain JSON-safe
`ratio_sample = {"expired_recipients": N, "deadline_recipients": D}`. Their `value`
is an approximate basis-point display projection, which may be fractional or round
onto a policy boundary. Never use that projection for alert decisions near a boundary.
Use the source sample and integer policy instead.

Checkpoint and acknowledgment validation require a complete qualifying sample,
verify its display projection, and check historical opening/resolution boundaries
with the same exact arithmetic. Missing, inconsistent or below-minimum ratio samples
and samples attached to count conditions are rejected. Samples on unknown conditions
are historical; current raw snapshot counts can be inspected in eligible `evidence`.

### Version compatibility

Count-only selections remain version 1 with their existing policy order, metric
identities, JSON shape, integer values and acknowledgment receipts. Version 2 is
required only for selections containing the one supported expired-ratio rule,
optionally alongside up to 30 count conditions. Version 2 without a ratio and
version 1 with ratio rules/samples are rejected.

The ratio metric is
`deadline_window.expired_ratio_basis_points.<within_ms>.minimum_recipients.<minimum>`.
Resume binds to the same horizon, minimum cohort, warning/recovery basis points and
shared sustain durations. Explicit default recovery and omitted recovery are
equivalent. Adding/removing the ratio changes selection and requires a separate
checkpoint; manage existing pending notifications explicitly. Duplicate ratio rules,
unknown versions, altered metric identities and inconsistent sample state are rejected.
Older Python SDKs reject version 2, so upgrade readers first. Node and Go/CLI
checkpoint kinds remain incompatible.

Existing outbox capacity, closing reservations, exact acknowledgment batches,
cancellation/result snapshots, callbacks and downstream deduplication apply to
ratio alerts too. A low cohort can leave an incident unknown after the backlog
drains; combine an absolute expired-count condition when tracking zero remaining
expired work matters. New work, retention and deadline overrides can change a
ratio without successful processing. Resolution is not proof of business completion.
