# ADR-20261031100001: Failed event deliveries retry or dead-letter; held apps step aside

Date: 2026-10-31
Status: Accepted
Amends: [ADR 20261029190000](20261029190000-event-delivery-retry-backoff.md) (event delivery retry backoff), [ADR-134](134-durable-async-contract.md) (durable invocation retries)
Related: [ADR 20261029093000](20261029093000-event-ga-gate.md) (event GA gate), [ADR 20261023115741](20261023115741-every-capability-behind-a-platform-flag.md) (capability holds)

## Context

A blocker hunt on 2026-10-31 found three gaps in how the drain settles an
event delivery.

1. **A handler that throws is never retried or dead-lettered.** The runner
   answers a thrown handler with 500 and `handler_error`; gatewayd-internal
   reports that, like any 4xx, as the app's own failure, and the scheduler
   turns it into `ErrPermanentInvoke`. The drain then fails the row with no
   retry (`state = 'failed'`). Dead-letter capture only runs for
   `dead_letter`, so the event was neither retried, nor in the dead-letter
   queue, nor replayable from it. ADR 20261029190000's backoff only ever
   applied to failures outside the app: network errors, wakes, 5xx from the
   platform.
2. **An app's `retry_policy` is unbounded.** The app's curve replaced the
   event backoff whole. A policy without `max_seconds` doubles forever, and
   one with a large cap could hold a keyed delivery's lane for days, past
   the 30 s keyed cap the ADR's amendment set.
3. **A held app starves other tenants.** When a strict compute budget holds
   an app, the drain returned without touching the row. The row stayed due,
   so every walk met it again first. A walk reads at most 32 pages of 64
   rows, so an app with a couple of thousand held rows hid the due work of
   every other app on the node behind it.

The owner chose (2026-10-31): retry thrown errors and then dead-letter them,
dead-letter other 4xx at once, bound the app policy, and defer held rows.

## Decision

For an event delivery (an async invocation or replay carrying the
`x-gregale-event-subscription-id` header), a failure the app's own code
returned is settled by its status:

| Handler answer | Settlement |
|---|---|
| 500 (thrown, `handler_error`), any other 5xx, 408, 429 | Retried with the event backoff; dead-lettered when the plan's attempts run out. |
| Any other 4xx | Not retried. Dead-lettered at once, so it can be replayed after a fix. |

The scheduler keeps the status on the error (`applicationStatusError`,
which still unwraps to `ErrPermanentInvoke`). The drain dead-letters a 4xx
by failing the row with a retry delay and an attempt budget equal to the
attempts already spent, so the store's existing budget rule moves it to
`dead_letter` and the usual dead-letter capture runs. Other invocations,
and failures that did not come from the app's code, keep today's verdict.

The app's `retry_policy` still shapes an event delivery's retries, within
bounds: a policy without `max_seconds` takes the event cap (5 minutes, or
30 s for a keyed delivery), and no policy waits more than 5 minutes
between attempts. The plan still caps its attempts.

A row of an app held by a strict compute budget moves back by one minute
(`financialHoldDeferral`), as rows held by a capability flag already do. Its
attempts are untouched: a hold is an admission decision, not a failed
execution.

## Consequences

- A handler that throws gets the plan's retries, then lands in the
  dead-letter queue as `event_subscription`, replayable like any other
  dead letter. A handler that rejects its input lands there after one
  attempt.
- Dead-letter volume rises by the deliveries that used to end as plain
  `failed` rows. They are kept for 30 days like other dead letters.
- An app that relied on a retry policy with very long waits now waits at
  most 5 minutes between event delivery attempts.
- A held app's due work is looked at once a minute instead of on every
  walk, so other tenants' work is no longer starved behind it. Held work
  resumes within a minute of the hold lifting.
- No schema change. An older scheduler keeps today's behaviour; both run
  side by side.
