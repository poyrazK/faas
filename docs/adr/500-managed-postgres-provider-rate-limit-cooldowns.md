# ADR-500: Honor managed PostgreSQL provider rate-limit cooldowns

- **Status:** accepted
- **Date:** 2026-10-03
- **Decision:** On a Neon HTTP 429, defer subsequent requests through the same
  provider instance until `Retry-After` expires. Accept positive decimal seconds
  and future HTTP dates. Use a one-minute fallback for absent, zero, expired,
  malformed, or overflowing values. Consumption responses establish only a
  consumption cooldown; other API responses establish a general cooldown that
  also covers consumption. Preserve context cancellation during response-body
  reads and reject canceled requests before transport or cooldown checks.
- **Why:** The adapter discarded retry guidance and immediately sent more
  requests into an exhausted quota, including create-response recovery reads.
  Consumption has a lower shared quota than the general API. Its exhaustion
  must not delay lifecycle work or credential revocation. Cancellation during
  body reads was incorrectly returned as provider unavailability.
- **Consequences:** Cooldowns are safe under concurrent calls and cannot be
  shortened by an earlier request's later response. They return the existing
  sanitized unavailable error without sleeping or retrying a mutation. Resource
  locks (423) do not establish account cooldowns. Successful complete responses
  retain their existing handling. State is local to a provider instance and
  resets on restart; already dispatched requests are not canceled. No schema,
  customer quota, configuration, or public error contract changes.
- **Rejected alternatives:** Retrying POST internally risks duplicate provider
  resources after an ambiguous response. Blocking every API call on consumption
  exhaustion delays unrelated operations. Treating this local backoff as an
  account-wide budget would hide traffic from other backends, replicas, and
  external clients. Shared account pacing, fair recovery scheduling, and
  fleet-wide priority for missing windows remain separate work.

Neon's [consumption guide](https://neon.com/docs/guides/consumption-metrics)
describes a separate shared consumption limiter of approximately 50 requests
per minute per account. The [API rate-limit reference](https://api-docs.neon.tech/reference/api-rate-limiting)
documents general API limits and HTTP 429 handling. The fallback is Gregale's
retry policy, not a claim about the provider's refill time.
