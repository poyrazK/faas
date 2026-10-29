# Go callback recovery host example

[`RecoverCallbacks`](recovery.go) runs one pass of at most 100 saved-callback
iterations with four concurrent workers, a 30-second database claim and adaptive
unconfirmed backoff. For unbound intents its initial cap is one second and its
maximum is five minutes; full jitter selects one millisecond through the current cap.
The due time and claim count persist in PostgreSQL. It is a library example built
by the standalone Go SDK's `go build ./...`; it opens no database or HTTP listener.

The application supplies a PostgreSQL driver and configured `*sql.DB`,
authenticated `*faas.Client`, trusted account/app UUIDs and an available recovery
host. Install the application-owned receipt/intent schemas explicitly and protect
saved capabilities/results. See the [Go intent protocol](../../../../docs/event-acknowledgment-intents.md).
This example adds no external dependencies to the SDK.

The [retry policy contract](../../../../docs/event-acknowledgment-go-retry-policy.md)
explains durable counts, worker coordination and failed-settlement uncertainty.
For a fixed one-second delay, omit `RetryPolicy` and set `Backoff: time.Second`.
Combining a policy and nonzero `Backoff` is invalid. This policy changes future
saved-callback scheduling; callback-level retries retain their existing bounds.

[`StageCompletionWithPolicy`](staging.go) accepts a worker reconstructed from a
protected authenticated job and a transactional SQL-only handler. It stages
business writes, receipt, completion and a versioned policy atomically. Current
Go recovery captures that policy under the row lock and uses it instead of the
example's worker fallback. Inspect `queued.Intent.RetryPolicy` as metadata.

Apply the explicit stored-policy upgrade before upgrading existing Go intent
workers. Enable bound staging only when every recovery host serving that scope
supports bindings; updated Go, Node and Python workers do. See
[schema, repeated saves and rollout](../../../../docs/event-acknowledgment-stored-retry-policy.md).

[`AdoptCallbackPolicy`](adoption.go) binds the same policy to an explicitly selected
existing unbound intent. Supply the original metadata and a saved command UUID;
install the optional adoption receipt schema first. A live claim, changed
timestamp/count, terminal state or existing binding rejects adoption. Keep the
original inputs when resolving unknown commits. See the
[adoption and receipt contract](../../../../docs/event-acknowledgment-policy-adoption.md).

[`StageCompletionWithRetryBudget` and `ResumeCallbackBudget`](retry_budget.go)
show an initial eight-claim binding and explicit guarded resume from selected
metadata. Install the budget upgrade before current Go helpers, the optional
resume receipt schema before controls, and use budget-aware Go hosts for enabled
scopes. Recovery progress and health expose parked work independently. See
[budgets, parking and command replay](../../../../docs/event-acknowledgment-retry-budgets.md).

Call the function under the host's shutdown/deadline context. Configure bounded
pool/statement/transport operations and keep the pool/client available until the
function returns. Process progress before deciding when another pass should run:

```go
progress, err := recovery.RecoverCallbacks(ctx, db, client, accountID, appID)
// Export only progress's fixed scalar fields, never saved intent/job contents.
switch {
case errors.Is(err, preview.ErrEventConsumerCommitUnknown):
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

Load bounded trusted JSON into `preview.EventAcknowledgmentIntentCheckpoint`, then
validate it with `ValidateEventAcknowledgmentIntentCheckpoint`. Pass the same
effective watch policy on resume. No platform client is needed for monitoring.
The example's thresholds are 100 due intents, one stalled intent and one parked
intent with a 30-second sustain; ordinary Go defaults supply the remaining settings.
It uses a version 2 checkpoint because parked alerts are enabled. Every host that
can resume or acknowledge it must understand version 2. Set `ParkedThreshold: 0`
to retain the previous version 1 selection and resume its saved checkpoints.
Changing the selection requires a new session after reconciling/retaining the old
session's incidents and notifications; do not edit a saved version in place.
`parked_intents` recovery means only that parked count stayed below threshold,
including after an operator resume or supersession. Missing/incomplete evidence
cannot resolve an incident. See the
[parked alert contract](../../../../docs/event-acknowledgment-go-checkpoints.md#parked-intent-alerts).

Inspect both returned result and error. `errors.As` can read the fixed
`*preview.EventAcknowledgmentIntentCheckpointWatchCallbackError.Phase`; no raw host
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

## Adopt a budget for existing backlog

[`BudgetAdoptionCommand` and `ApplyBudgetAdoption`](budget_adoption.go) separate
selection from execution. Authorize the selected owner/key and total, then durably
save the complete original command before calling. Nil expected policy means
unbound; v1 metadata is copied and its timing preserved. The total exceeds consumed
claims without resetting them. Install the separate budget-adoption ledger first.
A live claim, changed timestamp/count/policy, v2/terminal row or newer attempt
rejects adoption with a retained outcome. After uncertainty, retry the saved
inputs rather than refreshing a fence. See
[installation, concurrency and receipt identity](../../../../docs/event-acknowledgment-budget-adoption.md).

## Export recovery health to Prometheus

[`NewRecoveryHealthMetricsHost`](recovery_metrics.go) builds one scoped cache and
an unbound `net/http` server. Its `Run(ctx)` method explicitly starts sequential
metadata polling: scan 1000, stall five minutes, query five seconds, delay five
seconds after each completed read, maximum age thirty seconds. Construction starts
no SQL, goroutine or listener and requires no platform client/credential.

The application supplies its configured PostgreSQL `*sql.DB` and trusted owner
UUIDs, supervises `Run` and `Server.ListenAndServe` separately, and treats an
unexpected exit from either as a host failure. The default listener is
`127.0.0.1:9468`, distinct from Node/Python recovery and preparation examples.
Prometheus must share the namespace or use an explicitly protected reachable
address. The example sets bounded HTTP header/read/write/idle timeouts; configure
database connect/statement/release bounds as well. Constructors start neither loop.

Cache methods serialize concurrent poll/scrape access. The HTTP adapter formats a
new detached snapshot for `GET /metrics`, performs no SQL, and omits counts after
read failure. Stop polling with its caller context, shut down HTTP using a
separate bounded context, join both loops, then close the database pool. If
graceful HTTP shutdown times out, explicitly close the server and join its active
handlers before releasing owned resources. An unresponsive database driver must
return before `Run` can finish; no active query is raced or abandoned.

Use the [Go scrape job](../../../../examples/event-acknowledgment-monitoring/prometheus-recovery-go.yml)
and [shared recovery setup](../../../../examples/event-acknowledgment-monitoring/README.md#recovery-health-go).
Keep owner mapping in authorized host configuration, outside metric labels. Each
target observes one retained database scope; duplicate observers cannot be summed
into a global backlog. Missing/incomplete/stale evidence cannot establish health,
and a Prometheus resolved notification can follow evidence loss. See
[the Go metrics contract](../../../../docs/event-acknowledgment-go-recovery-prometheus.md)
and [runbook](../../../../docs/runbooks/GregaleEventRecovery.md#go-hosts-and-guarded-recovery).
Hosted behavioral qualification for concurrency, clocks, cancellation, scraping
and alert evaluation remains pending before production use.

## Observe actual recovery worker activity

[`NewRecoveryWorkerMetricsHost`](worker_metrics.go) composes the existing health
host with a separate scoped worker cache and combined handler on loopback 9468.
Use this host instead of the health-only host for that endpoint. Construction
starts nothing; explicitly supervise `RunHealth`, HTTP and application scheduling
of `RecoverCallbacks(ctx, client)`. The latter uses the monitored finite pass:
100 admissions, four workers, a 30-second claim and adaptive backoff.

The default expectations are first/next pass within one minute and active pass
return within five minutes. Tune them for the application's schedule; they do
not impose deadlines or start a scheduler. Process the original progress/error
before deciding whether to invoke another pass, preserving unknown-COMMIT and
conditional-claim recovery. Only one pass can use this observation cache at once.
Healthy HTTP scrapes or `RunHealth` reads cannot refresh its activity.

Copy [prometheus-recovery-go-worker.yml](../../../../examples/event-acknowledgment-monitoring/prometheus-recovery-go-worker.yml)
instead of the health-only scrape job for the same target. Its additional worker
opt-in enables three independent alerts. Shared dashboard worker panels show
actual start/return times, active/idle age, duration and latest-pass scalar counts.
Counts retain the previous joined pass while a new one runs; missing outcomes
stay Unknown. No owner or protected inputs become labels.

Cancel/join recovery and health reads, shut down/join HTTP, then close database
and client resources. A hung pass must return before overlap or shutdown can
complete; metrics do not abandon queries, force callbacks or replace fences.
See the [worker contract](../../../../docs/event-acknowledgment-go-worker-liveness.md)
and [runbook](../../../../docs/runbooks/GregaleEventRecovery.md#worker-liveness).
Build/static checks do not qualify live behavior; hosted qualification is pending.
