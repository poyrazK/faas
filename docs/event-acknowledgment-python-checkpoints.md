# Restart-safe Python acknowledgment recovery alerts

`awatch_event_acknowledgment_intent_health_with_checkpoint` adds opt-in durable
alert state to the [Python recovery watch](event-acknowledgment-intent-health.md#python-watch).
Persist each checkpoint before notifying, replay its pending lifecycle events
after a crash, and deduplicate downstream delivery by owner/incident/sequence.
Storage, fencing and notification delivery remain application-owned. The ordinary
watch retains its existing in-memory behavior.

Go provides a [checkpointed watch](event-acknowledgment-go-checkpoints.md) with
typed metadata and context cancellation; its checkpoint kind is distinct.

## Start or resume

```python
from faas_sdk.preview import (
    EventAcknowledgmentIntentCheckpointWatchOptions,
    EventAcknowledgmentIntentCheckpointWatchCancelled,
    awatch_event_acknowledgment_intent_health_with_checkpoint,
)

saved = await load_owned_checkpoint()

async def poll(checkpoint, cancel):
    return await read_owned_acknowledgments(checkpoint, cancel)

async def progress(snapshot, cancel):
    await save_owned_checkpoint_atomically(snapshot["checkpoint"], cancel)
    for notification in snapshot["checkpoint"]["pending"]:
        cancel.check()
        await deliver_idempotently(notification, cancel)
        cancel.check()
        await record_owned_acknowledgment({
            "incident_id": notification["incident_id"], "sequence": notification["sequence"],
        }, cancel)

try:
    await awatch_event_acknowledgment_intent_health_with_checkpoint(
        lambda: application_pool.connection(timeout=3), account_id, app_id,
        EventAcknowledgmentIntentCheckpointWatchOptions(
            due_threshold=100, stalled_threshold=1, parked_threshold=1, sustain_ms=30000,
            resume=saved, on_progress=progress, poll_acknowledgments=poll,
        ),
        stop_event=shutdown,
    )
except EventAcknowledgmentIntentCheckpointWatchCancelled as stopped:
    # Independently bounded shutdown persistence; preserve the current writer fence.
    await save_owned_checkpoint_on_shutdown(stopped.result["checkpoint"])
    raise
```

The callbacks and storage operations above belong to the application. Keep one
ownership-fenced writer per checkpoint and serialize offline acknowledgments with
live updates. Commit a complete snapshot atomically; reject stale writers. Do not
overwrite current state with an older otherwise valid checkpoint. The SDK installs
no file, table, service, transport or background task on import.

The connection factory, idle autocommit leases, cooperative read/cleanup deadlines
and timing options follow the ordinary Python watch. Copy the same effective
health settings, thresholds, poll/query/freshness/sustain values on resume. An
owner or policy mismatch fails before callbacks or health reads. `on_progress` is
required; `poll_acknowledgments` is optional. Both may be synchronous or awaitable
and receive the same read-only cancellation view as ordinary notifications.

## Format and validation

`validate_event_acknowledgment_intent_checkpoint(value)` returns a detached
canonical dictionary. Both versions use kind
`gregale.python.event-acknowledgment-intent-watch`, distinct from Node, Go/CLI and
Python deadline checkpoints. It contains normalized `target`, complete effective
`policy`, a random canonical UUID `session`, `sequence`, optional database
`last_observed_at`, enabled `conditions`, and ordered `pending`/`acknowledged`
notification arrays. Timestamps are canonical UTC RFC 3339 strings with six
fractional digits. Sequence is a native integer 0..9007199254740991.

| Format | Selection and conditions |
| --- | --- |
| Version 1 | `parked_threshold` is zero or omitted. Saved policy omits that field and preserves the existing keys/order, with one or two due/stalled conditions. |
| Version 2 | `parked_threshold` is positive. Saved policy includes the threshold, with one through three enabled conditions in due, stalled, parked order. |

The new option is keyword-only, preserving existing positional arguments for
ordinary and inherited checkpoint options. The validator rejects version/selection
mismatches: version 1 cannot enable parked alerts and version 2 cannot omit or
disable them. Counts require native integers; booleans/floats are rejected.
Other saved policy settings stay explicit. Older Python validators reject version
2. Upgrade every host that may resume or acknowledge such checkpoints before
enabling parked alerts. These monitoring versions are independent of retry-policy
versions, and snapshots remain distinct from Go/Node formats.

Changing thresholds requires a new watch session. A saved version 1 checkpoint
cannot resume with parked alerts enabled, and changing only its version is
invalid. Finish/reconcile the previous session and retain its checkpoint,
unresolved incidents and pending notifications under the application's writer
fence before starting the new selection. Do not discard notifications or
synthesize resolutions.

Validation checks bounds, timestamps, policy, condition order, native integer
types, incident lineage and acknowledgment ordering. It reconstructs known fields,
dropping unknown payloads/proof fields. Invalid or foreign formats raise a fixed
`ValueError`. No platform token, claim, original event, result body or raw error
is retained. Validation is not authentication and cannot detect rollback to an
older valid snapshot; storage fencing/version control must handle that.

The database timestamp high-water mark survives restart. Monotonic clocks,
unfinished confirmation intervals and sample counts are never serialized.
Historical counts/observations are not fresh proof.

## Resume and incident identity

On startup, saved conditions become unknown while unresolved `incident_id` values
remain. The optional acknowledgment poll runs, then `on_progress` receives an
initial detached snapshot **before the first health read**. Replay
`snapshot["checkpoint"]["pending"]` there. `snapshot["alerts"]` contains only new
lifecycle notifications from the latest observation and is empty at startup.
Pending historical notifications can be delivered while the health database is
unavailable; their replay does not claim their old evidence remains fresh.

Fresh complete strictly advancing observations must span `sustain_ms`, with at
least two samples, before opening, reconfirming or resolving an incident. Missing,
incomplete, stale, out-of-order or gapped evidence resets proof and retains the
unresolved identity. A renewed breach reconfirms that same incident without
opening it again. Sustained clear evidence resolves it, including after resume
or lost confirmation. Switching warning/recovery modes resets pending proof.

Only `opened` and `resolved` lifecycle events enter the outbox. Evidence loss and
confirmation status appear in progress. Every notification contains `sequence`,
`incident_id`, `metric`, `transition`, local completion time `at`, `value` and
`threshold`. IDs combine session UUID and opening sequence. A resolution keeps
that ID and uses a later global sequence. Deliver pending entries in sequence
order and deduplicate by owner scope plus incident ID and sequence.

### Parked-intent alerts

Set `parked_threshold` to a native integer count from 1 through the effective
health `scan_limit`. Zero/omission disables it; due and stalled thresholds may
both be zero for a parked-only watch. Metric `parked_intents` uses health `parked`,
with equality breaching the threshold. Due/stalled values exclude parked work,
so clearing automatic backlog cannot clear a parked incident.

Opening and recovery use the same complete, fresh, advancing observations across
`sustain_ms` as the other metrics. Failed reads, invalid/truncated coverage, stale,
repeated/regressing timestamps and sample gaps leave confirmation unknown and
retain unresolved identity. Even an incomplete lower bound already above the
threshold cannot open an incident. Startup replays pending parked notifications
before health reads and requires fresh proof before reconfirming or resolving.
Existing outbox and safe-sequence limits reserve capacity for its resolution.

A resolution means only that retained parked count stayed below the threshold.
An operator budget resume or newer staging can reduce that count without callback
confirmation or business success. This watch only reads metadata; it never grants
claims, resumes work or sends platform callbacks. The ordinary Python watch also
accepts `parked_threshold` with in-memory lifecycle notifications.

## Exact acknowledgments and capacity

```python
from faas_sdk.preview import acknowledge_event_acknowledgment_intent_alerts

updated = acknowledge_event_acknowledgment_intent_alerts(saved, [{
    "incident_id": delivered["incident_id"], "sequence": delivered["sequence"],
}])
await save_owned_checkpoint_atomically(updated)
```

This pure helper validates/copies its input, never mutates it, and applies a batch
all-or-nothing. At most 1024 unique exact pairs may be acknowledged. Each pair
must match a pending event or retained acknowledgment receipt. Wrong IDs,
duplicate/unknown sequences, booleans and another session's acknowledgments fail.
An opening must be acknowledged before or together with its resolution. Repeated
exact acknowledgments remain idempotent while their receipt is retained. Only
the latest 1024 acknowledged notifications are retained.

The live watch applies identical validation to `poll_acknowledgments` before
startup progress and between observations. Keep the acknowledgment ledger durable
and nondestructive until its updated checkpoint commits. Return only exact pairs
still pending or present in retained receipts, removing old ledger rows after
their checkpoint update is safely recorded. Invalid batches stop the watch without
partial application. Never run an offline writer concurrently with the live one.

`EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX` is 1024. Pending notifications plus one
reserved closing slot per unresolved incident must fit that bound. Opening an
incident reserves queue and sequence room for its future resolution. Full capacity
or sequence exhaustion blocks new openings with `notification_outbox_full` or
`notification_sequence_exhausted` confirmation progress. Existing notifications
are retained and reserved resolutions still fit. After acknowledgment frees room,
new openings require another eligible observation. If sequence space is exhausted,
drain/reconcile state and deliberately start a new session; never reset sequence
inside an existing session. Business receipts/intents are not pruned by monitoring.

## Progress, errors and shutdown

`on_progress(snapshot, cancel)` runs sequentially at startup and after each read.
The detached JSON-safe snapshot contains `stop_reason`, capped `observations`,
`evidence_status`/`evidence_reason`, optional health metadata, `checkpoint`, new
`alerts`, and per-condition `confirmations`. Confirmation metadata exposes phase,
reason, capped fresh samples, elapsed/required/remaining milliseconds. Time alone
cannot confirm anything. Unknown evidence retains historical checkpoint values.
Mutating callback snapshots cannot change the live watch; persist them unchanged.

Persist the complete checkpoint before **any** pending notification attempt.
Record an acknowledgment only after destination confirmation under its idempotency
contract. If delivery succeeds but the acknowledgment write is lost, replay repeats
the same pair. This is at-least-once notification delivery; storage and delivery
are separate transactions.

A progress or acknowledgment callback failure stops with
`EventAcknowledgmentIntentCheckpointWatchCallbackError`, fixed code
`event_acknowledgment_intent_checkpoint_callback_failed`. `error.phase` identifies
`progress` or `acknowledgments`; detached `error.result` has
`stop_reason="callback_failure"`. Arbitrary callback causes are omitted. Invalid
acknowledgment batches have the same failure boundary. Reconcile current fenced
storage with `error.result["checkpoint"]` before retry; a failed save may already
have committed. Do not infer rollback or blindly overwrite saved state. SDK
callbacks are not automatically retried.

Event/native cancellation joins active reads, acquisition, cleanup and callbacks,
then raises `EventAcknowledgmentIntentCheckpointWatchCancelled`, a
`CancelledError`, with detached `error.result`. Even an already-cancelled valid
watch retains checkpoint state. Its stop reason is `interrupted`, evidence is
unknown, new alerts are empty and conditions need fresh confirmation. It does
not resolve incidents or automatically acknowledge pending events. Persist the
final checkpoint with an independent bounded shutdown context while retaining
ownership; a stale owner must not overwrite its successor. Invalid options/resume
still fail before startup. The ordinary watch's cancellation API is unchanged.

Run on an available application monitoring host and drain before closing its
pool/loop. No helper keeps a parked VM awake or changes event ordering, consumer
deduplication or business side effects. See the
[checkpoint example](../examples/event-acknowledgment-python/README.md#persist-recovery-alert-checkpoints).
Local evidence covers builds, generation and static checks; hosted restart,
replay, fencing, timing and cancellation qualification remains pending before release,
including parked-only/mixed alerts, incomplete evidence, operator resume/supersession
and version 1/version 2 compatibility.
