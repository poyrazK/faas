# Node preparation lifecycle telemetry

The [Python preparation observer](event-acknowledgment-python-telemetry.md) uses the
same event vocabulary with frozen dataclass metadata and synchronous callbacks.

`runPreparedDurable` accepts an optional `onEvent` callback for application logs,
metrics and alerts. Events describe the local coordinator's phases and observed
outcomes. They contain no event body, prepared value, saved response, identity,
digest, acknowledgment/claim token, raw error or transport diagnostic.

```js
await worker.runPreparedDurable(pool,
  signal => prepareWithoutEffects(originalBody, signal),
  (tx, signal, prepared) => applyTransactionalWrites(tx, signal, prepared), {
    preparationTimeoutMs: 60000,
    processingLeaseMs: 30000,
    signal: job.signal,
    onEvent: event => { diagnosticQueue.tryEnqueue(event); },
  });
```

`diagnosticQueue` is an application-owned bounded adapter: `tryEnqueue` must return
immediately and may drop events when full. Drain it independently into your log or
metrics backend. The helper installs no queue, exporter, database ledger, alert
service or retry scheduler. See [coordinated processing and recovery](event-acknowledgment-node-preparation.md).

## Event contract

`EventAcknowledgmentPreparationEvent` is a discriminated union of frozen objects
with primitive fields. Every event has `kind` and `elapsedMs`, a nonnegative,
bounded whole-millisecond duration from operation admission using a monotonic
clock. It is not an event timestamp or a lease expiry. `onEvent` is captured once
when options are validated and does not change if the caller mutates its options.

| Kind | Additional fields | Observation |
| --- | --- | --- |
| `started` | None | Worker operation guard admitted the prepared operation, before its receipt-only probe. |
| `claim_acquired` | None | Claim COMMIT returned a new local processing capability; a completed-receipt observation emits no acquisition event. |
| `preparation_started` | None | Side-effect-free preparation is about to run. |
| `preparation_finished` | `outcome: returned\|threw`, `durationMs` | Preparation settled; returned data is never included. Return does not establish valid leases, successful completion or committed effects. |
| `renewal_failed` | `lease: local\|platform`, `reason` | First observed failure for that lease, including initial/final local refresh or platform startup after bounded retries. |
| `intent_committed` | `replayed: boolean` | Receipt validation/staging or final business completion returned a confirmed local receipt/intent transaction. |
| `commit_unknown` | `stage` | A typed unknown-commit error was observed. One event per operation; it grants no rollback inference. |
| `shutdown` | `stage`, `outcome`, optional `reason` | Owned renewal work has been joined and the prepared operation is about to settle. |

Successful heartbeat renewals emit nothing. Each lease reports at most one failure
per operation, so event volume is bounded by phase transitions rather than the
number of renewals. Parent cancellation does not manufacture a renewal failure
when a call observes an already-aborted operation. Unknown local renewal commits
are still reported even if cancellation arrived first.

`preparation_finished.durationMs` measures callback execution from after the start
observer returns until callback settlement, rounded down. `elapsedMs` includes
receipt reconciliation, acquisition, phase transitions and, at shutdown, joined
cleanup. Observers run on the same event loop: their own time contributes to the
operation's elapsed time and can consume its budget. Durations do not establish
database lease validity or a hard processing deadline.

## Stages, outcomes and reason codes

`EventAcknowledgmentPreparationStage` contains `receipt_reconciliation`,
`platform_start`, `claim`, `local_renewal`, `preparation`, `local_handoff` and
`completion`. Shutdown reports the foreground stage last reached. An uncertain
background local renewal reports `commit_unknown.stage: local_renewal`, even if
foreground preparation was still running.

`EventAcknowledgmentPreparationOutcome` contains:

| Outcome | Meaning |
| --- | --- |
| `committed` | A receipt/intent transaction returned confirmed. Callback delivery remains independent. |
| `commit_unknown` | The operation surfaced a typed unknown-commit error. Inspect its stage and reconcile through the existing protocol. |
| `cancelled` | The operation surfaced its parent's abort reason, with no confirmed/unknown commit outcome taking precedence. |
| `failed` | The operation failed, including preparation timeout, renewal failure or an application exception. This is not proof of rollback. |

Shutdown reason is omitted for `committed`; otherwise it is a fixed
`EventAcknowledgmentPreparationReason` literal. Known typed SDK errors map to
`preparation_timeout`, `claim_busy`, `local_lease_lost`, `local_operation_busy`,
`local_store_failed`, `platform_lease_lost`, `platform_unconfirmed`,
`receipt_conflict`, `receipt_missing`, `intent_conflict`, `intent_superseded` or
`intent_store_failed`. Unknown commits map to `commit_unknown`, parent cancellation
to `cancelled`, and all other errors to `failed`. Arbitrary error codes, messages,
causes, stacks and abort reasons are never copied into events.
An unrelated later parent abort does not replace the reason for an earlier failure.

An existing receipt normally produces `started`, `intent_committed` with
`replayed: true`, then `shutdown` with `committed`, skipping preparation. A new
claim adds acquisition and preparation events. Renewal failures may interleave
with foreground phases. Event delivery is synchronous in emission order, while
any accidentally asynchronous observer work can finish in a different order.

`intent_committed` can precede a late `renewal_failed` from the still-running
platform monitor. The final shutdown remains `committed`. The exact receipt and
intent stay confirmed even when cancellation arrives during cleanup. An unknown
claim/renewal/final commit remains uncertain and must use existing recovery;
telemetry never selects retry/dead-letter or changes acknowledgment state.

## Observer isolation and limits

Provide a synchronous, nonblocking observer. A synchronous throw is ignored, as
is an accidentally returned promise rejection. The helper does not await observer
promises, inspect their errors, retry notification or join them during shutdown.
The observer is called without a receiver and receives no capability or result
object. It must remain passive: do not control transactions, acknowledge jobs,
alter ownership, close shared resources or start recovery from this callback.

Frozen payloads prevent event mutation. Observer failures do not alter the
operation's returned result, exception, commit classification or renewal
cancellation. A blocking observer can still stall JavaScript timers and consume
deadlines; exception isolation cannot prevent blocking or arbitrary application
side effects. Separate worker instances still need exclusive durable-job ownership.

Invalid options, callbacks, pools, pre-aborted signals and busy-worker rejection
produce no lifecycle stream. A non-function `onEvent` throws `TypeError` before
database/platform access. Operations admitted to the guard emit `started` first
and `shutdown` last once cleanup can finish. Uncooperative preparation or driver
calls can delay shutdown; a process crash can omit events entirely.

Events are best-effort process-local observations, not durable audit evidence or
an acknowledgment outbox. They have no persisted sequence, replay guarantee or
restart deduplication. Use the retained receipt/intent protocols and
[read-only processing inspection](event-consumer-processing-inspection.md) for
recovery. The [protected-job example](../examples/event-acknowledgment-node/README.md#observe-coordinated-preparation)
forwards the option without changing job versions or ownership signals.

For fixed counters and cumulative duration histograms, attach the bound
[`EventAcknowledgmentPreparationMetrics.record`](event-acknowledgment-node-metrics.md)
observer. It retains no event identities or payloads and provides recursively
frozen snapshots for application-owned exporters. Shutdown outcome counts remain
separate from receipt replay and renewal-failure observations.

No schema, migration, route, generated API or registry change is needed. Local
validation covers build, generation, packaging and static checks; no tests or
assertions were added or changed and no local tests were run. Hosted acceptance
remains required for observer throws/rejections, ordering, secret exclusion,
cancellation, lease loss and confirmed/unknown commit boundaries before release.
