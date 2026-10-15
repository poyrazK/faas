# ADR-1341 · Rate-limit token leasing

- **Status:** proposed
- **Date:** 2026-10-15
- **Amends:** ADR-104 amendment 5 (central rate-limit mode) and ADR-040
  (per-account limits). Keeps the shared Postgres counter as the authority.
- **Decision:** in central mode a gateway takes tokens from the shared
  counter in leases and spends them locally, instead of consulting Postgres
  for every request.
- **Why:** central mode writes the control-plane database on every app
  request. On production-us (Oct 15, 2026) 200 requests to one app produced
  390 writes to `pg_ratelimit_counters` (one account and one app counter per
  request) against 0 in a 75 s idle baseline. With the request-ID journal
  that is about three control-plane writes per request, on the 2-vCPU host
  that saturated at about 157 req/s. The coalescer (one statement in flight
  per counter per gateway) caps writes at about one per database round trip,
  so a hot counter still costs hundreds of writes per second, and every
  request still waits for a round trip to the control plane.

## Design

### Leases

A lease is a number of tokens a gateway has already debited from the shared
counter, held locally for one counter key (`scope:subject:plan`). A request
spends one token from the lease without touching Postgres. Tokens are
debited before use, so leasing never admits more than the shared bucket
allows; its error is in the other direction (below).

A lease is taken with the existing `ConsumeTokens(n)` statement, which
grants `min(n, available)` atomically, creates the counter row on first use,
and reports the shared remainder. No schema change.

### Lease size

```
L = clamp(ceil(r * W), 1, max(1, floor(f * burst)))
```

- `r` — this gateway's recent request rate for the counter (EWMA over 1 s).
- `W` — lease window, 250 ms: a lease should last about this long.
- `f` — lease fraction, 0.10: one gateway never holds more than 10% of the
  bucket's burst.

At each plan's app limit, with two gateways splitting traffic evenly:

| Plan | App limit (rps / burst) | Lease cap (10% of burst) | Lease at the limit | Statements per counter per gateway |
| --- | --- | --- | --- | --- |
| Free | 5 / 20 | 2 | 1 | 2.5 /s |
| Hobby | 20 / 100 | 10 | 3 | 3.3 /s |
| Pro | 100 / 500 | 50 | 13 | 3.8 /s |
| Scale | 500 / 2,000 | 200 | 63 | 4.0 /s |

A quiet counter gets leases of 1, which behaves like today's per-request
consult. A busy counter costs at most about `1/W` = 4 statements per second
per gateway, however many requests it serves.

### Refill without waiting

When half of a lease is spent, the gateway asks for the next one in the
background (the coalescer still keeps one statement in flight per counter).
In steady traffic no request waits for Postgres. A request that finds the
lease empty while the refill is in flight waits for it, as every request
does today.

### Expiry

Unspent tokens expire 1 s after the lease was granted. A token stands for
permission to admit a request now; holding it longer would let a gateway
save up a burst the bucket never granted. Expired tokens are counted, not
returned.

When the shared bucket is under pressure (the last grant was partial, or it
reported fewer than `2L` tokens left), the gateway returns unspent tokens at
expiry with one new `ReturnTokens` statement (`tokens = LEAST(burst, tokens + n)`),
the only statement leasing adds. That keeps a limit close
to exact for a customer pacing at it, at the cost of one extra write per
lease only while the bucket is nearly empty.

### Empty bucket

A grant of 0 marks the counter empty until one token should have refilled
(`1/rps`, between 50 ms and 1 s). Requests in that window get 429 from the
gateway without a statement. Today a 429 storm still costs one statement per
request, or per round trip once the coalescer batches; with leasing it costs
at most one per backoff per gateway.

### Accuracy

- **No over-admission** against the shared bucket: every admitted request
  spent a token that was debited first. (The breaker fallback during a
  Postgres outage is unchanged and still admits at the full plan rate per
  gateway.)
- **Bounded under-admission:** at most `N * L` tokens can expire unspent
  when traffic moves between gateways or stops, `N` being the number of
  gateways. With two gateways and `f = 0.10` that is at most 20% of one
  burst, once, and the bucket refills at its normal rate.
- **Response headers:** `X-RateLimit-Remaining` becomes the shared remaining
  from the last grant plus this gateway's unspent lease. It is an
  approximation: other gateways may have spent tokens since that grant.

### What it covers

Every key that goes through `allowTokenWithCentralKey`: app limits, account
limits, edge-rule throttles and consumer keys. Pre-auth source budgets
(`coordination = "central"` routes) keep their own backend and are out of
scope; they are rare and per source.

### Failure

A lease that fails opens the existing breaker for `centralBreakerWindow`
(1 s) and the request is decided locally, as today. Leases already held are
still spent.

## Configuration and rollout

- `[ratelimit] lease = true` in gatewayd-internal's TOML, default `false`, so
  central mode keeps its current behaviour until an environment opts in.
  `lease_window_ms` (250) and `lease_fraction` (0.10) are tunables, not plan
  limits, so they stay in the gateway's TOML rather than `pkg/api/limits.go`.
- Metrics: `gateway_ratelimit_lease_requests_total{scope, result}` (`granted`,
  `partial`, `empty`, `failed`), `gateway_ratelimit_lease_tokens_total{scope,
  outcome}` (`granted`, `spent`, `expired`, `returned`).

## Verification

1. **Property test:** simulate N gateways with random arrivals against one
   shared in-memory bucket with the SQL's semantics. Assert that admissions
   never exceed the token-bucket allowance at any time, and fall short of it
   by at most `N * L`.
2. **Write bound:** under constant load a counter costs at most `1/W + 1`
   statements per second per gateway.
3. **Production check** with the Snapbox test app:
   - `pg_ratelimit_counters` writes per request at 100 req/s or more fall
     from 1.95 to under 0.05;
   - driving twice the plan rate at a Free-plan test app admits within
     ±10% of the plan's 5 req/s.

## Rejected alternatives

- **Local buckets split by gateway count:** needs N everywhere, and uneven
  routing (warm-node affinity, sessions) makes a gateway's share wrong.
  ADR-070 already rejected per-process limits for multi-box fleets.
- **A dedicated counter service (Redis or similar):** new infrastructure on
  the control plane, and still a network round trip on every request.
- **Larger coalescing windows:** delaying requests to batch them adds
  latency to every request and still scales writes with traffic.
