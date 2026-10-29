# Deadline count watches in Node

The Node SDK exports `watchEventSubscriptionDeadlines(target, options)` from
`@gregale/sdk-node`. It polls the existing complete deadline-window endpoint,
without sampling, modifying subscriptions, replaying deliveries or sending messages.
Configure the SDK client/authentication normally before starting the watch.

For current recipient identities after an alert, use
[`investigateEventDeadlineAlert`](event-deadline-node-investigation.md) with the saved
checkpoint and exact incident/sequence pair. It derives matching filters and reads
one bounded page without changing the checkpoint or delivery state.

```ts
import {
  watchEventSubscriptionDeadlines,
  validateEventDeadlineCheckpoint,
  acknowledgeEventDeadlineAlerts,
} from '@gregale/sdk-node/preview';

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
count or expired-ratio warning is required. Counts must be nonnegative safe integers,
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

The checkpoint kind is `gregale.node.event-deadline-watch`. Count-only selections
use version 1; selections containing an expired ratio use version 2 (see below).
Both are **Node deadline-only formats**, not Go/CLI or Python checkpoints. Its policy,
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

## Expired backlog ratios

Select an optional ratio rule using integer basis points:

```ts
const options = {
  withinMs: 300_000,
  expiredRatio: {
    warningBasisPoints: 1000, // >=10%
    recoveryBasisPoints: 500, // <=5%
    minimumRecipients: 100,
  },
  sustainMs: 60_000,
  recoverySustainMs: 120_000,
};
const result = await watchEventSubscriptionDeadlines(target, options);
```

The ratio is `summary.already_expired_count / summary.deadline_recipients` from
one complete snapshot. The denominator includes **all** retained captured pending
application recipients with effective deadlines; the upcoming horizon does not
restrict it. This is a pending-backlog ratio, not the fraction of all accepted or
completed events that expired.

`warningBasisPoints` requires an integer 1..10000; 100 basis points = 1%.
`recoveryBasisPoints` must be an integer >=0 and strictly below warning. Omission
defaults to warning minus one basis point; explicit zero requires a zero numerator
in a qualifying cohort. `minimumRecipients` is mandatory and must be a positive
safe integer <=9007199254740991. `EVENT_DEADLINE_RATIO_BASIS_POINTS_MAX` is 10000,
representing 100%. Null, fractional, nonfinite and out-of-range option values are
rejected. Options are copied into normalized rules before the first await.

A ratio can be the only selected condition or accompany total/reason count rules.
It shares the existing warning/recovery durations, freshness checks, complete
partition validation and single deadline-window request. It is appended after
existing count conditions; at most 31 conditions can be selected. No endpoint,
generated API, migration or per-ratio request is added.

A zero denominator or cohort below the minimum reports unknown with
`insufficient_deadline_recipients`, resetting that condition's live proof. Other
eligible count conditions continue independently. Node retains an active incident
identity while its evidence is unknown, requiring fresh warning/recovery proof
when a qualifying cohort returns. It does not emit a successful resolution for
small or empty cohorts. This preserves the Node watch's existing lifecycle; it
does not adopt Go's closing `confirmation_lost` notification behavior.

### Exact decisions and historical samples

Warning and recovery comparisons use `BigInt` cross-products:

```
expiredRecipients * 10000 >= warningBasisPoints * deadlineRecipients
expiredRecipients * 10000 <= recoveryBasisPoints * deadlineRecipients
```

All source counts and policy settings must first be safe integers. Only these
transient comparisons use BigInt. Ratio conditions and alerts retain a JSON-safe
`ratioSample: { expiredRecipients, deadlineRecipients }`. Their `value` is an
**approximate basis-point display value**, potentially fractional. Never compare
or round that projection to reproduce alert decisions near a boundary; use the
sample and integer policy. A projected value may round onto a boundary while the
exact sample lies on one side.

Checkpoint validation verifies sample counts, qualifying denominator and display
projection, then checks opening/resolution boundaries with the same exact arithmetic.
Missing, inconsistent, below-minimum or extra samples attached to count conditions
are rejected. Samples on unknown conditions are historical; current complete raw
counts remain available through `evidence` when response evidence is eligible.
No stored sample becomes fresh timer proof on resume.

### Version 1 and version 2 compatibility

Version 1 remains the count-only format. Existing count-only checkpoints, condition
order, metric identities, alerts and acknowledgment receipts remain readable and
continue to serialize as version 1. Their values still require bounded integers;
fractional count values and ratio metadata are rejected.

Version 2 requires exactly one configured expired-ratio rule and permits up to 30
existing count conditions alongside it. Its metric identity is
`deadline_window.expired_ratio_basis_points.<withinMs>.minimum_recipients.<minimum>`.
Policy, conditions and retained ratio alerts bind the minimum cohort and effective
warning/recovery thresholds. Horizon and shared durations remain resume policy too.
Omitted recovery and an explicit default produce the same canonical policy.

A version-1 checkpoint cannot carry ratio rules or samples. Unsupported versions,
ratio-to-count metric substitutions, duplicate ratio rules and inconsistent samples
are rejected. Version 2 without a ratio rule is also rejected. Older Node SDKs
reject version 2, so upgrade readers before handing them ratio checkpoints.
Adding/removing a ratio changes watch selection and cannot resume the same
checkpoint; use a separate checkpoint and manage pending notifications explicitly.
Go/CLI and Python checkpoint formats remain separate.

The bounded outbox, closing reservations, exact acknowledgments, callback error
snapshots, cancellation and downstream deduplication rules above also apply to
ratio alerts. Low cohorts can leave an incident unknown even when the backlog
has drained; combine an absolute expired-count alert when zero remaining expired
work must be tracked separately. New work can lower the ratio by increasing its
denominator, and retention/overrides can change either count without business
completion. Ratio resolution never certifies delivery success.
