# Durable outbox publishing rate limits

The Go SDK can share a token bucket across recovery workers using the same
application database and account/app scope. This controls new recovery claims;
it does not throttle direct publish API calls, recipient delivery, other databases,
or HTTP already authorized by committed claims. Worker concurrency remains a
separate setting. Ordering and deduplication guarantees remain unchanged.

## Install and configure

Drain and replace older workers before relying on this limit. Install the
application-owned `faas.EventOutboxRateLimitSchema` explicitly before upgrading
recovery workers or health readers. New installations can use the complete
`faas.EventOutboxSchema`. Helpers never execute DDL. Missing scope configuration
means disabled; missing tables or unreadable configuration fail closed.

```go
current, err := faas.GetEventOutboxRateLimit(ctx, db, accountID, appID)
if err != nil {
    return err
}
receipt, err := faas.SetEventOutboxRateLimit(ctx, db, accountID, appID,
    faas.EventOutboxRateLimitOptions{
        CommandID:        commandID, // Persist a nonzero UUID before submitting.
        ExpectedRevision: current.Revision,
        Enabled:          true,
        Interval:         100 * time.Millisecond,
        Burst:            20,
    })
```

This allows an initial burst of 20 claim reservations and replenishes one token
per 100 milliseconds, capped at 20. Interval must be a whole number of milliseconds
from 1 ms through 1 hour; burst must be 1..10,000. Zero values default to one second
and one token. These are SDK configuration bounds, not platform plan quotas.
Set Enabled false with a new command ID and current revision to disable.

The database clock supplies refill evidence. Integer millisecond credit preserves
fractional tokens without floating-point drift; repeated submillisecond polls do
not discard the refill remainder. Idle time, including time paused, replenishes
capacity up to the burst. Changing an enabled limit preserves the available whole
and fractional token count, clamped to the new burst, with conservative rounding.
An enabled-to-enabled no-op does not grant a fresh burst. Enabling from disabled
starts with a full bucket. Database clock rollback fails closed until the stored
refill anchor is no longer in the future.

## Reservation and failure behavior

The bucket row lock lasts only through the claim transaction. Token consumption,
claim lease and attempt increment commit together. Empty polls, blocked-row
maintenance and rolled-back claims consume no tokens. When capacity is exhausted,
recovery returns `ErrEventOutboxRateLimited` before claiming or incrementing an
attempt. `errors.As` can obtain `*EventOutboxRateLimitWaitError`, whose RetryAt and
RetryAfter describe database-clock capacity at observation, not a reservation.

A committed reservation consumes capacity even if the process crashes, the commit
response is lost, preflight loses the lease, or HTTP fails. Tokens are not refunded.
This conservative behavior avoids bypassing the limit during uncertain outcomes.
HTTP occurs after commit, so concurrent requests can bunch after scheduling delays;
the setting limits claim reservations rather than guaranteeing exact wire timing.
Existing leases may finish after a limit change. Retry deadlines, attempt limits,
next-attempt schedules, producer keys and saved retry policies stay intact.
Rate limiting can therefore leave a publication expired before it is visited.

Configuration changes share the publishing scope advisory lock: an exclusive
change waits for claim transactions holding shared locks, then atomically stores
configuration, revision, monotonic history sequence and receipt. It never waits
for their HTTP completion. Use context deadlines for database lock waits.

## Commands and retention

`GetEventOutboxRateLimitCommand` retrieves a historical receipt;
`ListEventOutboxRateLimitCommands` pages retained history by After sequence with
Limit default 50, maximum 100, and NextAfter when another page exists. Retrieval
success does not mean the command was applied: inspect Outcome and ErrorCode.

A revision mismatch commits a rejected/conflict receipt without changing the
bucket. Repeating the same normalized parameters and command UUID returns the
original receipt and error, including a rejection. Reusing the UUID with different
parameters conflicts. On `ErrEventOutboxCommitUnknown`, look up or replay the same
command; do not submit a new ID just to retry an uncertain result. Receipts contain
metadata only and are historical observations, not live capacity evidence.
Rate commands use their own namespace, separate from pause and per-publication
commands. Command IDs do not authenticate operators; database access and caller
identity policy remain application responsibilities.

Retain the authoritative rate-limit row for the lifetime of the scope. Deleting
it disables enforcement and resets revisions and history sequence allocation.
Keep command receipts for the required replay/audit horizon; deleting receipts
also deletes their idempotency protection. No helper prunes either table.

## Worker and health behavior

Worker pools back off rate-limited polls, using the capacity hint with a delay
capped at IdleMax so configuration changes can be noticed. A `rate_limited`
callback with code `publishing_rate_limited` is emitted once per worker per
consecutive rate-limited polling episode. RetryAt and RetryAfterMillis are optional
observation metadata. Workers may each report an episode; callbacks are not a
durable audit stream. Publishing pause takes precedence over rate limiting.

Health samples the bucket with its existing bounded unpublished-row snapshot.
RateLimitEnabled, RateLimitRevision, PublishingRateLimited and optional
RateLimitNextAt describe the scope. ThrottledDue counts otherwise eligible sampled
work waiting for capacity while not paused. Due and Stalled exclude throttled
work; PausedDue takes precedence when paused. Existing live claims, expiry and
blocked counts remain visible. Eligibility ages include work waiting on either
control. Unknown versions or future rate timestamps make health incomplete.

Due/stall watches report unknown or confirmation_lost with reason
`publishing_rate_limited` for fresh complete throttled samples; they do not resolve
incidents from the artificially reduced counts. Pause has priority, while blocked
and expired conditions continue normally. All normal freshness, completeness and
sustained-evidence requirements still apply.
