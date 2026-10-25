# Restart-safe Node recovery alerts

`watchEventAcknowledgmentIntentHealthWithCheckpoint` adds opt-in checkpointing to
Node acknowledgment-intent recovery monitoring. Persist the checkpoint before any
notification attempt, replay its pending notifications after a crash, and use each
stable incident/sequence pair for downstream deduplication. Persistence and delivery
remain application-owned. The existing non-checkpointed watch keeps its behavior.

Go provides a [checkpointed watch](event-acknowledgment-go-checkpoints.md) with
typed metadata and context cancellation; its checkpoint kind is distinct.

## Start or resume

```js
import {
  watchEventAcknowledgmentIntentHealthWithCheckpoint,
  validateEventAcknowledgmentIntentCheckpoint,
} from '@gregale/sdk-node/preview';

const saved = await loadOwnedCheckpoint();
const result = await watchEventAcknowledgmentIntentHealthWithCheckpoint(pool, accountId, appId, {
  signal,
  health: { scanLimit: 1000, stallAfterMs: 300000 },
  dueThreshold: 100, stalledThreshold: 1, parkedThreshold: 1, sustainMs: 30000,
  resume: saved == null ? undefined : validateEventAcknowledgmentIntentCheckpoint(saved),
  pollAcknowledgments: (checkpoint, callbackSignal) => readOwnedAcknowledgments(checkpoint, callbackSignal),
  onProgress: async (progress, callbackSignal) => {
    await saveOwnedCheckpointAtomically(progress.checkpoint, callbackSignal);
    for (const notification of progress.checkpoint.pending) {
      callbackSignal.throwIfAborted();
      await deliverIdempotently(notification, callbackSignal);
      await recordOwnedAcknowledgment({ incidentId: notification.incidentId, sequence: notification.sequence }, callbackSignal);
    }
  },
});
// Cancellation returns a detached result; it does not resolve any incident.
// Persist result.checkpoint with a separate, bounded shutdown context.
```

All persistence, acknowledgment and delivery functions above belong to the
application. Keep one ownership-fenced writer per checkpoint, serialize offline
acknowledgments with live watch updates, and commit each complete snapshot
atomically. A store must reject stale writers. Do not mutate the supplied snapshot
or overwrite it with an older version. The SDK does not create a database table,
file, notification transport or background service.

Use the same normalized account/app scope and effective policy on resume. Policy
binding includes health scan/stall settings, thresholds, poll interval, query
deadline, freshness and sustain duration. A changed owner or policy is rejected
before callbacks or reads. Supply previous explicit options if defaults differ.
The [ordinary watch options and database timeout requirements](event-acknowledgment-intent-health.md#node-watch)
apply unchanged. No platform client or credentials are needed for the health read.

## Checkpoint contents and validation

Both versions use kind `gregale.node.event-acknowledgment-intent-watch` and are distinct
from deadline-watch, Go/CLI and non-checkpointed recovery watch state. It contains:

- Normalized owner scope, complete effective policy and a random session UUID.
- A monotonic safe-integer notification sequence and timestamp high-water mark.
- Condition records with historical values and unresolved incident IDs.
- Ordered `pending` lifecycle notifications and retained `acknowledged` receipts.

| Format | Selection and conditions |
| --- | --- |
| Version 1 | `parkedThreshold` is zero or omitted. The normalized policy omits that field and preserves the existing JSON field order, with one or two due/stalled conditions. |
| Version 2 | `parkedThreshold` is positive. The complete policy includes that threshold, with one through three enabled conditions in due, stalled, parked order. |

The validator rejects a version/selection mismatch: version 1 cannot enable
parked alerts and version 2 cannot omit or disable them. Policy counts must be
integers within the health scan limit. Other saved policy settings remain
explicit; missing fields do not acquire current defaults. Older Node validators
reject version 2. Upgrade every host that can resume or acknowledge such a
checkpoint before enabling parked alerts. Monitoring format versions are
independent of retry-policy versions, and Node snapshots remain distinct from Go.

Changing thresholds requires a new watch session. A version 1 checkpoint cannot
resume with parked alerts enabled, and changing only its version is invalid.
Finish/reconcile the old session and retain its checkpoint, unresolved incidents
and pending notifications under the application's writer fence before starting
the new selection. Do not discard pending events or synthesize resolutions.

`validateEventAcknowledgmentIntentCheckpoint(unknown)` validates bounds, policy,
timestamps, sequences, incident lineage, threshold values and acknowledgment order.
It reconstructs known fields rather than retaining unknown payloads or timer data.
Invalid/cross-format checkpoints throw a generic `TypeError`. The checkpoint holds
only metadata: no event body, callback result, platform token or recovery claim.
Treat storage as trusted application state; validation is not authentication or a
signature and cannot detect an older otherwise valid snapshot.

A timestamp high-water mark survives restart. Repeated/regressing database evidence
cannot become fresh merely because the process restarted. Historical values are
not live proof. Monotonic timers and unfinished sample proofs are never serialized.

## Resume and incident lifecycle

On startup, saved conditions become unknown while unresolved incident IDs remain.
The optional acknowledgment poll runs, then `onProgress` receives an initial
snapshot **before the first database read**. Replay `checkpoint.pending` there;
`progress.alerts` contains only notifications generated by the latest observation
and is empty at startup. Pending historical notifications can be delivered even
when the health database is currently unavailable.

Fresh complete, advancing observations must span `sustainMs` with at least two
samples before a new opening, reestablishment or resolution. Missing, truncated,
stale or out-of-order evidence and sample gaps reset proof and retain unresolved
identity. A renewed breach reconfirms an existing incident without a second
opening. A sustained clear resolves that same incident, including after a restart
or loss of confirmation. Crossing between warning and recovery resets the proof.

This differs deliberately from the ordinary watch, which has no retained incident
identity. In checkpoint mode, unknown evidence and confirmation loss appear in
progress; only `opened` and `resolved` lifecycle notifications enter the outbox.
An existing incident never disappears because evidence is missing or a watch stops.

Each notification has `sequence`, `incidentId`, `metric`, `transition`, `at`,
`value` and `threshold`. IDs combine the checkpoint session with the opening
sequence; a resolution uses that same ID and a later sequence. `at` is local
observation completion, while condition `observedAt` is the database clock.
Notifications are historical transitions and can be replayed before current
reconfirmation; replay does not assert that their old evidence remains fresh.
Deliver pending notifications in sequence order and make downstream delivery
idempotent by incident ID plus sequence (and owner scope where appropriate).

### Parked-intent alerts

Set `parkedThreshold` to an integer count from 1 through the effective health
`scanLimit`. Zero/omission disables it; due and stalled thresholds may both be
zero for a parked-only watch. Metric `parked_intents` uses the bounded health
`parked` count, with equality breaching the threshold. Due/stalled values exclude
parked work, so clearing automatic backlog cannot clear a parked incident.

Opening/recovery need the same complete, fresh, advancing observations across
`sustainMs` as other metrics. Failed reads, invalid/truncated coverage, stale,
repeated/regressing timestamps and polling gaps leave confirmation unknown and
retain unresolved identity. An incomplete lower bound above the threshold still
cannot open an incident. Startup replays pending parked notifications before any
health read and requires fresh proof before reconfirming or resolving the retained
incident. The existing outbox and safe sequence bounds reserve resolution capacity.

A resolution describes only a sustained count below the threshold. An operator
budget resume or newer staging can reduce parked count without confirming the
callback or business outcome. This monitor reads metadata; it does not grant
claims, resume work or send platform callbacks. The ordinary Node watch also
accepts `parkedThreshold` with invocation-local notifications.

## Exact acknowledgments and capacity

```js
import { acknowledgeEventAcknowledgmentIntentAlerts } from '@gregale/sdk-node/preview';

const updated = acknowledgeEventAcknowledgmentIntentAlerts(savedCheckpoint, [
  { incidentId: delivered.incidentId, sequence: delivered.sequence },
]);
await saveOwnedCheckpointAtomically(updated);
```

Offline acknowledgment returns a detached validated checkpoint and never mutates
its input. Apply it only while no live writer can overwrite it. A batch is
all-or-nothing and has at most 1024 unique sequence entries. Every exact pair must
match a pending notification or retained acknowledgment receipt. Wrong incident
IDs, unknown/duplicate sequences and acknowledgments from another session fail.
An opening must be acknowledged before or together with its resolution. Repeating
an exact retained acknowledgment is idempotent; receipts older than the latest
1024 acknowledged notifications are no longer accepted as acknowledgment proof.

The watch's optional `pollAcknowledgments` callback applies the same rules before
startup progress and between observations. Keep the acknowledgment ledger durable
and nondestructive until the updated checkpoint has committed. Return only exact
pairs still present in `pending` or retained `acknowledged` receipts; remove stale
ledger entries after their checkpoint receipts are safely recorded. Returning an
invalid batch stops the watch without partially applying it.

`EVENT_ACKNOWLEDGMENT_INTENT_OUTBOX_MAX` is 1024. Pending notifications plus one
reserved closing slot per unresolved incident cannot exceed that bound. Opening
an incident reserves queue and safe sequence capacity for its future resolution. A full
outbox or insufficient safe sequence space blocks new openings with
`notification_outbox_full` or `notification_sequence_exhausted` progress. It does
not drop queued notifications or replace incident IDs. Resolutions of existing
incidents retain their reserved capacity. After acknowledgments free capacity,
a fresh qualifying observation is still required before opening anything new.

Acknowledgment receipts form a separate bounded history. No receipts, business
receipts or acknowledgment intents are deleted by this monitor. If sequence space
is exhausted, drain/reconcile the checkpoint and deliberately start a new session;
never reset the sequence within an existing session.

## Progress, failure and shutdown

`onProgress(progress, signal)` is required and awaited sequentially. Progress
includes the checkpoint, observation count, evidence status/reason, optional local
health metadata, newly generated `alerts`, and per-condition `confirmations`.
Confirmation fields expose phase, reason, capped fresh sample count, elapsed and
remaining duration; time alone is not confirmation. Saved counts remain historical
while evidence is unknown. Snapshots are detached: changing them cannot mutate
the running watch, but persist them unchanged.

Persist the checkpoint before sending **any** pending notification. Record an
acknowledgment only after the destination confirms receipt under the application's
idempotency contract. If delivery succeeds and the acknowledgment write is lost,
replay repeats the same pair; the destination must deduplicate it. This is at-least-once
notification delivery, not a transaction across storage and a notification service.

A progress or acknowledgment callback failure stops with
`EventAcknowledgmentIntentCheckpointWatchCallbackError`. Its `phase` identifies the
callback and `.result` contains a detached metadata snapshot with
`stopReason: 'callback_failure'`; raw causes are omitted. Persist/reconcile
`error.result.checkpoint` before notification retry. A failed save may already
have committed: use fenced atomic storage and compare current saved state rather
than inferring rollback or blindly overwriting it. SDK callbacks are not retried.

Host cancellation, including an already-aborted signal, returns a detached result
with `stopReason: 'interrupted'`, unknown evidence and retained pending/incident
state. It waits for active driver/callback work, emits no resolution and
acknowledges nothing automatically. Persist the final checkpoint with an
independently bounded shutdown context because the watch signal is already
aborted. Keep storage ownership until that write completes; if ownership was lost,
a stale shutdown writer must not overwrite the new owner. Invalid options or
resume input still reject before the watch starts.

Run from an application-owned durable monitoring host. No helper keeps a parked
VM awake or changes delivery ordering/deduplication. See the
[checkpoint integration example](../examples/event-acknowledgment-node/README.md#persist-recovery-alert-checkpoints).
Local validation is SDK build, generation, packaging and static checks; restart,
notification replay, storage fencing and timing require hosted acceptance before
release, including parked-only/mixed conditions, incomplete evidence, operator
resume/supersession and version 1/version 2 compatibility.
