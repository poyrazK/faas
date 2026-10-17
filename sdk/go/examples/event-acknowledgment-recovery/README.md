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

Build/static validation does not qualify runtime recovery behavior. No local
tests are run; hosted behavioral qualification remains pending before release.
