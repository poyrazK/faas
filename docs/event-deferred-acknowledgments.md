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
    preview.EventAcknowledgmentRequest{Token: deliveryToken})
// Handle err; never log deliveryToken. status contains no secret.
_ = status

// After the business transaction and durable consumer receipt commit:
status, err = client.CompleteEventAcknowledgment(ctx, app, invocationID,
    preview.EventAcknowledgmentRequest{
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

Python applications can use the [async acknowledgment worker](event-acknowledgment-python-worker.md)
for confirmed lease renewal, transactional receipts and immediate callback recovery.
Its [protected-job example](../examples/event-acknowledgment-python/README.md)
retains ingestion jobs until terminal callback confirmation.

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
The legacy health field `renewal_failure_coverage=not_recorded` remains unchanged
for compatibility. The independent minute-bucket summary described below supplies
windowed counts; coalesced groups spanning a window boundary cannot supply those
denominators.

## Paginated outcome investigation

Use `GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/acknowledgment-history/pages`
for investigations exceeding the original latest-update view. That original
endpoint and its ordering remain unchanged. The new Go method is
`ListEventAcknowledgmentHistoryPage(ctx, app, subscription, query)`; generated
Node and Python clients expose the same operation.

Optional filters are `invocation_id`, `kind`, `from` (inclusive), and `until`
(exclusive). Time bounds select a group's **first occurrence**, normalize to UTC
microseconds, and require `from < until` when both are supplied. They do not
select individual renewals within a coalesced group. `limit` defaults to 100 and
accepts 1–100. A page returns `has_more`, `next_cursor` when more groups exist,
and the initial `cutoff`. Request the next page with that cursor and the same
filters and effective page size. Cursors are bound to account, application,
consumer and normalized options, expire one hour after the cutoff, and never
grant access without the normal application read authorization. Invalid,
expired and mismatched cursors return HTTP 400.

Pages order `(first_at, invocation_id, attempt, replay_generation, kind)`
descending, using an exclusive continuation boundary and a fixed first-page
cutoff. These fields are immutable, so ongoing renewals cannot move a group
between pages. Groups first recorded after the cutoff are excluded. Counts and
`last_at` remain live and may include operations after the cutoff; this is not
an exported database snapshot. Retention and invocation deletion can remove
groups between pages, and an insert uncommitted at the initial read may become
visible later if its first timestamp precedes the cutoff. Refresh without a
cursor to begin a new traversal. No events or tokens are copied into the cursor.

## Windowed renewal and expiry metrics

The existing subscription delivery-health response now includes
`processing.renewal_window` when supported by the server. The CLI's
`events subscription-delivery-health` command displays the same summary, and
Go, Node and Python SDKs expose its typed fields. Existing alerts and watch
thresholds are unchanged.

The summary selects only whole UTC minutes wholly inside the requested health
window and excludes the open minute. Its own `window_start` and `window_end`
are the effective bounds: a five-minute request at 12:05:30 summarizes
12:01:00–12:05:00, not partial boundary minutes. Sub-minute requests can have an
empty effective window; both rate fields are then absent. This alignment is
independent of the retained completion-latency window and current-state gauges.

Each consumer has at most three counters per minute: successful explicit
renewals, current-token renewal rejections, and deferred-awaiting expiry
transitions. Initial handoff, idempotent completion repeats, ordinary HTTP
completions, malformed requests, wrong tokens and replaced-attempt tokens are
not renewal samples. Authorization and infrastructure errors are not measured
by this rejection fraction. Valid current-token conflicts after completion or lease
expiry are samples. Expiries are counted when the scheduler transitions the
expired attempt, not retrospectively at its lease deadline. Delayed commits
can make previously closed buckets visible in a later read.

`renewal_attempts = renewal_successes + renewal_rejections`.
`renewal_rejection_fraction` divides rejections by these **recorded operations**;
it is absent without samples. Repeated valid-token rejections are individual
operations, not deduplicated deliveries. `lease_expiry_rate_per_second` divides
expiry transitions by effective window seconds. It is an event frequency, not
a delivery-failure probability. Counts saturate at 2,147,483,647 per bucket;
`saturated=true` makes totals lower bounds and omits both rate fields.

`coverage=recorded_full_minute_buckets` covers instrumented operations only.
No pre-upgrade history is backfilled; a mixed-version rollout omits operations
handled by older writers. Zero samples do not prove that no historical activity
occurred. The parent `processing.complete` describes retained invocation gauges,
not metric rollout completeness. The deprecated non-windowed
`renewal_failure_coverage=not_recorded` field retains its old value for SDK
compatibility; use the nested summary and its independent coverage instead.

Counters contain account/app/consumer scope, minute, outcome kind, count,
saturation and retention metadata only. They contain no invocation IDs, tokens,
digests, payloads or customer error strings. Writes commit atomically with the
existing acknowledgment transaction, including valid-token rejection diagnostics.
Recording errors fail the operation instead of losing its counter. Shared
expiry counters are acquired in account/app/consumer order within sweep batches.

Retention ends 48 hours after bucket start, independently of invocation pruning.
Reads exclude expired buckets immediately. Scheduler ticks prune bounded batches;
physical cleanup can lag during outages. App/account deletion cascades metric
removal. A deleted subscription does not prevent an already accepted delivery
from completing: metric rows deliberately have no subscription foreign key.

## Local alerts over recorded minute windows

Delivery-health watches can now select two additional conditions:
`processing_renewal_rejection_fraction` and
`processing_lease_expiry_rate_per_second`. These are local watch notifications,
not a hosted notification service. The CLI accepts:

- `--renewal-rejection-threshold FRACTION`: greater than zero through one;
  `0.05` means at least 5% of recorded renewal operations were rejected.
- `--renewal-min-samples N`: recorded renewal attempts required in every eligible
  window; defaults to 10 when the renewal alert is enabled.
- `--lease-expiry-rate-threshold RATE`: positive expiry transitions per second;
  this is a frequency, not a failed-delivery percentage.
- `--processing-window-sustain DURATION`: breach and recovery confirmation,
  at least one minute, default one minute.
- `--processing-metrics-recording-since RFC3339`: optional manual assertion of
  continuous recording; by default the watch requires verified writer coverage.

Zero thresholds disable their conditions. These flags require `--watch`.
The corresponding Go SDK options are `ProcessingRenewalRejectionFraction`,
`ProcessingRenewalMinimumSamples`, `ProcessingLeaseExpiryRate`,
`ProcessingWindowSustain`, and `ProcessingMetricsRecordingSince` (a `*time.Time`).
The existing callback, alert JSON, durable outbox, checkpoint and acknowledgement
mechanisms apply to these conditions.

Automatic mode requires `recording_coverage.verified` for the entire effective
window. Missing inventory, missing or incompatible writers, stale heartbeats,
restarts and conflicting sessions make coverage unknown. The window end follows
the last completed minute acknowledged by every writer, so it can lag the current
minute. Rates use this effective interval.

An explicit recording-since timestamp remains an **operator assertion**. Confirm
that every relevant writer records continuously before choosing it. Do not infer
it from a first bucket or absence of events. After rollback, restore, data loss or
another recording gap, begin a new watch with a newly confirmed boundary and a
fresh checkpoint. Manual mode bypasses automatic inventory verification.

The watch checks scope, exact effective window bounds, minute width, retention,
coverage label, saturation, bounded counts, denominator consistency and reported
rates. Its `MaxAge` applies to the database observation clock. Insufficient renewal
samples, unsupported/missing summaries, stale/repeated/out-of-order observations,
invalid evidence and missing window coverage produce `unknown`, not healthy or
recovered. Expiry zero is eligible only with verified coverage or a manual assertion;
renewal zero samples cannot resolve a renewal incident.

Both opening and recovery need at least two distinct consecutive completed-minute
windows and the configured local elapsed duration. Fresh polls of one unchanged
window maintain liveness but do not add confirmation samples. A breach/clear
change caused by a delayed commit resets the opposite confirmation even within
the same window. Skipped completed windows or excessive polling gaps lose
confirmation. Windows overlap, so these are sustained observations, not independent
statistical samples. The default timeout may need increasing to cover warm-up
and multiple windows.

A confirmed incident remains firing while recovery is pending. Recovery requires
sustained eligible values strictly below the threshold. Lost evidence emits
`confirmation_lost`, not `resolved`; renewed proof can open a new incident.
Checkpoints preserve `metric_recording_since`, thresholds, minimum request samples,
sustain and recovery policy, but do not preserve live confirmation. Resume requires
matching options and fresh windows, and rejects a changed recording boundary or a switch between automatic and manual modes.
These conditions use checkpoint version 2; older clients reject the unknown
metrics rather than silently reinterpret them. Existing conditions are unchanged.

## Declaring acknowledgment writer coverage

Configure the same exhaustive roster on every apid and schedd, with a distinct,
stable slot for each deployed process. For example, an apid TOML file can contain:

```toml
event_acknowledgment_writer_slot = "api-1"
event_acknowledgment_writer_roster = '[{"role":"apid","slot":"api-1"},{"role":"schedd","slot":"scheduler-1"}]'
```

The schedd uses the identical roster and slot `scheduler-1`. Environment overrides
are `FAAS_EVENT_ACK_WRITER_SLOT` and `FAAS_EVENT_ACK_WRITER_ROSTER`. Rosters require
both roles, at most 128 unique role/slot pairs and 16 KiB of JSON. Slots contain
1–128 ASCII letters, digits, underscores, periods, colons or hyphens.

The deployment must ensure this inventory includes every acknowledgment writer
and exclusively assigns its slots. This protocol cannot discover or fence an
unlisted legacy process that never registers. An expected older writer without
heartbeats appears missing. With no configured roster, processes still register
observations but coverage never verifies.

Each process starts a fresh session and heartbeats every 10 seconds with a
30-second lease. Every live session must support the recording capability and
agree on the canonical roster. Duplicate sessions, roster changes and lease gaps
invalidate continuity; a restart establishes a new conservative boundary. Health
reports counts, an opaque inventory revision and coverage timestamps, without
exposing slots or session identifiers. Coverage reads and metric counts share a
repeatable-read snapshot. Public evidence stops at the least recent heartbeat. Heartbeats do not fence
in-flight metric transactions; delayed commits can still update a recorded minute.

The live scan is bounded to 256 sessions and fails closed on overflow. Historical
session observations are retained for 48 hours, with bounded cleanup on heartbeats.
Heartbeat failures are logged and retried without stopping delivery. These records
attest configured writer capability and liveness, not delivery success or database
data integrity. After a data-loss restore, restart every writer to establish a new
coverage boundary before trusting automatic windows. A silent data rollback cannot
be detected from writer liveness alone.

## Explicit retry and dead-letter acknowledgments

A durable consumer can stop its current attempt without waiting for lease expiry:

- `POST .../acknowledgment/retry` schedules the captured execution backoff. An
  exhausted captured attempt budget dead-letters instead.
- `POST .../acknowledgment/dead-letter` immediately dead-letters this delivery.

Both accept `{"token":"<delivery token>","reason":"dependency_unavailable"}`.
The reason must be a non-sensitive code of 1–128 lowercase ASCII letters, digits,
underscores, periods or hyphens. Never include credentials, tokens or event payloads.
Callbacks require app-scoped write authority and the current token. They work
within the live offered or handed-off lease, including a callback racing the
scheduler's observation of `202`. Stop processing before requesting a disposition;
the callback does not kill workers or undo effects.

The invocation transition, quota release, saved disposition and diagnostic outcome
commit atomically. Subscription capacity and ordering follow existing invocation
state: retry remains pending with its captured due time; dead-letter is terminal.
Other consumers are unaffected. Claims consume attempts; repeated callbacks do not.
Existing keyed ordering and at-least-once semantics remain in force.

Responses include `disposition`, `failure_reason` and, for a pending retry,
`next_attempt_at`. Disposition records the requested action; inspect `state` to
identify budget-exhausted dead-lettering. Repeat the same action, token and reason
after an uncertain response. Matching repeats return the original saved response
without releasing quota, moving the due time or adding history again, even after
the old lease expires. New claims and replay generations fence old tokens. Changed
action/reason, cancellation, completed attempts and expired live claims return 409.
GET reports current state with retained disposition details for the matching
attempt. Receipts share invocation retention and are replaced on the next offer.

Go methods are `RetryEventAcknowledgment` and `DeadLetterEventAcknowledgment`,
using `EventAcknowledgmentFailureRequest`. Generated Node and Python clients expose
the corresponding operations. The CLI reads tokens from a protected JSON file:

```sh
gregale events delivery-retry my-app INVOCATION_UUID --request-file failure.json
gregale events delivery-dead-letter my-app INVOCATION_UUID --request-file failure.json
```

Do not put tokens in command arguments. CLI output does not print the token.
History adds `retry_requested` and `dead_letter_requested` outcomes, separately
from lease-expiry and renewal-rate counters. Update clients that validate history
enums before using these outcomes. Upgrade apid and every scheduler before using
negative acknowledgments: an older scheduler cannot reset the saved rejection
receipt on a new offer and fails closed. Drain these attempts with compatible
schedulers before rollback. No production migration or deployment is performed.

For the Go receipt-aware worker, renewal lifecycle, explicit failure methods and
crash recovery, see [Go worker acknowledgments](event-acknowledgment-worker.md).

[Durable Go acknowledgment intents](event-acknowledgment-intents.md) persists
completion with the business receipt and replays saved callbacks after crashes.
