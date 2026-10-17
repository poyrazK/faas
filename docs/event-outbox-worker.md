# Graceful application outbox recovery worker

The Go SDK provides a blocking, caller-owned worker pool around application outbox
recovery. It does not install tables or start a service automatically. Install the
[outbox schema](event-outbox.md) and any enabled [retry-policy upgrade](event-outbox-retry-policy.md)
first, then run the worker in the application's background process.

```go
workerCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

err := faas.RunEventOutboxWorker(workerCtx, db, client, accountID, appID,
    faas.EventOutboxWorkerOptions{
        Concurrency: 4,
        ShutdownGrace: 30 * time.Second,
        Recovery: faas.EventOutboxRecoveryOptions{
            RetryPolicy: &faas.EventOutboxRetryPolicy{
                InitialBackoff: time.Second,
                MaxBackoff: 5 * time.Minute,
                MaxAttempts: 20,
            },
        },
    }, func(callbackCtx context.Context, event faas.EventOutboxWorkerEvent) {
        // Record locally or hand off promptly, honoring callbackCtx.Done().
        // Callbacks can run concurrently; shared state must be synchronized.
    })
if err != nil && !errors.Is(err, context.Canceled) {
    return err
}
```

Keep the SQL database and client usable until the worker returns. The client must
be configured for the saved owner and the existing publication authorization and
expected-app-ID contracts. The callback is optional; pass nil to run without
notifications. Call the function directly in a dedicated process, or supervise it
as part of the application's existing goroutine lifecycle.

## Concurrency, polling and timeouts

Each goroutine recovers at most one row at a time, using the existing
`FOR UPDATE SKIP LOCKED` claim path and fenced settlement. Concurrency bounds
recovery calls, not database connections globally, request rate, or other worker
processes. Provision the SQL connection pool and application infrastructure
accordingly. Multiple processes can share the outbox under the existing leases.

| Option | Default | Supported range |
| --- | --- | --- |
| Concurrency | 1 | 1–32 |
| IdleMin | 250 ms | At least 10 ms, no greater than IdleMax |
| IdleMax | Greater of 5 seconds and IdleMin | Up to one minute |
| ErrorBackoff | One second | 10 ms–one minute |
| OperationTimeout | One minute | One second–10 minutes |
| ShutdownGrace | 30 seconds | One millisecond–five minutes |

The nested Recovery options retain their existing lease, request-timeout,
backoff/policy, and attempt-limit validation. Worker configuration and a copy of
its retry policy are validated before goroutines start. Workers recovering unbound publications should use consistent retry settings.
Bound publications use their persisted policy; worker retry settings cannot
override it.

Empty or scope-paused polls wait IdleMin, then double the wait up to IdleMax. A nonempty result or
error resets idle backoff. Published, blocked, and durably unconfirmed results
allow immediate progress on another row. Storage errors, lost claims and other
operation errors wait ErrorBackoff to avoid a hot failure loop. Poll waits stop
promptly on parent cancellation. No callback is generated for an empty poll.

Each iteration's claim and delivery contexts start with the OperationTimeout
budget. That budget covers database work, HTTP, settlement and callback time;
existing HTTP request, lease, retry-window and parent claim deadlines can shorten
it. There is no lease renewal or business-handler execution. Idle backoff can
add up to IdleMax polling latency before newly due work is noticed.

## Shutdown and drain

Parent cancellation or deadline stops further claim attempts and cancels active
claim SQL. A claim that committed at the cancellation boundary may still proceed
as in-flight work. Unknown commit outcomes remain uncertain and must be reconciled;
the worker does not assume a canceled claim never committed.

Committed claims use a context that preserves parent values while intentionally
detaching parent cancellation/deadline. This gives in-flight publication,
settlement, and callbacks up to ShutdownGrace to finish; their own operation,
request and lease deadlines continue to apply. Once grace expires the worker
cancels this drain context and waits for all goroutines to exit. It returns the
parent context error, including context.Canceled after a graceful signal shutdown.

Grace is a cancellation deadline, not forced termination. Database drivers,
transports and synchronous callbacks must honor contexts and return promptly.
The function does not abandon background goroutines to return early. A callback
that ignores cancellation can keep shutdown waiting. Callback panics are not
recovered by the SDK. After grace cancellation prevents settlement, an active
claim can remain until its original lease expires; later recovery preserves the
same key, request and attempt count.

## Local outcome callbacks

Callbacks run synchronously on the worker goroutines, concurrently across the
pool. They consume capacity and must be concurrency-safe, bounded and cooperative
with the provided delivery context, which may already be canceled. The SDK does
not create a transport, persist notifications, or retry callbacks.

| Outcome | Meaning |
| --- | --- |
| `paused` | Scope pause observed before claiming; once per worker per consecutive paused polling episode |
| `published` | Gregale acceptance was confirmed and local settlement returned successfully |
| `unconfirmed` | Uncertainty was saved with a later retry schedule |
| `blocked` | A deadline, attempt limit or permanent rejection blocked the row |
| `claim_lost` | Lease preflight or settlement lost its claim fence |
| `storage_error` | Storage/claim commit failed; remote or local outcome can be uncertain |
| `error` | Another operation error, including cancellation or timeout |

ErrorCode provides stable local details: `commit_unknown`, `store_failed`,
`operation_timeout`, `operation_canceled`, `claim_lost`, `publishing_paused`, `unconfirmed`, a blocked
row's LastCode, or `recovery_failed`. Storage failures take precedence over an
unconfirmed error when settlement could not be saved. Ordinary canceled claim
operations during shutdown are suppressed; uncertain commits and failures after
claiming remain reportable.

Events include owner IDs, a process-local worker number, local notification time,
and producer key when known. Status is optional: failed settlement or an unknown
claim commit may provide a key without a local result snapshot. A snapshot can
be stale by callback time. Metadata can contain business identifiers; event
payloads, credentials, tokens and raw server/driver errors are excluded.

Published confirms durable acceptance, not consumer completion. Callback delivery
is not durable or exactly once. Use existing outbox status, health, reconciliation
and control history for investigation. In particular, callback absence or a
storage error never proves an event was not accepted. Recovery continues after
per-row and storage failures until cancellation; operators can cancel from their
own supervisor when intervention is required. No new ordering, retention or
business-side-effect guarantee is introduced.

## Scope pause

Workers honor [durable publishing controls](event-outbox-publishing-control.md)
before each claim. Paused polls do not consume attempts and back off like idle
polls. Local paused callbacks are observations, not durable scope-change events;
concurrent workers may each report the pause, and errors can start a new episode.
A resume is noticed on a later poll, with up to IdleMax polling delay. Already
committed claims can settle after a pause. Install the control schema and replace
older workers before relying on this fence.

## Shared publishing rate limits

[Durable rate limits](event-outbox-rate-limit.md) coordinate claim capacity across
workers. Rate-limited polls back off without spending attempts. The optional
callback emits outcome `rate_limited`, code `publishing_rate_limited`, and capacity
observation fields RetryAt/RetryAfterMillis once per worker per consecutive
throttled episode. Poll delay is capped at IdleMax. Pause takes priority; existing
committed claims continue. Install rate storage and replace older workers first.
