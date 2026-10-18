# Adaptive Python acknowledgment recovery retries

Python saved-callback recovery accepts an optional frozen
`EventAcknowledgmentIntentRetryPolicy`. It uses the same durable claim count,
capped exponential full jitter and database-clock due-time contract as
[Go](event-acknowledgment-go-retry-policy.md) and
[Node](event-acknowledgment-node-retry-policy.md).

```python
from faas_sdk import (
    EventAcknowledgmentIntentRecoveryPassOptions,
    EventAcknowledgmentIntentRetryPolicy,
    arecover_event_acknowledgment_intents,
)

progress = await arecover_event_acknowledgment_intents(
    client.inner, connection_scope, account_id, app_id,
    EventAcknowledgmentIntentRecoveryPassOptions(
        max_attempts=100,
        concurrency=4,
        retry_policy=EventAcknowledgmentIntentRetryPolicy(
            initial_backoff_ms=1000, max_backoff_ms=300000,
        ),
    ),
    stop_event=shutdown,
)
```

The existing `arecover_next_event_acknowledgment_intent` and `claim.deliver`
accept the same `retry_policy` keyword. Configure the authenticated SDK client,
trusted owner scope, protected storage and exclusively leased idle autocommit
connection as described in the [Python intent protocol](event-acknowledgment-python-intents.md).
The bounded pass still requires a fresh exclusive connection scope per admission.

## Options and compatibility

| Policy field | Default | Bounds |
| --- | --- | --- |
| `initial_backoff_ms` | 1000 | Native integer 1..3600000 milliseconds. |
| `max_backoff_ms` | 300000 | Native integer from the initial value through 3600000 milliseconds. |

An empty policy enables jitter with both defaults. An initial cap above five
minutes requires an explicit maximum at least as large. Booleans, integer
subclasses, floats, strings, None, zero, negative values and reversed bounds are
invalid field values. Recovery requires the exact frozen policy dataclass and
copies its validated values before I/O; mappings and policy subclasses are not
accepted. Validation happens when starting recovery/delivery, not at dataclass
construction.

Leave `backoff_ms` omitted or `None` when supplying a policy. An explicit fixed
delay conflicts with a policy, including `backoff_ms=1000`. Without a policy,
omitted/None backoff selects the existing fixed 1000 ms delay. Explicit fixed
delays retain native-integer bounds 1..3600000. The Python signatures/options now
use None to represent omission, while their effective default remains 1000 ms.
The pass adds its new field after existing fields, preserving their positional
order. Prefer keyword arguments for options.

Invalid policy combinations fail before claim I/O or pass scope acquisition.
For a separately acquired claim, invalid delivery options leave its existing
lease unchanged; provide valid options within that lease or let it expire.

## Durable count and scheduling

For committed recovery claim number `n`, the cap is:

`min(max_backoff_ms, initial_backoff_ms × 2^(n−1))`.

The existing row-locked claim update atomically increments and returns
`delivery_attempts`. Recovery validates the returned native integer within
1..2,147,483,647 before COMMIT and retains it inside the redacted process-local
capability. Unknown COMMIT still returns no usable capability. The count
saturates; doubling stops once the configured cap is reached, bounding work even
for saturated counts.

Each claim selects a positive whole-millisecond delay from one through the cap.
The initial value is a first **cap**, not a minimum wait. Jitter uses a separate
SHA-256 domain over the already-random claim token and unsigned big-endian integer
modulo, matching Go and Node without floating-point conversion. No new entropy
read follows an uncertain HTTP result. The delay exposes no raw token bits.

Only conditional `unconfirmed` settlement writes database now plus the selected
delay to `next_attempt_at`. Later recovery still requires due/unclaimed work.
The timestamp survives process restart and worker changes, while the host owns
when to invoke recovery again. No idle sleep or polling task is added. Confirmed
and superseded settlements retain their existing handling.

Counts measure committed recovery claims, including crashes before HTTP and
commits with lost responses, across all SDKs and fixed-delay workers. Within-claim
callback retries do not increment the count separately. It therefore cannot
prove physical request counts. The pass's `max_attempts` remains a per-call
admission budget and is not a lifetime intent limit.

## Cancellation, uncertainty and worker configuration

The callback/preflight/settlement protocol is shared with fixed-delay recovery.
Saved identity/action/token/result/reason hashes, receipt checks, live local
claim expiry and platform attempt fencing retain their existing contracts.
Business work is never executed by callback recovery.

The pass keeps its shielded ownership boundary, cooperative stop event/native
task cancellation, joined connection cleanup, frozen progress and uncertain
COMMIT precedence. No worker is force-cancelled or connection abandoned. Keep
bounded acquisition, query, HTTP and cleanup operations and join recovery before
closing resources. The [pass contract](event-acknowledgment-python-recovery-pass.md)
describes partial progress and failure categories.

A callback can succeed remotely before response loss or a failed settlement.
When settlement fails or the claim expires, a new due time may not have committed;
another worker can recover after lease expiry without that selected delay.
Preserve retained evidence and reconcile unknown outcomes rather than repeating
business work or changing the saved action.

For unbound rows these settings remain worker fallback; coordinate them across
hosts. Python also supports [stored versioned bindings](event-acknowledgment-stored-retry-policy.md)
which override fallback through settlement, matching Go and Node. Current
Python staging, listing and recovery requires the stored-policy columns even
for unbound rows. Apply the exported upgrade before updating existing workers,
and update every possible claimant before enabling bound staging.
Callback-level attempts, timeout and bounded
Retry-After handling remain as before. This durable policy adds no remote
Retry-After propagation, lifetime exhaustion or automatic dead-letter decision.

See the [host example](../examples/event-acknowledgment-python/README.md#recover-a-bounded-callback-pass).
Local validation covers isolated generation without tests, Python 3.11 syntax,
lint, package inclusion, source/schema comparisons and documentation checks.
No local tests or SDK helper smoke executions are run. Hosted timing, restart,
concurrency, cancellation and uncertain-outcome qualification remains pending
for this increment.
