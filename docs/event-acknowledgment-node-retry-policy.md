# Adaptive Node acknowledgment recovery retries

Node saved-callback recovery accepts an optional `retryPolicy` with capped
exponential full jitter. It uses the same durable count, cap calculation and
database-clock due-time contract as the [Go policy](event-acknowledgment-go-retry-policy.md).
Omitting the policy preserves the fixed one-second default and explicit
`backoffMs` values.

```js
import { recoverEventAcknowledgmentIntents } from '@gregale/sdk-node';

const progress = await recoverEventAcknowledgmentIntents(pool, accountId, appId, {
  signal: shutdown.signal,
  maxAttempts: 100,
  concurrency: 4,
  retryPolicy: { initialBackoffMs: 1000, maxBackoffMs: 300000 },
});
```

The exported TypeScript interface is `EventAcknowledgmentIntentRetryPolicy`.
The same `retryPolicy` option is accepted by
`recoverNextEventAcknowledgmentIntent` and `claim.deliver`:

```js
const status = await recoverNextEventAcknowledgmentIntent(pool, accountId, appId, {
  signal,
  claimDurationMs: 30000,
  retryPolicy: { initialBackoffMs: 1000, maxBackoffMs: 300000 },
});
```

For separate acquisition, call `claimEventAcknowledgmentIntent` using its existing
options, then `claim.deliver(pool, { signal, retryPolicy })`. Configure the
authenticated SDK runtime, trusted owner scope, protected intent storage and pool
as described in the [Node intent protocol](event-acknowledgment-node-intents.md).

## Options and snapshots

| Policy field | Omitted default | Bounds |
| --- | --- | --- |
| `initialBackoffMs` | 1000 | Integer 1..3600000 milliseconds. |
| `maxBackoffMs` | 300000 | Integer from the effective initial value through 3600000 milliseconds. |

`retryPolicy: {}` enables jitter with both defaults. Defaults apply independently:
an initial cap above five minutes requires an explicit maximum at least as large.
Zero, negative, fractional, nonfinite and nonnumeric values are invalid. Omit
`backoffMs` when supplying a policy; specifying both is invalid, even if the fixed
delay equals the default. An undefined policy retains fixed-delay behavior;
null is invalid.

Retry fields and nested policy fields are read from own data properties and
copied/frozen before I/O. Policy objects accept only `initialBackoffMs` and
`maxBackoffMs`; unknown own keys, symbols and accessors are rejected. Inherited
fields are not read. Descriptor/proxy errors become a fixed `TypeError` without
retaining their cause, and retry configuration getters are not invoked. Keep other
single-iteration options immutable while a call is active. The bounded pass
also snapshots its closed options and callback overrides.

Invalid policies fail before claiming rows in single-iteration recovery or
starting pass workers. Separate acquisition creates its claim before the host
chooses delivery options; an invalid delivery policy leaves that claim to expire
unless the host supplies valid options within its existing lifetime.

## Count and delay

For committed recovery claim number `n`, the local delay cap is:

`min(maxBackoffMs, initialBackoffMs × 2^(n−1))`.

The claim update increments and returns `delivery_attempts` atomically under the
existing row lock. The count is decoded from canonical decimal text and checked
within 1..2,147,483,647 before claim COMMIT. Doubling stops at the configured cap,
so a saturated count does not cause unbounded work or numeric overflow.

The selected delay is a positive whole millisecond from one through that cap.
These fields specify caps, not minimum waits. A domain-separated SHA-256 hash of
the claim's already-random token supplies jitter; its first eight bytes use the
same unsigned big-endian calculation as Go. BigInt modulo preserves that entropy
without rounding through a JavaScript number. No extra randomness is requested
after an uncertain callback, and raw claim-token bits are not exposed through
the delay.

Only an `unconfirmed` settlement writes database now plus the delay to
`next_attempt_at`. The timestamp survives restart and worker changes. Later
claims still require due work and an available local claim. No sleep, scheduler
or background worker is added. Confirmed/superseded settlements keep their
existing handling.

Counts measure committed recovery claims, including workers that crashed before
HTTP and claim commits with lost responses. They include claims from fixed-delay
and other-language workers. Callback-level HTTP retries do not each increment
this count; it is not a physical-request counter. The pass's `maxAttempts`
remains a per-invocation admission budget, separate from the durable count.

## Fences, abort and configuration

Saved callback identity, action, token, result/reason and hashes retain the
existing protocol. Negative callbacks still check the receipt, HTTP uses the
remaining local database lease, and settlement requires the same live claim
token. Platform attempt fencing and unchanged-input idempotency remain necessary.
This policy sends no business work and chooses no negative disposition.

The bounded pass retains joined abort, pool-release guards, fixed failure
categories and frozen scalar progress. Uncertain COMMIT still takes precedence.
Single-iteration calls retain their native abort and sanitized storage/callback
contracts. A callback can succeed remotely before a lost response or failed local
settlement. If settlement fails or the claim expires, there may be no newly
committed due time; after expiry, another worker may recover without the selected
delay. Preserve and reconcile retained evidence.

For unbound rows these settings remain worker fallback; coordinate them across
hosts. Node also supports [stored versioned bindings](event-acknowledgment-stored-retry-policy.md)
which override fallback through settlement, matching Go and Python. Current
Node staging, listing and recovery requires the shared stored-policy columns,
even for unbound rows. Apply the exported upgrade before updating existing
workers, and update every possible claimant before enabling bound staging.

Callback attempts, timeouts and bounded within-claim Retry-After handling retain
their existing implementation. This durable policy does not propagate remote
Retry-After guidance or impose a lifetime attempt limit, exhausted state or
automatic dead-letter action. See the
[pass contract](event-acknowledgment-node-recovery-pass.md) and
[host example](../examples/event-acknowledgment-node/README.md#recover-a-bounded-callback-pass).

Local validation covers generation parity, TypeScript/declaration builds,
package inclusion, example syntax, source/schema comparisons and documentation
checks. No local tests or SDK helper smoke executions are run. Hosted timing,
restart, concurrency, abort and uncertain-outcome qualification remains pending
for this increment.
