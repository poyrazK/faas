# Stored acknowledgment retry policies

All three SDKs can save a versioned retry policy atomically with an intent. Recovery
captures that binding under the row lock and uses it through settlement,
regardless of worker fallback settings. Unbound intents keep fixed or adaptive
worker fallback behavior.

This page describes the shared v1 timing policy. Go also supports an optional
[v2 claim budget, parking and resume](event-acknowledgment-retry-budgets.md).
Current Go, Node and Python helpers require its additional schema upgrade, even
for v1/unbound work. Keep enabled scopes exclusive to budget-aware hosts of any
supported SDK; older binding-aware workers reject v2 and pre-binding workers may
ignore its limit.

## Install or upgrade first

Fresh installations explicitly apply `faas.EventAcknowledgmentIntentSchema`.
For an existing table, apply
`faas.EventAcknowledgmentIntentStoredRetryPolicySchema` once inside an application
migration transaction **before upgrading intent workers**. The upgrade adds
three nullable columns and one CHECK constraint:

- `retry_policy_version` (integer).
- `retry_initial_backoff_ms` (bigint).
- `retry_max_backoff_ms` (bigint).

All null means unbound. A binding requires all values, version 1, initial
milliseconds 1..3600000 and maximum between initial and 3600000. Existing rows
remain unbound. The upgrade does not reset claims, attempts, due times, actions,
capabilities, receipts or results. Track this one-time ALTER in application
migration history. CREATE TABLE IF NOT EXISTS does not upgrade existing tables.

Current SDK staging, listing and recovery read these columns even for unbound work;
missing columns produce sanitized storage errors. Helpers never execute DDL.
There is no Gregale platform migration or registry change. Fresh DDL is packaged
identically in all three SDKs.

Node exports `eventAcknowledgmentIntentStoredRetryPolicySchema`; Python exports
`event_acknowledgment_intent_stored_retry_policy_schema`. Both contain the same
upgrade SQL as Go. Apply one shared migration, not one per SDK.

All three updated SDKs honor bindings. Older workers ignore them and can choose
a different delay. Upgrade every possible claimant before enabling bound
staging. The CHECK validates metadata, not worker compatibility. Install the
schema, upgrade recovery, then enable staging.

## Save the completion and policy together

```go
policy := faas.EventAcknowledgmentIntentRetryPolicy{
    InitialBackoff: time.Second,
    MaxBackoff: 5 * time.Minute,
}
queued, err := worker.RunDurableWithRetryPolicy(ctx, db, policy,
    func(workCtx context.Context, tx faas.EventConsumerSQLTransaction) (json.RawMessage, error) {
        _, err := tx.ExecContext(workCtx,
            "UPDATE invoices SET paid = true WHERE id = $1", invoiceID)
        if err != nil { return nil, err }
        return json.RawMessage(`{"applied":true}`), nil
    })
```

Business writes, receipt, completion and normalized policy commit together.
Business callbacks contain only transactional SQL effects. Policy validation and
copying precede worker/database/HTTP operations, using the existing
[Go defaults and bounds](event-acknowledgment-go-retry-policy.md). Version 1
specifies exponential full jitter, with caps rather than minimum waits.

Other binding-aware methods are:

| Method | Purpose |
| --- | --- |
| `SaveCompletionIntentWithRetryPolicy(ctx, db, policy)` | Stage a committed receipt without business work. |
| `RunDurableWithProcessingLeaseAndRetryPolicy(ctx, db, lease, policy, handler)` | Apply the existing local processing fence in the final transaction. |
| `SaveRetryIntentWithRetryPolicy(ctx, db, reason, policy)` | Save an explicit retry decision and its callback schedule. |
| `SaveDeadLetterIntentWithRetryPolicy(ctx, db, reason, policy)` | Save an explicit dead-letter decision and its callback schedule. |

Negative staging still requires known rollback, stopped work and exclusive
durable-job ownership. Receipt checks block a negative intent after committed
completion. Callback retry settings never choose or change a business disposition.

### Node and Python staging

Node accepts optional `retryPolicy` on `runDurable`, `saveCompletionIntent`,
`runDurableWithProcessingLease`, `saveRetryIntent`, `saveDeadLetterIntent` and
`runPreparedDurable`. The first five use exported
`EventAcknowledgmentIntentStagingOptions`; prepared options include the same
field. Policy values are copied/frozen before I/O and preparation.

```js
const queued = await worker.runDurable(pool, applyBusinessSQL, {
  signal,
  retryPolicy: { initialBackoffMs: 1000, maxBackoffMs: 300000 },
});
```

Python accepts the `retry_policy` keyword on `run_durable`,
`save_completion_intent`, `run_durable_with_processing_lease`,
`save_retry_intent`, `save_dead_letter_intent` and `run_prepared_durable`.
For prepared work the keyword is separate from preparation options. Exact frozen
policy values are copied before I/O, including receipt-only reconciliation.

```python
queued = await worker.run_durable(
    connection, apply_business_sql,
    retry_policy=EventAcknowledgmentIntentRetryPolicy(1000, 300000),
    stop_event=shutdown,
)
```

Omitting policy leaves new rows unbound and preserves existing bindings. Keep the
original policy choice with protected durable job configuration so explicit
retries repeat the same decision. Prepared completion binds it in the same
fenced final transaction as writes/receipt/intent. Preparation and lease renewal
mechanics are unchanged; immediate callback modes do not bind intents.

## Repeated staging and immutable bindings

New insertion writes policy columns in the existing transaction. Staging uses
invocation serialization and locks the latest row before comparing metadata.
Repeating the same normalized binding returns retained status without rewriting
policy, payload, claim, count, due time or confirmed state. Equivalent defaults
compare equal. Different policy values return
`ErrEventAcknowledgmentIntentConflict`.

Explicit policy staging also conflicts with an existing matching **unbound**
intent. Conflicts roll back the transaction, including new business writes.
Reconcile uncertain commits with the same original inputs and policy. To bind
existing backlog, all three SDKs provide a separate
[guarded adoption command](event-acknowledgment-policy-adoption.md) with expected
timestamp/count, no live claim and a durable command receipt. It cannot revise
an existing binding.

Legacy staging creates new rows unbound and preserves an existing matching
binding. It cannot remove it. Newer generation/attempt pairs retain existing
supersession and secret clearing and can save their own initial policy. History
retains policy metadata. Direct database changes are outside the SDK's binding
contract.

## Recovery and diagnostics

`ClaimEventAcknowledgmentIntent` reads and validates policy under its existing
`FOR UPDATE SKIP LOCKED` lock before incrementing attempts or committing a claim.
Partial, unknown-version or out-of-range bindings fail as sanitized storage errors
before a callback send; they never fall back. An uncertain claim COMMIT still
returns no usable capability.

The captured policy takes precedence in `claim.Deliver`, `DeliverWithRetryPolicy`,
both single-iteration APIs and bounded passes. Valid fallback options remain
required. Claim duration, callback budgets, context and lease timeouts stay worker
settings. Unbound rows use the validated fallback exactly as before.

Existing receipt checks, saved hashes/actions, platform fences and live local
claim settlement retain their contract. The same durable count drives jitter;
unconfirmed settlement persists database now plus the delay. Within-claim retries
and Retry-After are unchanged. V1 has no lifetime exhaustion. The shared opt-in v2
budget adds explicit parking/resume; automatic disposition and background workers
remain absent. Failed settlement may not persist a new due
time; remote success can still precede response/settlement loss.

`EventAcknowledgmentIntentStatus.RetryPolicy` optionally contains payload-free
`EventAcknowledgmentIntentStoredRetryPolicy`: `Version`, `InitialBackoffMS` and
`MaxBackoffMS`. JSON uses `retry_policy` with `version`, `initial_backoff_ms` and
`max_backoff_ms`; nil omits it. Staging results and
`ListEventAcknowledgmentIntents` expose this without callback tokens/results or
original event bodies. Returned metadata does not grant ownership, prove remote
confirmation or mutate the active claim.

Node status adds optional frozen `retryPolicy` with `version`,
`initialBackoffMs`, `maxBackoffMs`. Python status adds `retry_policy`, an optional
frozen `EventAcknowledgmentIntentStoredRetryPolicy` with `version`,
`initial_backoff_ms`, `max_backoff_ms`. Null/omitted metadata identifies unbound
work. Python retains full int64 generations; Node continues to reject unsupported
generations before claim COMMIT. Missing columns, partial/unknown bindings or
invalid bounds fail closed before a callback send in all SDKs.

The [buildable staging example](../sdk/go/examples/event-acknowledgment-recovery/staging.go)
uses this API. Local validation covers SDK/example builds and vet, schema
packaging/generation, source/DDL comparisons and docs. No local tests or helper
smoke checks are run. Hosted upgrade, concurrency, replay, conflicting saves,
policy precedence and uncertain-commit qualification remains pending.
