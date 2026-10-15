# Deferred event acknowledgments

Configure `lease_seconds` using
`PUT /v1/apps/{slug}/event-subscriptions/{subscriptionID}/acknowledgment-policy`.
Zero keeps ordinary HTTP-success delivery; 1–300 enables deferred acknowledgment.
The policy is captured at acceptance. Changes do not modify accepted recipients,
retries or replay children. Explicit subscription backfills capture current policy.
Legacy snapshots remain ordinary delivery. Enable only after updating the consumer.

An opted-in internal delivery includes `X-Gregale-Event-Acknowledgment-Token`
and `X-Gregale-Event-Acknowledgment-Lease-Seconds`, alongside existing invocation,
attempt, replay-generation, acceptance and consumer headers. Durably schedule the
work before returning **202 Accepted**. Other successful responses complete normally;
202 without an enabled policy also keeps its existing behavior.

The platform persists only a SHA-256 token digest bound to invocation, attempt
and replay generation. The raw token exists only in the transient dispatch headers.
Never log it, put it in a URL or include it in diagnostic artifacts. A durable
worker may retain it as a secret in a protected work record until completion.

## API and clients

All endpoints require authenticated app-scoped authority: `apps:read` for reads,
`deploy:write` for writes, or `admin`. The token is an additional fence, not
standalone authorization. Configure an appropriately scoped API credential in the
worker. Reads never disclose a token.

- `GET .../acknowledgment-policy` inspects future acceptance policy.
- `GET /v1/apps/{slug}/event-deliveries/{invocationID}/acknowledgment` inspects
  processing state and current lease without renewing it.
- `POST .../acknowledgment/renew` accepts `{"token":"<delivery token>"}`.
- `POST .../acknowledgment/complete` accepts
  `{"token":"<delivery token>","result":{"processed":true}}`.

Renew well before expiry. Database time controls the lease, and renewal cannot
exceed one hour from token issue for that attempt. A brief handoff window exists
after the app sends 202 but before the scheduler records it: renewal returns 409
while the token is still offered. Retry with the same token within the live lease;
stop work on confirmed expiry or replacement. Completion may arrive before handoff
and wins safely. Unknown callback outcomes require repeating the same token and
result, not issuing new work.

Complete only after committing business writes and the consumer's durable receipt.
The invocation result, terminal state and quota release commit together. Matching
completion repeats return the retained completed status, even after expiry.
Replaced attempts, tokens, replay generations or changed results return 409.
An omitted result means JSON null. Result comparison ignores object key order and
whitespace, but preserves numeric spelling (for example, 1 and 1.0 differ). Completion
results are at most one MiB. Renewal forbids a result field.

Go exposes `GetEventAcknowledgmentPolicy`, `SetEventAcknowledgmentPolicy`,
`GetEventAcknowledgment`, `RenewEventAcknowledgment`, and
`CompleteEventAcknowledgment`; generated Node and Python clients include the
same operations. For example, a Go worker with a configured SDK client calls:

```go
status, err := client.RenewEventAcknowledgment(ctx, app, invocationID,
    faas.EventAcknowledgmentRequest{Token: deliveryToken})
// Handle err; never log deliveryToken. status contains no secret.
_ = status

// After the business transaction and durable consumer receipt commit:
status, err = client.CompleteEventAcknowledgment(ctx, app, invocationID,
    faas.EventAcknowledgmentRequest{
        Token: deliveryToken,
        Result: json.RawMessage(`{"processed":true}`),
    })
// If the response is uncertain, repeat the same token and result.
```

## Recovery, diagnostics and retention

While processing, the invocation stays dispatching and retains its account quota,
subscription capacity and existing keyed work-policy ordering. The scheduler stops
renewing after handoff. A bounded sweep independently requeues each expired lease
with captured execution backoff, or dead-letters it at the captured attempt limit.
Crashes before handoff use existing abandoned-dispatch recovery; the next claim
checks its attempt budget before another request. A new claim issues a new token.
Old tokens cannot renew or complete a later claim, including an in-place replay.

Status includes waiting deliveries before their first dispatch and distinguishes `waiting`, `delivering`, `processing`, `expired`, `completed`,
`failed`, `dead_letter`, `cancelled` and `superseded`. Expired is the lease observation before
recovery; afterward the invocation becomes waiting or dead lettered. Existing
receipt, attempt-history and execution-health views retain underlying invocation
outcomes. Status never proves external business effects.

There is one operational acknowledgment record per invocation, replaced on a new
claim and deleted by a cascading foreign key when its invocation is pruned.
Missing retained evidence does not prove completion never happened. Missing or
deleted applications, mismatched scope and absent invocations fail closed.

Processing remains at least once. Expiry neither terminates workers nor undoes
external effects. Commit consumer receipts with database writes; use an outbox or
external idempotency keys for other effects. Acceptance plus consumer remains the
business deduplication identity; tokens and attempt numbers do not replace it.
A lost completion response after a business commit can still cause another
delivery, which must replay the saved consumer receipt.

## Durable workers and rollout

A 202 does not keep the application VM awake: Gregale may park it after the HTTP
request ends. An in-process goroutine is not a durable worker. Durably enqueue
work before returning 202 and use a recoverable worker that can reach apid.

Deploy the migration, apid and every scheduler before enabling a policy. An older
scheduler treats 202 as ordinary success. Before rollback, reset future policies
and drain already accepted deferred work with compatible schedulers. The migration
Down deliberately retains acknowledgment records and snapshot behavior. This
change performs no production rollout or migrations.

## Consumer processing health and alerts

`GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-health`
now includes an optional `processing` summary. The existing Go, Node and Python
clients expose it; ordinary subscriptions return zero matching deferred deliveries.
The aggregate is read-only and contains no payloads, results, token material,
event IDs or invocation IDs. It never renews a lease or changes processing state.

Counts cover all matching **retained** opted-in invocations: waiting, delivering,
processing, expired dispatch leases, completed, dead lettered, failed and other
terminal states. They are current gauges, not counts within the requested window.
`complete` means the retained scope was aggregated without sampling, not that
pruned historical evidence exists. Routing, execution and processing are
independent reads; the top-level response is not one database snapshot.

`near_expiry` counts live handed-off leases in their final third, capped at a
thirty-second horizon. A fresh short lease does not immediately count as risky.
The oldest processing age starts at token issue, including the initial delivery,
and excludes expired or replaced claims. Work-policy terminal expiry belongs to
`other`, not the current expired-lease gauge. Completion latency is measured from
token issue to explicit completion, only for matching retained completed claims
whose completion falls within the selected `window`. Ordinary 2xx completions
contribute to the completed gauge but not to explicit acknowledgment latency.
There is no latency measurement when `acknowledgment_samples` is zero. Retention
can remove samples; this maximum is not a historical SLO or business execution
latency. Rejected renewal calls are not recorded: `renewal_failure_coverage` is
`not_recorded`, and no zero failure count is inferred.

```sh
gregale events subscription-delivery-health my-app SUBSCRIPTION_UUID --window 15m
gregale events subscription-delivery-health my-app SUBSCRIPTION_UUID \
  --watch --processing-oldest-threshold 5m \
  --processing-lease-risk-threshold 30s --processing-expired-threshold 30s \
  --alerts-json
```

Processing-age alerts require at least two fresh samples and thirty seconds of
continuous age-threshold breach. Lease-risk and expired-lease alerts measure
continuous local observation of a nonzero gauge for the selected duration, also
requiring two fresh observations. A brief expiry recovered between polls may
never be observed; these alerts are not a durable expiry-event ledger.
Existing recovery events fire when conditions clear. Unknown, missing, stale,
repeated or out-of-order samples lose confirmation rather than falsely proving
recovery. Checkpoints preserve incident history and require fresh confirmation on
resume. Older clients reject checkpoints containing unfamiliar processing metrics.
Alerts are local to a running watch; no background notification service is started.

## Retained acknowledgment outcomes

`GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/acknowledgment-history`
requires application read authority. Add `?invocation_id=<uuid>` to inspect one
delivery. The Go SDK exposes `ListEventAcknowledgmentHistory`; Node and Python
expose the generated operation.

The response contains the 100 most recently updated outcome groups, ordered by
`last_at` descending with invocation/attempt/generation/kind tie breakers.
`truncated` reports additional retained groups; narrow to a delivery to inspect
its older groups. This initial API has no pagination or completeness guarantee.

Each attempt has at most five groups: `accepted`, `renewed`, `renewal_rejected`,
`completed`, and `expired`. Groups have first/last database timestamps and an
occurrence count saturated at 2,147,483,647. Renewals update a fixed row.
Idempotent handoff/completion repeats add no occurrences. Completion covers
explicit callbacks, not ordinary HTTP-success completion. Expiry covers the
scheduler's deferred-awaiting expiry transition, not generic dispatch reclaims.

Only a well-formed token matching the current attempt and replay generation can
produce renewal rejection history. Wrong, malformed and replaced tokens never
write. A valid current token can be rejected before handoff, after lease or
deadline expiry, or after completion. The ledger contains no token, digest,
request body, completion result, customer error text or event payload.
Successful state changes and outcomes commit atomically; valid-token rejection
diagnostics commit before HTTP 409. Ledger errors fail the operation.

Retention ends seven days after the attempt's fixed acknowledgment deadline.
Repeated renewals and rejections cannot extend it. Reads immediately exclude
expired groups; scheduler ticks delete bounded batches, so cleanup may lag.
Invocation pruning also deletes its groups; deleting a consumer removes access.
The ledger starts empty at rollout and reconstructs no prior operations.
`coverage=retained_coalesced_deferred_outcomes` describes these limits.
The health field `renewal_failure_coverage=not_recorded` remains unchanged because
that aggregate does not yet read the ledger. Groups spanning a window boundary
cannot serve as precise windowed failure-rate denominators.
