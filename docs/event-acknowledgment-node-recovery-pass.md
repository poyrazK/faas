# Bounded Node callback recovery

`recoverEventAcknowledgmentIntents` runs one finite pass over an account/app's
saved callback intents. It composes the existing
[single-intent recovery primitive](event-acknowledgment-node-intents.md#recover-one-callback)
with bounded admissions and concurrency. The host decides when to invoke another
pass; importing the SDK starts no scheduler, poller or service.

```js
import { recoverEventAcknowledgmentIntents } from '@gregale/sdk-node';

const result = await recoverEventAcknowledgmentIntents(pool, accountId, appId, {
  signal: shutdown.signal,
  maxAttempts: 100,
  concurrency: 4,
});
```

Configure the authenticated Node SDK client/OpenAPI runtime as usual and use an
available application recovery host. The pass sends existing saved callbacks
without business work, preparation, platform renewal, ingestion-job acknowledgment
or a new negative decision. It preserves the existing database claim/token/expiry,
saved input hashes, platform attempt fence and conditional settlement protocol.

## Options and admissions

`EventAcknowledgmentIntentRecoveryPassOptions` extends the single-intent options:

| Field | Default | Bounds/meaning |
| --- | --- | --- |
| `maxAttempts` | 100 | Integer 1..10000; maximum admitted recovery iterations. |
| `concurrency` | 4 | Integer 1..32; maximum concurrent iterations. |
| `claimDurationMs` | 30000 | Existing database claim duration, whole milliseconds 1000..300000. |
| `backoffMs` | 1000 when `retryPolicy` is omitted | Fixed persisted unconfirmed-settlement delay, whole milliseconds 1..3600000. Omit with a policy. |
| `retryPolicy` | Omitted | Optional `EventAcknowledgmentIntentRetryPolicy` for capped exponential full jitter using the durable recovery-claim count. |
| `callbackOptions` | Omitted | Existing `EventAcknowledgmentWorkerOptions`, copied before I/O. |
| `signal` | Omitted | Optional borrowed `AbortSignal`; the pass never aborts it. |

Each admission reserves one slot before starting recovery. Empty probes, failed
acquisitions, interruptions and settled callbacks all consume slots. At most
`min(concurrency, maxAttempts)` worker promises run, each processing one iteration
at a time. The same event-loop thread checks and reserves the budget without an
await between those actions. No task or retained result list is created per row.

The work budget counts iterations, not HTTP requests, unique intents or successful
claims. Existing callback retry limits apply within each iteration. A due
unconfirmed intent can be admitted again within a pass. No per-pass ID tracking,
deduplication, row fairness or global ordering is introduced.

Options, nested retry policies and callback overrides must have current own data properties.
Unknown keys, symbols, accessors, malformed values and throwing descriptor traps
raise a generic `TypeError` before recovery starts; getters are not invoked.
Overrides are copied/frozen before I/O. Broad callback ranges are checked at entry;
the existing primitive checks the chosen row's actual lease before claim COMMIT.
A row-incompatible override fails without claiming that row. Omitted overrides
keep lease-dependent defaults.

See the [retry policy contract](event-acknowledgment-node-retry-policy.md) for
bounds, jitter and worker coordination. Policies are copied/frozen before I/O.
Combining a policy with a defined `backoffMs` is invalid before workers start.

Bound intents use their captured stored policy instead of this worker fallback.
Current intent recovery requires the upgraded columns even for unbound rows;
see [binding precedence and rollout](event-acknowledgment-stored-retry-policy.md).

The first empty rejection, owner abort, hard failure or uncertain commit closes
admission. Unconfirmed settlements, known parked outcomes and lost claims remain independent observations
and allow more admissions. Already admitted attempts drain after empty/hard
failures; owner abort cooperatively stops their remaining work. Results reflect
all admitted promises after they finish, including outcomes observed after
admission closes.

An empty probe sees no due unclaimed row for that iteration's `SKIP LOCKED` query.
Other hosts/iterations may hold claims; future due work and concurrent commits can
change the view. Neither `empty` nor `limit` proves a globally drained backlog. Parked rows require
explicit operator action and are excluded from automatic claim selection. See
[budget parking and resume](event-acknowledgment-retry-budgets.md#node-budgets-and-guarded-resume).
Use retained [intent health](event-acknowledgment-intent-health.md#node-summary)
and listing as independent evidence.

## Pool ownership and abort

Use the existing `EventConsumerPool` contract: `connect()` returns a dedicated
connection with promise-returning `query` and synchronous `release(discard?)`.
Pool connections must be exclusive until released. Each recovery iteration uses
the primitive's short claim, preflight and settlement transactions, which may use
different connections. The connection is released before HTTP; no pass-wide
transaction or callback-long connection scope is added. Size and bound the pool's
acquisition/query/statement calls for the configured concurrency.

The pass delegates query bytes/parameters and discard flags unchanged through a
per-admission pool guard. A synchronous release exception is recorded without
replacing the original operation error. It closes admission and aborts later work
in that iteration. A known returned settlement remains counted if its final
release fails. An uncertain COMMIT remains uncertain even when release also fails.
Release failures count once per admission and can overlap settlement observations.
The guard never retries an ambiguous release; the host must diagnose/discard a
broken pool through its own policy. Pools must honor the synchronous release
contract; asynchronous release callbacks are outside this interface.

Owner abort uses a fresh internal signal with a fixed reason. Caller-provided
`signal.reason`, transport bodies and driver causes never enter pass progress or
errors. Pre-aborted calls produce an interrupted zero-attempt result. Listeners
are detached after the pass. The pass awaits every admitted recovery promise and
its active driver work/release, using no result-winning race or worker cancellation.
Existing callback cancellation/deadline behavior still applies.

Cancellation is cooperative: bound driver acquisition, query and cleanup calls,
and finish joining the pass before closing pools/clients/event loops. A finite
work budget is not a wall-clock deadline. In-flight remote callbacks can outlive
local claim supersession; unchanged-input idempotency and platform fences remain
necessary.

## Frozen progress and failure categories

`EventAcknowledgmentIntentRecoveryPassResult` is a frozen object of fixed scalar
fields, without scope/job IDs, tokens, saved result bodies, action reasons,
per-intent statuses, connections or raw errors:

| Field | Meaning |
| --- | --- |
| `stopReason` | `limit`, `empty`, `interrupted`, `failed` or `commit_unknown`. |
| `attempts` | Admitted recovery iterations, including empty probes/acquisition failures. |
| `confirmed` | Returned locally committed confirmed settlements. |
| `unconfirmed` | Returned locally committed unconfirmed settlements with persisted backoff. |
| `parked` | Known parking mutations, including exhausted crashed claims without callback delivery. |
| `superseded` | Returned locally committed superseded settlements. |
| `empty` | Actual empty recovery rejections. |
| `claimLost` | Claims expired/replaced before a valid settlement returned. |
| `interrupted` | Iterations interrupted without a known returned settlement/probe outcome. |
| `failed` | Other iteration failures without a known returned outcome. |
| `commitUnknown` | Iterations reporting uncertain local COMMIT, including typed pool-release uncertainty. |
| `cleanupFailures` | Admissions with a failed pool release; can overlap known outcomes. |

Counters describe observations, not unique deliveries. Cleanup and typed
uncertainty can overlap known settlements; do not sum all fields into a delivery
total. Owner abort after all settlements can produce an interrupted pass with
zero interrupted iterations. Progress is not a durable audit or proof that an
unsettled callback failed or rolled back.

Normal return uses `limit` or `empty`. All admitted promises join before these
fixed exceptions are raised, each carrying frozen `.result`:

| Exception | Category and precedence |
| --- | --- |
| `EventAcknowledgmentIntentRecoveryPassCommitUnknownError` | Subclass of `EventConsumerCommitUnknownError`, retaining `code="event_consumer_commit_unknown"`; precedes cancellation and ordinary failures. |
| `EventAcknowledgmentIntentRecoveryPassCancelled` | Subclass of `DOMException` with `name="AbortError"`; precedes ordinary failures, which remain in progress. |
| `EventAcknowledgmentIntentRecoveryPassError` | Other hard failure, including release failure after a known settlement. |

The ordinary failure code is `event_acknowledgment_intent_recovery_pass_failed`.
If a selected generation exceeded Node's safe-integer limit and ordinary failure
is the primary category, its code is
`event_acknowledgment_intent_unsupported_generation`. That row is not claimed,
rounded or silently skipped; inspect retained metadata and use a compatible
recovery host. The pass preserves the existing Node generation limitation.

Error messages, JSON and inspection contain fixed categories/progress without raw
causes or caller abort reasons. Inspect fresh authoritative retained evidence
after unknown COMMIT or claim loss, and preserve original intent/job ownership.
No pass exception authorizes deleting fences, repeating business work, changing
intent actions or acknowledging an ingestion job.

The [Node host example](../examples/event-acknowledgment-node/README.md#recover-a-bounded-callback-pass)
demonstrates explicit scheduling and progress handling. Python provides the
[same bounded-pass model](event-acknowledgment-python-recovery-pass.md), using its
own connection-scope and native cancellation contracts.

Go also provides a [bounded recovery pass](event-acknowledgment-go-recovery-pass.md)
using `database/sql`, context cancellation and progress returned by value.

Local validation covers generation, TypeScript build/declarations, package
inclusion, example syntax, static admission/result/source comparisons and
schema/ADR/documentation checks. No tests/assertions are added or changed and no
local tests are run. Hosted concurrency, empty races, independent retries,
connection isolation/release failures, abort, generation limits and uncertain
commit qualification remains pending before release.
