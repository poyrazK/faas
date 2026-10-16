# Deadline count watches in Node

The Node SDK exports `watchEventSubscriptionDeadlines(target, options)` from
`@gregale/sdk-node`. It polls the existing complete deadline-window endpoint,
without sampling, modifying subscriptions, replaying deliveries or sending messages.
Configure the SDK client/authentication normally before starting the watch.

```ts
import {
  watchEventSubscriptionDeadlines,
  validateEventDeadlineCheckpoint,
  acknowledgeEventDeadlineAlerts,
} from '@gregale/sdk-node';

const abortController = new AbortController();
const result = await watchEventSubscriptionDeadlines({
  app: 'orders',
  appId: '11111111-1111-4111-8111-111111111111',
  subscriptionId: '22222222-2222-4222-8222-222222222222',
}, {
  withinMs: 300_000,
  upcoming: 50,
  upcomingRecovery: 20,
  expired: 1,
  expiredRecovery: 0,
  upcomingReasons: { subscription_paused: 10 },
  upcomingReasonsRecovery: { subscription_paused: 2 },
  sustainMs: 60_000,
  recoverySustainMs: 120_000,
  intervalMs: 5000,
  timeoutMs: 600_000,
  maxAgeMs: 30_000,
  signal: abortController.signal,
  // resume: validateEventDeadlineCheckpoint(savedJSON),
  onProgress: async (progress, signal) => {
    // Application-owned functions: save atomically before attempting notification.
    await saveCheckpoint(progress.checkpoint, signal);
    await deliverAlertsIdempotently(progress.alerts, signal);
  },
  pollAcknowledgements: async (_checkpoint, signal) => readAcknowledgements(signal),
});
await saveCheckpoint(result.checkpoint);
```

`saveCheckpoint`, `deliverAlertsIdempotently` and `readAcknowledgements` above are
application-owned persistence/notification functions. The helper does not create
files or contact notification destinations. Run one writer per checkpoint and
serialize offline acknowledgment changes with any running watcher.

## Policy and coverage

Provide both the application slug and its expected stable UUID. Every response
must match that UUID and subscription UUID. IDs are canonicalized to lowercase.
`withinMs` is required: whole milliseconds 100..86400000. At least one positive
total or known-reason warning is required. Counts must be nonnegative safe integers,
up to `Number.MAX_SAFE_INTEGER` (9007199254740991); zero disables warnings.
Unknown reason labels are rejected even at zero. `EVENT_DEADLINE_REASONS` exposes
all 14 accepted labels.

`upcomingRecovery` and `expiredRecovery` independently configure inclusive total
recovery counts. `upcomingReasonsRecovery` and `expiredReasonsRecovery` configure
inclusive boundaries for selected reason warnings. Omission defaults to warning
minus one; explicit zero requires zero. A recovery count must be below an enabled
warning for the same cohort/reason. Reason maps are normalized into detached,
deterministically ordered rules before the first await. Caller changes do not
change the running watch.

Warning and recovery each require at least two fresh, advancing observations
spanning their respective local monotonic-clock duration. Durations independently
default to 60000ms. The 50/20 example opens at >=50, remains firing at 21..49 and
resolves only at <=20 after sustained recovery. Leaving a candidate band resets its
timer. Fresh warning proof after a lost confirmation reestablishes the existing
incident without generating a second opening.

The read must have complete read-only coverage, exact known-reason partitions,
consistent counters/deadlines/controls, matching horizon and usable timestamps.
Upcoming means strictly between observation and horizon end; expired counts cover
all deadlines at or before observation independently of horizon. Missing/unknown
reason keys are never zero. Counts above the 1000-row preview cap remain covered.

`intervalMs` defaults to 5000 and accepts 100..60000. `timeoutMs` defaults to 600000,
`maxAgeMs` to 30000 and `requestTimeoutMs` to 10000; each accepts 100..86400000.
All timing options require integer milliseconds. The watch cancels pending reads
and wakes sleeps on abort/timeout. Callbacks are awaited serially and receive the
watch's abort signal; callbacks must cooperate with cancellation. Persistence is
not abandoned in the background when a callback is still running.

## Unknown evidence and progress

Read failures (including unsupported endpoints), invalid/incomplete responses,
future/stale timestamps, repeated/regressing observations and local sample gaps
cannot open or resolve incidents. They reset confirmation and leave conditions
unknown. Timestamps retain nanosecond ordering rather than rounding observations
to JavaScript Date milliseconds. Freshness uses the local wall clock with its
millisecond resolution; duration/gap measurement uses the monotonic clock.

`confirmations` includes warning/recovery progress, fresh sample counts, elapsed
and remaining duration, unknown evidence reasons, resume waits and outbox blockage.
Remaining duration is a snapshot, not a promise that time alone will confirm the
condition. Counts stored on unknown conditions are historical, not fresh proof.
`evidenceStatus` describes response validity/freshness; per-condition confirmation
also rejects repeated/regressing observations and gaps. Unknown evidence is
reported through progress, rather than a durable lifecycle notification.

## Checkpoints and notification delivery

The checkpoint kind is `gregale.node.event-deadline-watch`, version 1. It is a
**Node deadline-only format**, not a Go/CLI delivery-health checkpoint. Its policy,
target, condition states, incident IDs, sequence and notification outbox are
JSON-serializable. Timer/sample proof is deliberately excluded. Validate loaded
JSON with `validateEventDeadlineCheckpoint` before use. Resume binds to the same
application/subscription, horizon, enabled counts, effective recovery boundaries
and durations. Omission and explicit default recovery are equivalent. Reason map
insertion order does not affect matching. Poll timing may change on resume.

Resume resets all conditions to unknown and requires fresh evidence. An active
incident remains unknown through the middle band until a warning or recovery is
confirmed. Saved observations and downtime do not count as new proof.

`alerts` contains only transitions created by the latest observation; lifecycle
transitions are `opened` and `resolved`. `checkpoint.pending` retains all unacknowledged
notifications. Persist the detached checkpoint **before** publishing notifications.
After a crash, replay pending alerts using `(session, incidentId, sequence)` for
idempotent downstream handling. Delivery remains at least once, not exactly once.
No authentication secrets or event payloads belong in this checkpoint.

Acknowledge exact `{ incidentId, sequence }` pairs through `pollAcknowledgements`
or offline `acknowledgeEventDeadlineAlerts(checkpoint, pairs)`, then persist the
returned checkpoint. The batch validates completely before changing state. Duplicate
pairs in one batch, mismatched identities and unknown sequences are rejected.
Repeating an acknowledgment is idempotent while its receipt remains in the latest
1024 acknowledged alerts. Older evicted receipts are rejected. A resolution cannot
be acknowledged before its opening unless both appear in the same batch.

The pending queue has 1024 slots, including one reserved closing slot per active
incident. New openings pause when capacity or safe-integer sequence space is
insufficient; conditions report `notification_blocked` and require continuing fresh
proof. Existing incidents retain room to resolve. Acknowledgments free queue slots;
sequence exhaustion requires a new checkpoint after draining existing incidents.

Callbacks receive detached snapshots. If either callback throws, the watch stops
with `EventDeadlineWatchCallbackError`; its `result.checkpoint` retains the state
needed for persistence and notification retry. Other unexpected failures reject
the promise. Normal completion returns `timed_out` or `interrupted`; persist the
returned checkpoint too. The watch does not assume that notification succeeded
merely because `onProgress` returned.

Counts describe rolling cohorts. Work can move to another reason, expire out of
upcoming coverage, gain an override or leave retained history without successful
business completion. A resolved count alert does not certify delivery success.
See [count and triage semantics](event-subscription-deadline-window.md).
