# ADR-331 · Realtime callback retry backoff

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Persist a jittered exponential retry timestamp after each
  failed durable callback. Start at the existing one-second retry interval and
  cap at one minute by default; `FAAS_REALTIME_CALLBACK_RETRY_MAX_INTERVAL`
  can raise the cap to at most one hour. Honor valid `Retry-After` values on
  HTTP 429 and 503 responses as a minimum delay, bounded by the configured
  cap. Preserve the existing attempt limit and dead-letter behavior.
- **Why:** Durable callbacks previously retried every second regardless of
  failure count or receiver guidance. A sustained receiver outage could cause
  repeated bursts and move recoverable callbacks to dead letters too quickly.
- **Consequences:** The next eligible delivery time remains in the fsynced
  outbox record and survives daemon restarts. Jitter spreads retries after
  simultaneous failures. Operators can increase the retry cap while keeping a
  one-hour hard ceiling; callbacks still dead-letter after the configured
  attempt budget.
- **Rejected alternatives:** Fixed one-second retries keep pressure on an
  unhealthy receiver. An uncapped `Retry-After` can hold callback records for
  an unbounded period and delay operator recovery.
