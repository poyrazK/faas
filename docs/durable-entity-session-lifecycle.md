# Recoverable session lifecycle

`defineRecoverableDurableSessionEntity` adds committed status snapshots, explicit
logical-time expiry, bounded cohort reclamation and application migrations. It is
an opt-in schema-version-2 helper; the original schema-version-1 session helper
and `rooms` starter continue to work unchanged. Use the starter namespace
`recoverable-rooms` for this contract. The same owner-authenticated HTTP invocation
or gated service WebSocket transport carries its commands.

## Recovery

```js
import {randomUUID} from 'node:crypto';
import {
  durableSessionLifecycleFrame, decodeDurableSessionStatus,
  decodeDurableSessionLifecycleAcknowledgement,
} from '@gregale/sdk-node/preview/durable-entities/session';

const query = durableSessionLifecycleFrame({
  kind: 'status', session_id: sessionId, query_id: randomUUID(),
});
// Send query via the existing service socket. On HTTP invoke, send its
// request_id and payload with your entity namespace/key selectors.
const status = decodeDurableSessionStatus(socketReply, query);
```

Every new status observation needs a fresh query UUID. Reusing a query replays its
original snapshot, which can be stale. Status commits a serialized snapshot through
normal invocation: it increments the entity version and creates a replay receipt,
and may commit a pending schema migration. It is not a read-only inspection call or
a lease against concurrent session changes. It exposes the current generation,
logical tick, and whether that session exists, plus its sequence/closed/expired
flags when retained. An absent session has null progress fields.

Keep one outstanding mutation per session and retain its exact frame. If you have a
pending frame, replay it to recover the original business result; status cannot
recover that result or replace replay. If local progress was lost, a fresh status
can recover the next sequence for a known retained session. Do not blindly open an
absent identity after reclamation: choose the current generation deliberately.

New open/message/close frames include `generation`; their stable request IDs include
it too. The command result includes kind, generation, session identity, sequence,
closed/expired flags and value. Use `decodeDurableSessionLifecycleAcknowledgement`
to match the committed result before advancing your local cursor. Open/close values
are null. Retain the pending frame if decoding fails, even after a successful commit.

## Expiry and retention

Configure positive safe-integer `idleTicks` and `retentionTicks`. State starts at
logical tick zero. The authenticated service explicitly advances time:

```js
const advance = durableSessionLifecycleFrame({
  kind: 'advance', generation: status.generation,
  tick: 10, operation_id: randomUUID(),
});
```

Ticks must be nonnegative safe integers and cannot regress. They are application
logical time, not an automatically advancing platform clock or an expiry alarm.
The service chooses the unit and drives advancement; guest code does not call
`Date.now()`. Initialize the logical tick before opening sessions if using a large
external time origin. Only the owner-authenticated service may send control
commands; do not forward untrusted user-provided ticks or control frames.

Open and successful messages record the current tick as activity. Advancing to at
least `active_at + idleTicks` closes idle sessions and marks them expired. Explicit
close records its closure tick. Expiry consumes no message sequence. The original
receipt of a committed message may still replay after expiry; new mutations fail.
Retention starts at the committed closure/expiry tick, so a delayed advance does
not retroactively shorten retention.

## Reclamation

```js
const reclaim = durableSessionLifecycleFrame({
  kind: 'reclaim', generation: status.generation, operation_id: randomUUID(),
});
```

Reclamation requires a nonempty cohort, all sessions closed, and every closure past
`retentionTicks`. It clears the retained sessions and increments the durable
generation while preserving business state and logical tick. Up to 32 identities
fit in each generation. One active or recently closed session blocks reclamation;
this first implementation does not individually recycle slots. The generation
never wraps: exhaustion refuses reclamation.

Fresh requests carrying an old generation cannot mutate or reopen a reclaimed
session, even after their replay receipts are removed. Existing receipts may still
return their original old-generation result without dispatching a transition.
Reclamation does not delete platform replay receipts or promise a specific receipt
retention period. Loss of a socket is never proof that reclaim or another command
failed to commit; retry the same saved frame.

## Application migrations

```js
const definition = defineRecoverableDurableSessionEntity({
  namespace: 'my-rooms', idleTicks: 10, retentionTicks: 5,
  schemaVersion: 3,
  applicationMigrations: new Map([
    [2, old => ({total: old.count})], // application data only: version 2 -> 3
  ]),
  initialState: () => ({total: 0}),
  decodeState: decodeTotalState, decodePayload: decodeDelta,
  handle: ({state, payload}) => ({
    state: {total: state.total + payload}, result: state.total + payload,
  }),
});
```

Import the definition helper from the session module. Supply every application
migration from version 2 through the target version. Callbacks receive isolated
application data, perform no I/O and return JSON synchronously. Session generation,
tick, sequence, activity and tombstones remain outside their control. Invalid or
async migration output fails before publication. Later schema versions must be
declared in project metadata and matched by the isolated restore validator.

The built-in 1 -> 2 migration retains legacy session IDs, sequences and closed
flags at generation/tick zero. Closed timestamps and activity initialize to zero;
the operator must choose an advancement policy accordingly. Legacy committed
requests may still replay their old receipts. New commands use the lifecycle
generation-qualified format; uncommitted legacy frames are refused. Coordinate
the client upgrade and resolve pending legacy requests before switching contracts.
Historical state rollback can roll back a generation too; reclamation fencing
assumes forward recovery of the committed state, not reuse of an obsolete backup.

## Qualification

[CI run 38127768064](https://github.com/poyrazK/faas/actions/runs/38127768064)
passed for source `0fbfaf3116f18a0e8d8db7df3d0ae2cbb9fa557a`: all 15 focused SDK
tests, SDK compilation/build, the packed-starter suite with 8 nested cases, and
the Go package/CLI/authenticated-API checks with race detection. The Go workflow
now allows 30 minutes for cold builds and saves its own complete build cache.

Focused CI covers status snapshots versus replay, checkpoint recovery, lost
acknowledgements, activity-based expiry, retention boundaries, failed publication,
generation fences after receipt removal, legacy/application migrations, typed
clients and isolated starter restore validation. Native GCS/KVM restart and
multi-owner acceptance remain required. Production preview gates stay off.
