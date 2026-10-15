# Deadline-aware outbox retry policy

The Go SDK offers an opt-in recovery policy with capped exponential jitter,
server Retry-After guidance, and a lifetime recovery-attempt limit. Existing
callers without `RetryPolicy` retain fixed Backoff behavior.

```go
status, err := faas.RecoverNextEventOutbox(ctx, db, client, accountID, appID,
    faas.EventOutboxRecoveryOptions{
        ClaimDuration: 30 * time.Second,
        RequestTimeout: 10 * time.Second,
        RetryPolicy: &faas.EventOutboxRetryPolicy{
            InitialBackoff: time.Second,
            MaxBackoff: 5 * time.Minute,
            MaxAttempts: 20,
        },
    })
_ = status
_ = err
```

Leave `Backoff` zero when supplying a policy; combining them is invalid. Initial
backoff defaults to five seconds and maximum backoff to five minutes. Both accept
whole milliseconds from one millisecond–one hour, and the maximum must be at least
the initial value. MaxAttempts supports 0–2,147,483,647; zero disables the limit.
An empty policy still enables jitter and Retry-After handling.

## Jitter and scheduling

For claim number `n`, the local delay cap is:

`min(MaxBackoff, InitialBackoff × 2^(n−1))`.

The calculation saturates at the configured cap without unbounded iteration or
duration overflow. Each claim chooses a positive randomized whole-millisecond
delay from one millisecond through that cap. Entropy is derived through a separate
hash of the already-random claim token, so no random-source read can fail after
an uncertain HTTP result. Raw token bits are not exposed through delay values.

After uncertainty, the next attempt is persisted using the database clock. Retry
waiting happens through that due timestamp, not an in-process sleep. Process
restart or another worker therefore preserves the saved schedule. Backoff always
uses the original producer key, saved content and namespace guard.

Policy-bearing outbox calls bypass the SDK's own `WithRetry` transport loop so
recovery receives each final response and owns scheduling. Other SDK requests
and legacy outbox calls retain their transport behavior. Custom transports must
honor contexts and avoid hidden retries if operators need claim counts to match
physical send counts.

## Retry-After

For HTTP 429 and 503 errors, recovery accepts a nonnegative integer-seconds
Retry-After header or an HTTP-date. A valid header takes precedence; otherwise a
nonnegative problem-body `retry_after_seconds` value is used. Malformed/negative
values are ignored. Header parsing is bounded to 256 trimmed bytes; oversized
values fall back to the problem body. Extremely large integer delays saturate at
seven days, the longest supported outbox window, instead of overflowing.

Relative server guidance is a minimum delay from settlement database time. An
absolute HTTP-date is compared by PostgreSQL, avoiding a client-clock conversion.
The next due time starts at the later of database now and the server minimum,
then adds the local jitter, capped at the original `RetryUntil`. This spreads
workers even when they receive the same server minimum. Server guidance may exceed the local exponential cap but
never extends the retry window. A past HTTP-date or zero delay does not shorten
the local jitter delay.

If guidance reaches or exceeds the deadline, the row is scheduled at the deadline;
recovery then blocks it with `retry_window_expired` without another HTTP send.
No Retry-After header or remote diagnostic text is retained in local status.

## Attempt limits and uncertainty

Attempts count committed recovery claims over the row's lifetime, including
claims whose worker crashed before HTTP or whose commit response was lost.
They are not guaranteed physical request counts. The limit is checked while the
publication row is locked, before another claim is created. A row already at its
limit becomes blocked with `attempts_exhausted` without another HTTP call.

A successful final allowed claim still confirms publication. If that claim ends
in uncertainty, settlement blocks it immediately with `attempts_exhausted` and
returns `ErrEventOutboxBlocked`. Permanent HTTP rejection retains its more specific
`publication_rejected` reason. Original deadline expiry takes precedence when
recovery visits expired work. Lost/expired claims still obey existing token and
lease fencing; failed settlement never proves that remote acceptance failed.

Use [read-only reconciliation](event-outbox.md#reconcile-retained-acceptance-without-publishing)
to investigate exhausted work. Attempt exhaustion does not prove that Gregale
rejected the event. Guarded resume preserves attempts, so the same exhausted
limit will block it again without sending. An operator choosing more attempts
must explicitly change the effective policy and resume within the original deadline;
neither control resets counts or extends the window.

These recovery options are the fallback for publications without a saved policy.
All workers handling unbound rows should use a consistent configuration. For a
bound row, recovery uses its [persisted versioned policy](event-outbox-stored-retry-policy.md)
instead of worker retry settings. Increasing a worker limit cannot override a
saved limit; use the guarded policy command. The durable count survives both
configuration paths. Existing health summaries report exhausted rows as blocked, and
payload-free status exposes `attempts_exhausted` separately from deadline expiry.
Recovery attempts are not control-command history entries.

## Existing installation upgrade

Fresh installs use the updated `faas.EventOutboxSchema`. For an existing outbox,
apply `faas.EventOutboxRetryPolicySchema` explicitly inside an application migration
transaction before enabling a policy. It replaces the original SDK-named
`gregale_event_outbox_last_code_check` constraint to allow `attempts_exhausted`.
It adds no columns or platform tables and never rewrites publication identity,
attempt counts, retry deadlines or payloads. Merely re-running CREATE TABLE IF
NOT EXISTS does not upgrade an existing CHECK constraint.

Helpers never execute DDL. If the old constraint remains, attempting to persist
an exhausted state fails as a sanitized storage error and the local update rolls
back; do not interpret it as publication rejection. Publication retention,
ordering, recipient retries and business deduplication guarantees remain unchanged.
