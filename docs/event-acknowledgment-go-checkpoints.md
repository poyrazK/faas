# Restart-safe Go acknowledgment recovery alerts

`WatchEventAcknowledgmentIntentHealthWithCheckpoint` adds opt-in checkpointing to
the [Go recovery health watch](event-acknowledgment-intent-health.md). Persist each
complete checkpoint before notifying, replay pending lifecycle alerts after a
crash, and deduplicate delivery by owner/incident/sequence. Storage, fencing and
notification delivery remain application-owned. The ordinary watch keeps its API
and in-memory behavior.

## Start or resume

```go
result, err := preview.WatchEventAcknowledgmentIntentHealthWithCheckpoint(
    ctx, db, accountID, appID,
    preview.EventAcknowledgmentIntentCheckpointWatchOptions{
        EventAcknowledgmentIntentWatchOptions: preview.EventAcknowledgmentIntentWatchOptions{
            DueThreshold: 100, StalledThreshold: 1, ParkedThreshold: 1,
            Sustain: 30 * time.Second,
        },
        Resume: saved, // *EventAcknowledgmentIntentCheckpoint; nil starts a session.
        PollAcknowledgments: readOwnedAcknowledgments,
        OnProgress: persistThenNotify,
    })
// Inspect result even when err != nil. Cancellation includes the final checkpoint.
```

Use trusted account/app UUIDs and the application PostgreSQL `*sql.DB`; no platform
client or credential is needed. `OnProgress` is required and has signature
`func(context.Context, EventAcknowledgmentIntentCheckpointWatchResult) error`.
The optional acknowledgment poll is
`func(context.Context, EventAcknowledgmentIntentCheckpoint) ([]EventAcknowledgmentIntentAcknowledgment, error)`.
Callbacks run sequentially and receive detached snapshots.

Keep one ownership-fenced writer across load, watch and shutdown persistence.
Serialize offline acknowledgments with live updates; atomically commit each
complete snapshot and reject stale writes. A successful validation cannot detect
an older otherwise valid snapshot. Match the normalized owner and every effective
policy field on resume, including health scan/stall settings, thresholds,
poll/query intervals, freshness and sustain duration. Mismatches fail before
callbacks or health reads. Defaults/bounds follow the ordinary Go watch.

## Typed checkpoint format

`EventAcknowledgmentIntentCheckpoint` uses kind
`gregale.go.event-acknowledgment-intent-watch`. It is distinct from Node/Python
recovery checkpoints and the Go/CLI platform delivery-health/deadline formats.
It contains normalized `account_id`/`app_id`, complete `policy`, a random canonical
UUID `session`, `sequence`, optional `last_observed_at`, ordered
`conditions`, `pending` lifecycle events and retained `acknowledged` receipts.

| Format | Selection and conditions |
| --- | --- |
| Version 1 | `ParkedThreshold` is zero. The existing policy JSON omits `parked_threshold`, with one or two due/stalled conditions. |
| Version 2 | `ParkedThreshold` is positive. Policy includes `parked_threshold`, with one through three enabled conditions in due, stalled, parked order. |

Both versions retain the same kind, notification shape, sequence/incident identity
and acknowledgment contract. A version/selection mismatch is invalid, including
version 2 with parked alerts disabled. Version 1 remains readable by existing Go
hosts when parked alerts are disabled; older validators reject version 2. Upgrade
every host that can resume or acknowledge a version 2 checkpoint first. These
versions describe monitoring metadata, independently of retry-policy versions.

Changing enabled thresholds requires a new watch session. A saved version 1
checkpoint cannot resume with `ParkedThreshold > 0`; changing only its version is
invalid. Finish/reconcile the previous session and retain its checkpoint, unresolved
incidents and pending notifications under the application's writer fence before
starting the new selection. Do not discard old notifications or synthesize resolutions.

`ValidateEventAcknowledgmentIntentCheckpoint(saved)` returns a detached canonical
copy and error. Decode bounded trusted JSON into the concrete Go type first.
Integer fields reject fractional/boolean JSON values through `encoding/json`;
unknown JSON fields are not retained. The validator checks bounds, effective
policy, timestamps, condition order/values, incident lineage and acknowledgment
ordering. Errors use `ErrInvalidEventAcknowledgmentIntentCheckpoint` without raw
input. It normalizes scope and times to UTC and strips process-local monotonic
time/location state. The checkpoint contains no platform token, claim capability,
event body, callback result, action reason or raw error.

Time fields use Go `time.Time` and standard RFC 3339 JSON timestamps, preserving
nanoseconds. Policy durations use `time.Duration` with explicit `*_ns` JSON keys;
this preserves sub-millisecond settings accepted by the ordinary Go watch.
Zero/missing duration or scan-limit defaults are invalid in saved policy; zero
thresholds still disable individual conditions. Sequence
is an `int64` from zero through `EventAcknowledgmentIntentSequenceMax`
(9007199254740991); this monitoring bound is independent of delivery generations.
It is safe to persist in a JSON integer without float conversion.

Application storage must bound encoded input before JSON decoding. Validation is
not authentication, a signature, rollback protection or storage fencing. Persist
and load snapshots without modifying their fields or translating other SDK kinds.

## Resume and incident lifecycle

Startup resets conditions to unknown while retaining unresolved incident IDs,
historical values and the database timestamp high-water mark. Monotonic sample
proof and unfinished timers are never serialized. Repeated/regressing database
timestamps therefore cannot become fresh merely because the process restarted.

The optional acknowledgment poll runs, then initial `OnProgress` runs **before
the first health read**. Replay `result.Checkpoint.Pending` there; `result.Alerts`
contains only newly generated transitions and is empty at startup. Historical
alerts can be delivered during a health-database outage. Their replay does not
assert that the old evidence remains fresh.

Fresh complete, strictly advancing observations must span `Sustain` with at least
two samples before an opening, reconfirmation or resolution. Missing, truncated,
stale, out-of-order or gapped evidence resets proof and retains unresolved
identity. Renewed breach reconfirms the same incident without another opening.
Sustained clear resolves that incident, including after a restart or lost
confirmation. Switching warning/recovery direction starts new proof.

### Parked-intent alerts

Set `ParkedThreshold` to a count from 1 through the effective health `ScanLimit`.
Zero disables it; due and stalled thresholds may both be zero for a parked-only
watch. Metric `parked_intents` uses the bounded health `Parked` count. Equality
breaches the threshold. Parked rows remain outside due/stalled eligibility, so a
zero due backlog does not clear a parked incident.

Opening and recovery use the same complete, fresh, sustained evidence and outbox
rules as other conditions. Failed, truncated, invalid, stale, repeated/regressing
or gapped observations leave parked confirmation unknown and retain any unresolved
incident. Even an incomplete lower bound above the threshold cannot open an alert.
Resume replays pending parked notifications before health reads and requires fresh
proof before reconfirming or resolving the retained incident. Capacity accounting
reserves its resolution slot like every other metric.

A parked-count resolution means only that retained parked work stayed below the
threshold. Granting more claims or superseding an intent can reduce this count;
resolution does not prove callback confirmation or business success. The watch
reads metadata and never grants a budget, resumes recovery or sends callbacks.
Node and Python provide equivalent parked alerts in their own
[Node](event-acknowledgment-node-checkpoints.md#parked-intent-alerts) and
[Python checkpoint formats](event-acknowledgment-python-checkpoints.md#parked-intent-alerts).

Only `opened` and `resolved` enter the outbox. Evidence/confirmation loss appears
in progress. Notifications contain sequence, incident ID, metric, transition,
local observation completion time, value and threshold. The incident ID is the
session UUID plus the opening sequence; resolution keeps that ID with a later
sequence. Deliver pending entries in sequence order and deduplicate by owner scope
plus incident ID and sequence. Notification time is local wall time; condition
`ObservedAt` is database time.

## Exact acknowledgments and capacity

```go
updated, err := preview.AcknowledgeEventAcknowledgmentIntentAlerts(saved, []preview.EventAcknowledgmentIntentAcknowledgment{
    {IncidentID: delivered.IncidentID, Sequence: delivered.Sequence},
})
```

This pure helper validates/copies its input and applies each batch all-or-nothing.
At most 1024 unique exact pairs may be acknowledged. Every pair must match a
pending notification or retained acknowledgment receipt. Unknown/duplicate
sequences, wrong IDs or another session's acknowledgments return
`ErrInvalidEventAcknowledgmentIntentAlerts`. An opening must be acknowledged
before or together with its resolution. Repeating an exact retained acknowledgment
is idempotent; only the latest 1024 acknowledged notifications are retained.

The live acknowledgment poll uses the same helper before startup progress and
between observations. Keep the acknowledgment ledger durable and nondestructive
until its updated checkpoint has committed. Return only exact pairs still pending
or in retained receipts, removing older ledger entries after the checkpoint update
is safely recorded. Invalid batches stop the watch without partial application.
Do not concurrently run an offline checkpoint writer.

`EventAcknowledgmentIntentOutboxMax` is 1024. Pending notifications plus one
reserved resolution per unresolved incident must fit that bound. Each opening
also reserves sequence capacity for its eventual resolution. Full capacity or
sequence exhaustion blocks new openings with `notification_outbox_full` or
`notification_sequence_exhausted` progress, preserving existing IDs and pending
entries. Reserved resolutions remain possible. Another eligible observation is
required after acknowledgments free room. If sequence space is exhausted,
drain/reconcile state and deliberately start a new session; never reset the
sequence within a session. Monitoring does not prune business receipts/intents.

## Progress, errors and shutdown

The watch returns `EventAcknowledgmentIntentCheckpointWatchResult` alongside
error. Progress callbacks receive the same detached type: stop reason,
observation count, evidence status/reason, optional local health, checkpoint,
new alerts and per-condition confirmations. Confirmation fields expose phase,
reason, fresh samples capped at two and elapsed/required/remaining durations in
nanoseconds. Time alone cannot confirm anything. Mutating callback snapshots
cannot change the watch; persist them unchanged.
Elapsed confirmation duration is capped at the required sustain interval.

Persist the complete checkpoint before **any** pending notification attempt.
Record an acknowledgment only after destination confirmation under the host's
idempotency contract. If delivery succeeds but acknowledgment persistence is lost,
replay uses the same pair. This is at-least-once notification delivery across
separate storage and notification transactions.

Returned callback errors are replaced by
`*EventAcknowledgmentIntentCheckpointWatchCallbackError`, whose `Phase` is
`progress` or `acknowledgments`. `errors.Is` recognizes
`ErrEventAcknowledgmentIntentCheckpointCallbackFailed`; the returned result has
`StopReason="callback_failure"`. Arbitrary callback/driver causes are omitted.
Invalid acknowledgment batches use the same callback failure boundary. SDK
callbacks are not retried. Reconcile current fenced storage with the returned
checkpoint before retry; a failed save may already have committed. Do not infer
rollback or blindly overwrite current state.

Cancellation takes precedence over a concurrent callback error and returns
`context.Canceled` or `context.DeadlineExceeded`, with `StopReason="interrupted"`,
unknown evidence, empty new alerts and retained pending/incident state. Conditions
require fresh proof again. Even a pre-canceled valid call returns its checkpoint
without callbacks or health reads. Invalid input still fails before startup.

Active reads and callbacks are synchronous and finish before return. The watch
creates no worker goroutine, performs no result-winning race and does not force
driver/callback cancellation. Bound database pool acquisition, statements and
callback storage/transport calls. Finish the watch before closing its pool.
Persist/reconcile the final checkpoint with an independent bounded shutdown
context while retaining writer ownership; a stale shutdown owner must not
overwrite its successor. Cancellation resolves no incident and acknowledges no
pending event. No caller `context.Cause` is inspected or exported. Session entropy
failure returns `ErrEventAcknowledgmentIntentCheckpointUnavailable` before startup.

Run from an available application monitoring host. The
[Go host example](../sdk/go/examples/event-acknowledgment-recovery/README.md#persist-recovery-alert-checkpoints)
shows persistence-before-delivery and durable acknowledgment boundaries. Node and
Python provide [their own](event-acknowledgment-node-checkpoints.md)
[checkpoint formats](event-acknowledgment-python-checkpoints.md).

Local validation covers SDK/example builds, vet, static format/lifecycle/copy and
unchanged read/schema comparisons, plus schema/ADR/documentation checks. No
tests/assertions are added or changed and no local tests run. Hosted restart,
replay, storage fencing, capacity, timing and cancellation qualification remains
pending before release, including parked-only/mixed alerts, incomplete evidence,
budget-resume/supersession recovery and version 1/version 2 compatibility.
