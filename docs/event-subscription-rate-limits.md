# Consumer delivery token buckets

Application subscriptions can opt into a durable token bucket shared by all
routing and replay admission workers. Each subscription controls its own pacing,
so one slow consumer does not spend another consumer's capacity. This complements
the existing per-second resume cap; both limits apply. Pause and circuit controls
keep precedence. Direct application outbox publishing has a separate limiter.

## Configure and inspect

`GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control`
includes optional `rate_limit` capacity metadata. A new bucket starts disabled at
revision zero. Older responses can omit the field. Use
`PUT .../delivery-control/rate-limit` with:

```json
{
  "command_id": "da855200-c4ad-4d08-8057-5c359c6cdd9e",
  "expected_revision": 0,
  "enabled": true,
  "interval_millis": 100,
  "burst": 20
}
```

This starts with 20 tokens and replenishes one per 100 milliseconds, capped at 20.
Interval is explicit, 10..3,600,000 whole milliseconds; burst is explicit, 1..100.
These configuration bounds live in `pkg/api/limits.go`. They do not change plan
capacity quotas. Mutation requires `deploy:write` or `admin`; reads use existing
read authorization and MFA policy. Database clocks supply production refill time.

Go clients expose `SetEventSubscriptionRateLimit`,
`GetEventSubscriptionRateCommand` and `ListEventSubscriptionRateCommands`; generated
Node and Python clients expose the corresponding operations. Explicit Enabled and ExpectedRevision pointers are mandatory in the Go request,
including false and revision zero.

A matching revision applies configuration and increments the revision. A mismatch
returns HTTP 200 with a **rejected** receipt and the unchanged current revision;
inspect `outcome`, not just HTTP success. Both outcomes allocate an increasing
history sequence. Replaying the same command UUID and original inputs returns the
historical receipt, even if its rejection would now match. Reusing a retained UUID
with changed inputs returns 409. Enable-to-enable changes preserve whole and
fractional available tokens, conservatively rounded and clamped to the new burst.
They do not grant a new burst. Enabling from disabled starts full. Disabling does
not alter legacy per-second limits, pause state, circuit state or deadlines.

After a timeout or other uncertain mutation, look up or replay the **same** UUID.
`GET .../rate-limit/commands/{commandID}` returns retained evidence;
`GET .../rate-limit/commands?after=0` lists up to 50 receipts in increasing sequence
order, with optional `next_after`. Pages are fresh observations, not frozen
snapshots. Receipt expiry can leave gaps. Unavailable evidence does not prove the
command never ran. Command UUIDs belong to this rate-control namespace.

Receipts last 30 days, with at most 128 unexpired receipts per subscription.
New commands return 429 at that bound; known replays remain available. Expired
receipts are removed on subsequent mutations. Expiry ends UUID replay protection.
The control row retains revision and sequence counters independently of receipts.
App/account deletion cascades operational state; controls survive subscription
removal to govern retained captured recipients. Never delete active control rows
to prune audit history: that resets configuration and counters.

## Admission, attempts and failures

The existing subscription advisory lock serializes configuration with admission.
Deferred rows recheck after at most the existing five-second control retry delay,
so relaxing a long interval does not strand raced deferrals until the old refill
time. Waiting-reason checks skip known throttled rows without claiming them.
The database transaction consumes a token alongside invocation/work admission and
routing progress. Failed transactions roll back consumption. Capacity-deferral,
pause, circuit and throttle paths consume no bucket token; existing routing
attempt refunds mean a throttled recipient spends no retry attempt or retry budget.
Pre-existing admission proof bypasses new reservations. Workflow/object recipients
without application subscription UUIDs retain their existing behavior.

Tokens are not refunded after successful admission, including worker crashes or
uncertain commit responses. This caps admission reservations; it is not exact HTTP
wire pacing, a concurrency limit, or proof that consumer execution completed.
Previously admitted work can finish after a rate change. Existing live-delivery
capacity limits apply independently. Per-consumer rate limits do not extend
retention, delivery-age deadlines, retries or producer-key deduplication windows.
Pause time refills the bucket naturally, up to burst. Integer millisecond credit
preserves refill remainders between frequent polls. A database clock behind a
stored refill anchor defers admissions until time catches up; configuration of an
enabled bucket returns conflict during that condition.

Backlog and delivery-health waiting reasons use the existing
`subscription_rate_limited` reason for either cap. They distinguish throttled
recipients from paused/circuit-blocked or runnable work, rather than reporting a
new failed attempt. Capacity status is advisory and can become stale immediately.

## Rollout

Apply migration `20261015123000000_event_subscription_token_bucket.sql` through
the normal release process before enabling the feature. Replace older admission
workers before relying on the bucket: they do not debit token credit. Initial
state is disabled, so existing resume rates keep their behavior during rollout.
No helper applies production DDL. The new table and control columns are operational
clone state, not copied configuration. Draining old workers is necessary for
rollback; the down migration removes bucket controls and command evidence while
preserving legacy pause/rate configuration.

Before applying a change, use the [read-only rate preview](event-subscription-rate-preview.md)
for projected capacity, a reviewed revision, a bounded rate-only admission model
and deadline-risk identities. The preview makes no reservations and extends no
deadlines.
