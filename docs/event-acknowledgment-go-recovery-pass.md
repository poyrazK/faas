# Bounded Go callback recovery

`RecoverEventAcknowledgmentIntents` runs one finite pass over an account/app's
saved callback intents. It composes the existing
[single-intent recovery primitive](event-acknowledgment-intents.md#recover-one-callback)
with bounded admissions and concurrency. The host owns invocation, future
scheduling and process lifetime; importing the SDK starts no recovery service.

```go
progress, err := faas.RecoverEventAcknowledgmentIntents(ctx, db, client,
    accountID, appID, faas.EventAcknowledgmentIntentRecoveryPassOptions{
        MaxAttempts: 100,
        Concurrency: 4,
    })
```

Use an authenticated SDK client, trusted account/app UUIDs and an application
PostgreSQL `*sql.DB` with the existing receipt/intent tables installed explicitly.
The pass sends saved callbacks without business work, preparation, platform
renewal, ingestion-job acknowledgment or a new negative decision. It preserves
the existing saved-input hashes, database claim/token/expiry, platform attempt
fence and conditional settlement protocol. Go retains full `int64` replay
generations; the Node safe-integer restriction does not apply to Go.

## Options and admissions

Options, including a supplied retry policy, are copied before I/O. Zero values
select the defaults below:

| Field | Default | Bounds/meaning |
| --- | --- | --- |
| `MaxAttempts` | 100 | 1..10000 admitted recovery iterations. |
| `Concurrency` | 4 | 1..32 concurrent iterations. |
| `ClaimDuration` | 30 seconds | Whole milliseconds, one second through five minutes. |
| `Backoff` | One second when `RetryPolicy` is nil | Fixed fallback unconfirmed-settlement delay, whole milliseconds from one millisecond through one hour. Leave zero with a policy. |
| `RetryPolicy` | Nil | Optional fallback `EventAcknowledgmentIntentRetryPolicy` for capped exponential full jitter based on the durable claim count. |
| `CallbackOptions` | Zero worker options | Existing `EventAcknowledgmentWorkerOptions`, including lease-relative defaults. |

See the [retry policy contract](event-acknowledgment-go-retry-policy.md) for bounds,
jitter and worker coordination. Supplying both a policy and nonzero `Backoff` is
invalid. Policy values are normalized before any claim or worker starts.

Captured stored policies override worker fallback on bound intents. Upgrade
the intent schema before using current Go recovery; see
[stored policies and rollout](event-acknowledgment-stored-retry-policy.md).

Nil context/database/client, invalid scope, out-of-range options and broadly
invalid callback overrides return `ErrInvalidEventConsumerRequest` with zero
progress before recovery starts. Nonzero callback overrides must also satisfy
the selected intent's advertised lease. The existing Go `Deliver` primitive
checks that after acquiring the claim; an incompatible override stops the pass
and leaves the saved callback recoverable after that claim expires. Keep zero
callback fields for lease-relative defaults across intents with different leases.

At most `min(Concurrency, MaxAttempts)` worker goroutines run, each recovering one
iteration at a time. A mutex protects the admission check, attempt reservation,
stop flag and counters. Each slot is reserved before calling the existing
`RecoverNextEventAcknowledgmentIntent` or its `WithRetryPolicy` variant; no mutex
is held during database or HTTP work. No goroutine or retained result list is
allocated per intent.

Empty probes, acquisition failures, interrupted iterations and settled callbacks
all consume slots. The work budget counts iterations rather than HTTP requests,
unique intents or successful claims. Callback retry limits still apply within
each iteration. A due unconfirmed intent can be admitted again in the same pass;
there is no per-pass ID set, row fairness or new ordering/deduplication guarantee.

The first recorded empty probe, interruption, hard failure or uncertain commit
closes admission. Other already admitted iterations drain using the supplied
context. Unconfirmed settlements and lost claims allow further admissions.
An empty probe observes only its query's due/unclaimed `SKIP LOCKED` view: other
workers may hold claims and future due work may remain. Neither `empty` nor
`limit` establishes a globally drained backlog. Inspect retained
[intent health](event-acknowledgment-intent-health.md) separately.

## Connection ownership and cancellation

`*sql.DB` supplies concurrent connections through its standard pool. The existing
recovery primitive owns each short claim transaction and its preflight/settlement
statements. No pass-wide transaction or callback-long connection reservation is
added. Configure pool capacity/acquisition limits, database statement timeouts
and an HTTP transport compatible with the chosen concurrency. Do not mutate the
client's configuration or close the database/client while a pass is active.

Cancellation is cooperative through the supplied `context.Context`. A valid
pre-canceled call returns `interrupted`, zero attempts and the context sentinel
without creating workers. Cancellation during a pass prevents later admissions
and reaches admitted database/HTTP work. The pass waits for every worker before
returning; it does not return early when the context becomes done. Drivers and
transports must honor context/timeout contracts. A finite iteration budget alone
does not impose a wall-clock deadline or force a blocked driver to return.

No pass-owned background worker survives return. Close pools/clients only after
joining the call. In-flight remote callbacks may complete despite cancellation
or claim supersession; the existing unchanged-input idempotency and platform
attempt fences remain necessary. The pass does not inspect or export
`context.Cause`, caller cancellation reasons, driver errors or response bodies.

## Progress and errors

`EventAcknowledgmentIntentRecoveryPassResult` contains ten scalar fields, returned
by value with snake-case JSON tags. It retains no per-intent status, IDs, action
reasons, tokens, payloads, connections or raw errors:

| Field | Meaning |
| --- | --- |
| `StopReason` | `limit`, `empty`, `interrupted`, `failed` or `commit_unknown`. |
| `Attempts` | Admitted iterations, including empty probes and acquisition failures. |
| `Confirmed` | Returned locally committed confirmed settlements. |
| `Unconfirmed` | Returned locally committed unconfirmed settlements with persisted backoff. |
| `Parked` | Known committed exhaustion parking, including expired crashed claims; no automatic redelivery until explicit resume. |
| `Superseded` | Returned locally committed superseded settlements. |
| `Empty` | Empty recovery probes. |
| `ClaimLost` | Expired/replaced claims without a returned settlement. |
| `Interrupted` | Interrupted iterations without a returned settlement/probe outcome. |
| `Failed` | Other iterations without a returned outcome. |
| `CommitUnknown` | Iterations reporting `ErrEventConsumerCommitUnknown`. |

Each admitted iteration contributes to one outcome counter. `Deliver` may return
both a committed status and a callback error: the pass counts the known settlement
and continues. In particular, a callback timeout with a committed `unconfirmed`
settlement is not a hard pass failure or owner cancellation. Supersession due to
an unavailable platform lease or a committed receipt remains a known settlement.
Raw per-callback errors are not retained. Uncertain commit is classified before
any other iteration observation.

Go [v2 retry budgets](event-acknowledgment-retry-budgets.md) can park exhausted
callbacks. Parking consumes a pass admission and permits further admissions;
already parked rows are not selected again. Empty can coexist with parked work
requiring operator action. Current Go helpers require the budget schema upgrade
even for unbound/v1 work.

Normal completion returns nil error with `limit` or `empty`; always inspect
progress, since nil does not mean every callback was confirmed. An owner
cancellation observed after known settlements can return `interrupted` with a
zero `Interrupted` counter. All progress reflects admitted work after it joins.

`StopReason` precedence is unknown commit, cancellation/interruption, hard
failure, empty, then limit. The error joins only fixed sentinels:

| `errors.Is` target | Meaning |
| --- | --- |
| `ErrEventConsumerCommitUnknown` | At least one claim COMMIT had an uncertain outcome. |
| `context.Canceled` / `context.DeadlineExceeded` | Caller cancellation/deadline or an observed unsettled interruption. |
| `ErrEventAcknowledgmentIntentRecoveryPassFailed` | At least one other hard iteration failure. |

Several targets can match simultaneously. Check unknown commit first, inspect
all progress and reconcile authoritative retained evidence before rescheduling.
Known settlements from other workers are preserved alongside errors. Standard
`database/sql` cleanup errors are not separately exposed by the underlying Go
primitive, so this result has no Node/Python `cleanupFailures` counter.

A store failure or missing settlement does not prove rollback or failed remote
delivery. Claim COMMIT uncertainty returns no usable claim; let the possibly
committed claim expire and inspect retained evidence. Never use pass progress or
errors as authority to rerun business work, change saved actions, acknowledge an
ingestion job, delete fences or restore an in-memory claim.

The [Go host example](../sdk/go/examples/event-acknowledgment-recovery/README.md)
shows explicit scheduling and error handling. The
[Node](event-acknowledgment-node-recovery-pass.md) and
[Python](event-acknowledgment-python-recovery-pass.md) passes use the same finite
admission model with their own connection and native cancellation contracts.

Local validation covers the standalone SDK/example build, vet, source admission,
result/error and shared schema checks, plus documentation checks.
No tests/assertions are added or changed and no local tests are run. Hosted
concurrency, empty races, independent retries, connection isolation, cancellation
and uncertain-commit qualification remains pending before release.
