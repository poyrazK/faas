# Python preparation lifecycle telemetry

`run_prepared_durable` accepts an optional keyword-only `on_event` in
`EventAcknowledgmentPreparedOptions`. The observer receives frozen primitive
metadata about the local coordinator, following the
[Node event vocabulary](event-acknowledgment-node-telemetry.md). No event body,
prepared value, saved response, identity, digest, token, raw exception or transport
diagnostic enters an event.

```python
from dataclasses import asdict
from faas_sdk.preview import EventAcknowledgmentPreparedOptions

def observe(event):
    # Application-owned bounded queue. Never block or await in this callback.
    diagnostics.put_nowait(asdict(event))

queued = await worker.run_prepared_durable(
    connection_scope, prepare_without_effects, apply_transactional_writes,
    EventAcknowledgmentPreparedOptions(
        preparation_timeout_ms=60_000, on_event=observe,
    ),
    stop_event=job.stop_event,
)
```

The application owns queue capacity and independently drains it into a diagnostic
backend. Queue-full or observer exceptions are isolated and may drop observations.
No queue, exporter, durable audit ledger, transport or service is installed.
See the [coordinated preparation contract](event-acknowledgment-python-preparation.md).

## Frozen event contract

`EventAcknowledgmentPreparationEvent` is a union of frozen, slotted dataclass
variants with primitive fields. Use `event.kind` to distinguish them and `asdict`
when exporting metadata. Variant constructors are private implementation details.
Every event has `kind` and `elapsed_ms`, a nonnegative whole-millisecond duration
from coordinator start, before receipt reconciliation. It uses a monotonic clock
and saturates at 9,007,199,254,740,991, matching Node's exact integer bound. It is
neither an event timestamp nor lease-expiry evidence.

| Kind | Additional fields | Observation |
| --- | --- | --- |
| `started` | None | Owned prepared operation began, before the receipt-only probe. |
| `claim_acquired` | None | Claim returned a new local capability; a matching-receipt observation emits no acquisition event. |
| `preparation_started` | None | Side-effect-free preparation is about to run. |
| `preparation_finished` | `outcome: returned\|threw`, `duration_ms` | Callback settled. Return establishes neither lease validity nor committed effects. |
| `renewal_failed` | `lease: local\|platform`, `reason` | First observed failure for that lease, including startup or initial/final local refresh. |
| `intent_committed` | `replayed: bool` | Receipt validation/staging or new business completion returned confirmed local receipt/intent commit. |
| `commit_unknown` | `stage` | A typed uncertain-COMMIT outcome was observed, once per operation. |
| `shutdown` | `stage`, `outcome`, `reason` | Coordinator renewal/forwarding work has joined and the operation is about to settle. |

Python's `shutdown.reason` is `None` for committed outcomes (and appears as JSON
null with `asdict`); Node omits it. Other fields follow the same vocabulary with
snake-case duration names. Successful heartbeat renewals emit nothing. Each lease
reports at most one failure, bounding a lifecycle to nine events regardless of
preparation duration or renewal count. Already-signalled cancellation/timeout
does not manufacture renewal failures. Unknown local renewal commits remain
reportable even after cancellation.

`preparation_finished.duration_ms` measures callback execution after the start
observer returns, rounded down and saturated at the same bound. It excludes the
start/finish observers' execution. `elapsed_ms` includes prior observer time,
receipt queries, acquisition and, at shutdown, coordinator cleanup. Blocking
observers can consume preparation deadlines or stall renewal timers. Durations
provide diagnostics, not safe processing budgets or database-clock fences.

## Stages, outcomes and reasons

`EventAcknowledgmentPreparationStage` contains `receipt_reconciliation`,
`platform_start`, `claim`, `local_renewal`, `preparation`, `local_handoff` and
`completion`. Shutdown uses the last foreground stage. An uncertain background
local renewal reports `commit_unknown.stage: local_renewal` even while foreground
preparation was still running.

`EventAcknowledgmentPreparationOutcome` contains:

| Outcome | Meaning |
| --- | --- |
| `committed` | A receipt/intent transaction returned confirmed; callback delivery remains independent. |
| `commit_unknown` | A typed unknown-COMMIT error surfaced. Reconcile through the existing protocol. |
| `cancelled` | SDK/native cancellation surfaced without confirmed/unknown commit taking precedence. |
| `failed` | Another failure surfaced, including timeout, lease loss or application error. This proves no rollback. |

`EventAcknowledgmentPreparationReason` is limited to `cancelled`,
`preparation_timeout`, `claim_busy`, `local_lease_lost`, `local_operation_busy`,
`local_store_failed`, `platform_lease_lost`, `platform_unconfirmed`,
`receipt_conflict`, `receipt_missing`, `intent_conflict`, `intent_superseded`,
`intent_store_failed`, `commit_unknown` and `failed`. Known SDK error classes/codes
map to these literals. Arbitrary messages, codes, causes, tracebacks, HTTP statuses
and abort reasons are never copied. An unrelated later cancellation cannot
reclassify an earlier failure; the surfaced operation outcome decides shutdown.

An existing receipt normally yields `started`, `intent_committed` with
`replayed: True`, then committed `shutdown`, skipping preparation. New processing
adds acquisition and preparation events. Renewal failures may interleave with
foreground phases. A confirmed local commit remains committed despite late
renewal loss or cancellation. Unknown claim/renewal/final commits retain their
existing precedence, including uncertainty discovered while joining renewal.
Telemetry chooses no acknowledgment/recovery action.

## Observer isolation and lifecycle

`on_event` is captured once with the effective options before I/O. `None` disables
delivery; a non-callable raises `ValueError` before database/platform access.
Existing positional timeout/lease options remain valid. The callback is excluded
from options repr. Invalid options, invalid callbacks/factories, busy-worker or
pre-aborted rejection produce no stream. Cancellation before the owned operation
begins can also omit the stream. Operations that reach the coordinator emit
`started` first and `shutdown` last after their cleanup can finish.

Provide a synchronous, nonblocking, passive observer. It must not acknowledge
jobs, control transactions, cancel tasks, change ownership, close shared resources
or start recovery. Synchronous failures, including a raised cancellation exception,
are swallowed without changing processing results, exceptions or renewal signals.
Exception isolation cannot prevent blocking or arbitrary application side effects.
Failure notifications run outside active exception handlers so an observer's own
exception cannot accidentally inherit the processing failure as implicit context.

The SDK never awaits, schedules or drives observer work. An accidentally returned
unstarted native coroutine is closed without execution. Already-running
`asyncio.Future`/Task failures are consumed without retaining their errors; those
borrowed futures are never cancelled or joined. Other awaitables are ignored.
Do not use an async observer: put primitive metadata into a bounded local queue
and drain it independently. Async observer work is not part of coordinator
shutdown. An observer that never returns can delay cleanup indefinitely.

Events are best-effort process-local observations, without durable sequence,
replay, restart deduplication or acknowledgment semantics. A crash can omit events
entirely. Use retained receipts/intents and
[read-only inspection](event-consumer-processing-inspection.md#python-inspection)
for recovery. Separate worker instances still require exclusive durable-job
ownership and cooperative handlers.

The [protected-job example](../examples/event-acknowledgment-python/README.md#observe-coordinated-preparation)
uses an explicit bounded `asyncio.Queue` without changing job versions or ownership.
No schema, migration, endpoint or registry change is needed. Local validation
covers compilation/lint, Python 3.11 syntax, generation, package inclusion and
static vocabulary/source checks. No tests/assertions are added/changed and no
local tests run. Hosted observer isolation, event ordering, secret exclusion,
cancellation, renewal loss and confirmed/unknown commit acceptance remains pending
before release.

For fixed counters and duration histograms, pass the synchronous
[`EventAcknowledgmentPreparationMetrics.record`](event-acknowledgment-python-metrics.md)
method as the observer and export detached snapshots separately. Its observations
do not establish stream completeness or recovery proof.
