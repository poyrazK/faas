# Adaptive Go acknowledgment recovery retries

Go saved-callback recovery can spread retries during an outage using an optional
`EventAcknowledgmentIntentRetryPolicy`. Each committed unconfirmed settlement
persists a capped exponential delay with full jitter. The existing fixed-delay
APIs and the pass's default one-second `Backoff` retain their behavior.

The separate Go [stored retry budget](event-acknowledgment-retry-budgets.md)
can cap total recovery claims, park exhausted work and grant explicit resumption.
It requires an additional schema upgrade before current Go helpers and
budget-aware hosts for enabled scopes. Worker fallback policies keep this page's
unlimited timing-only contract.

Current Go intent staging, listing and recovery requires the new stored-policy
columns. Fresh schemas include them; existing installations must apply the
[explicit upgrade](event-acknowledgment-stored-retry-policy.md#install-or-upgrade-first)
before upgrading Go workers, even when all intents are unbound.

```go
progress, err := preview.RecoverEventAcknowledgmentIntents(ctx, db, client,
    accountID, appID, preview.EventAcknowledgmentIntentRecoveryPassOptions{
        MaxAttempts: 100,
        Concurrency: 4,
        RetryPolicy: &preview.EventAcknowledgmentIntentRetryPolicy{
            InitialBackoff: time.Second,
            MaxBackoff: 5 * time.Minute,
        },
    })
```

Leave `Backoff` zero when supplying a policy. Combining them returns
`ErrInvalidEventConsumerRequest` with zero progress before claiming any rows.
A nil policy keeps the existing fixed delay. A non-nil empty policy enables
jitter with a one-second initial cap and a five-minute maximum cap.

## Policy bounds and entry points

| Field | Zero default | Bounds |
| --- | --- | --- |
| `InitialBackoff` | One second | Whole milliseconds from one millisecond through one hour. |
| `MaxBackoff` | Five minutes | Whole milliseconds from the effective initial value through one hour. |

Defaults apply independently. For example, an initial value greater than five
minutes requires an explicit maximum at least as large. Negative values,
sub-millisecond precision and reversed bounds are invalid. Policies are copied
and normalized before database/HTTP work. Keep caller-owned options immutable
while starting a call.

For a single recovery iteration, use:

```go
status, err := preview.RecoverNextEventAcknowledgmentIntentWithRetryPolicy(
    ctx, db, client, accountID, appID, 30*time.Second,
    preview.EventAcknowledgmentWorkerOptions{},
    preview.EventAcknowledgmentIntentRetryPolicy{
        InitialBackoff: time.Second,
        MaxBackoff: 5 * time.Minute,
    })
```

Hosts separating acquisition from delivery can call
`ClaimEventAcknowledgmentIntent`, then
`claim.DeliverWithRetryPolicy(ctx, db, client, callbackOptions, policy)`.
The existing `claim.Deliver` and `RecoverNextEventAcknowledgmentIntent` continue
to accept their explicit fixed delay. Claim duration, callback options, owner
scope, error sanitization and status-plus-error semantics follow the
[saved-intent protocol](event-acknowledgment-intents.md#recover-one-callback).

## Durable count, jitter and due time

For committed recovery claim number `n`, the delay cap is:

`min(MaxBackoff, InitialBackoff × 2^(n−1))`.

The claim update atomically increments and returns the existing durable
`delivery_attempts` count under the row lock. It saturates at 2,147,483,647.
The local calculation doubles only until reaching the configured cap, bounding
work and avoiding duration overflow even for saturated counts. With an initial
cap of one second and a maximum of five minutes, successive caps are one, two,
four, eight seconds, continuing up to five minutes.

Each claim selects a positive whole-millisecond delay from one millisecond
through its cap. `InitialBackoff` names the first **cap**, not a minimum wait;
full jitter can select a shorter delay at any attempt. Jitter uses a
domain-separated SHA-256 hash of the claim's already-random recovery token.
Scheduling therefore needs no additional random-source read after an uncertain
HTTP outcome and exposes no raw token bits through the delay. Fresh claims
select fresh jitter even after the count saturates.

Only the conditional `unconfirmed` settlement sets `next_attempt_at` to the
database clock plus that delay. The due time persists across worker changes and
process restarts. Recovery queries admit it only once it is due and unclaimed.
The host still owns when another iteration/pass runs; an SDK sleep or background
poller is not installed. `confirmed` and `superseded` settlements retain their
existing handling and leave the due timestamp unchanged.

The count measures committed recovery claims, including claims whose worker
crashed before HTTP or whose COMMIT response was lost. It also includes claims
created by fixed-delay or other-language workers. Callback HTTP retries within a
claim do not separately increment it. Attempt count therefore cannot prove how
many physical requests reached Gregale.

## Fencing, uncertainty and worker configuration

The policy changes the delay for an unconfirmed settlement. Saved callback
identity, action, token, reason/result and hashes retain their existing contract.
Negative callbacks still check the consumer receipt before sending. Delivery
preflight and settlement still require the live local claim, and the platform
still fences its invocation/generation/attempt. Business work is never repeated
by recovery. A committed unconfirmed status can accompany a callback error;
inspect both status/progress and error.

If a claim expires or settlement fails, there may be no committed new due time.
After the claim expires, another worker can recover the same saved callback;
the local policy does not guarantee a delay after every failed HTTP call.
Uncertain claim COMMIT still returns no usable capability. Preserve retained
evidence and reconcile it using the existing recovery protocol. Remote success
can precede a lost response or local settlement failure.

For unbound intents, policy remains worker configuration: future settlements
use the next worker's configured fallback. Coordinate those settings across
[Node](event-acknowledgment-node-retry-policy.md),
[Python](event-acknowledgment-python-retry-policy.md) and Go.

Go can also [bind a versioned policy during staging](event-acknowledgment-stored-retry-policy.md).
Its captured binding takes precedence over worker fixed/adaptive fallback and
appears in payload-free status. Updated Node and Python workers honor the same
bindings. Upgrade every recovery host before enabling bound staging. Valid
worker fallback options remain required.

Callback-level attempts, timeout, `RetryBackoff` and bounded Retry-After handling
remain within `EventAcknowledgmentWorkerOptions` and the existing callback
implementation. Remote Retry-After guidance is not carried into this durable
policy. Custom HTTP transports remain responsible for hidden retries and context
contracts. This policy imposes no lifetime attempt limit, exhausted state or
automatic dead-letter decision. The pass's `MaxAttempts` remains a per-call
admission budget; see the [pass contract](event-acknowledgment-go-recovery-pass.md).

The [buildable host example](../sdk/go/examples/event-acknowledgment-recovery/README.md)
uses the policy. Local validation covers SDK/example builds, vet, source/schema
comparisons and documentation checks. No local tests or helper smoke executions
are run. Hosted timing, restart, concurrency, cancellation and uncertain-outcome
qualification remains pending for this increment.
