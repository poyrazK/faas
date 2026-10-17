# Go callback recovery host example

[`RecoverCallbacks`](recovery.go) runs one pass of at most 100 saved-callback
iterations with four concurrent workers, a 30-second database claim and a
one-second persisted unconfirmed backoff. It is a library example built by the
standalone Go SDK's `go build ./...`; it opens no database or HTTP listener.

The application supplies a PostgreSQL driver and configured `*sql.DB`,
authenticated `*faas.Client`, trusted account/app UUIDs and an available recovery
host. Install the application-owned receipt/intent schemas explicitly and protect
saved capabilities/results. See the [Go intent protocol](../../../../docs/event-acknowledgment-intents.md).
This example adds no external dependencies to the SDK.

Call the function under the host's shutdown/deadline context. Configure bounded
pool/statement/transport operations and keep the pool/client available until the
function returns. Process progress before deciding when another pass should run:

```go
progress, err := recovery.RecoverCallbacks(ctx, db, client, accountID, appID)
// Export only progress's fixed scalar fields, never saved intent/job contents.
switch {
case errors.Is(err, faas.ErrEventConsumerCommitUnknown):
    // Preserve retained evidence; reconcile before explicitly scheduling again.
case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
    // All admitted workers have joined. The host can now close its resources.
case err != nil:
    // Inspect progress.Failed and restore storage/configuration access.
default:
    // Inspect Unconfirmed/ClaimLost; empty does not prove global drain.
}
// Hand progress and err to the application's recovery policy.
```

Imports in this sketch are `context`, `errors`, the SDK as `faas` and this example
as `recovery` (`github.com/poyrazK/faas/sdk/go/examples/event-acknowledgment-recovery`).
Several error sentinels can match; unknown commit takes precedence. A nil error
does not mean all callbacks were confirmed. A committed unconfirmed settlement
is counted even when its callback timed out; it does not fail the entire pass.

The host owns future scheduling, retry policy and shutdown. The pass never runs
business work, renews platform processing, acknowledges an ingestion job or
chooses a new negative action. Review the
[full pass contract](../../../../docs/event-acknowledgment-go-recovery-pass.md)
for option bounds, settlement/error precedence and cooperative cancellation.

## Persist recovery alert checkpoints

[`WatchRecoveryAlerts`](checkpoint.go) accepts an optional saved checkpoint and an
application implementation of `RecoveryAlertHost`. Acquire one storage writer
fence before loading/resuming and retain it through final reconciliation/save.
The host's implementation supplies atomic fenced checkpoint storage, a durable
nondestructive acknowledgment ledger and idempotent notification delivery.

The example saves the **whole** checkpoint before sending any pending entry.
Only confirmed destination delivery is recorded in the acknowledgment ledger.
The watch polls that ledger with exact incident/sequence pairs and persists
updated receipts in subsequent progress. Replay can repeat a destination send
after a crash; deduplicate by account/app, incident ID and sequence. Deliver the
ordered `Checkpoint.Pending` list, including startup replay, rather than only the
latest `Alerts` slice.

Load bounded trusted JSON into `faas.EventAcknowledgmentIntentCheckpoint`, then
validate it with `ValidateEventAcknowledgmentIntentCheckpoint`. Pass the same
effective watch policy on resume. No platform client is needed for monitoring.
The example's thresholds are 100 due intents and one stalled intent with a
30-second sustain; ordinary Go defaults supply the remaining settings.

Inspect both returned result and error. `errors.As` can read the fixed
`*faas.EventAcknowledgmentIntentCheckpointWatchCallbackError.Phase`; no raw host
cause is retained. A failed checkpoint save may already have committed: compare
current fenced storage with the returned checkpoint before notification retry.
Do not blindly overwrite it or reset the session/sequence.

On cancellation, active reads/callbacks have finished and the returned result
contains the final checkpoint. Before releasing ownership, reconcile/persist it
using a separate bounded shutdown context, for example
`context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)`; always call its
cancel function. The storage implementation must reject an expired/stale writer
even during shutdown. A pre-canceled valid watch returns its retained state
without invoking host callbacks. No incident is resolved or acknowledged merely
because the watch stops.

See the [Go checkpoint contract](../../../../docs/event-acknowledgment-go-checkpoints.md)
for outbox capacity reservations, exact acknowledgment validation and fresh proof.
The example is built with the SDK and installs no storage or transport itself.

Build/static validation does not qualify runtime recovery behavior. No local
tests are run; hosted behavioral qualification remains pending before release.
