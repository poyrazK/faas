# Outbound pool load checks

`make test-outbound-resilience` runs regression checks for deadline propagation,
bounded pool saturation, recovery, stalled AMQP calls and capped/cancellable
retry waits. Postgres integration checks use `DATABASE_URL`; a missing URL skips
those checks in the ordinary unit suite.

The separate load gate requires explicit isolated Postgres and Redis endpoints:

```sh
FAAS_RESILIENCE_DATABASE_URL='postgres://faas@127.0.0.1:55432/faas?sslmode=disable' \
FAAS_RESILIENCE_REDIS_URL='redis://127.0.0.1:56379' \
make test-outbound-resilience-load
```

The full CI workflow's `outbound-pool-load` job runs this gate with isolated
Postgres/Redis services at both 32 and 64 concurrent callers.

The default profile runs 32 concurrent callers with 20 operations each. It
exercises four Postgres slots with 5ms queries, the response cache's eight-slot
Redis pool, and the retry budget's eight-slot Redis pool. Every operation must
succeed; p95 must remain below 250ms. Each request has a one-second budget;
the cache and retry-budget clients also retain their shorter operation limits.
Checks assert pool recovery and that concurrent retry spends create no extra
allowance. The retry-budget test creates one uniquely named hash, expiring after
five minutes; the other workloads are reads.

These are strict performance gates: every backend error fails the run, including
Redis operations that exceed their production 100ms budget. Use an idle runner
with isolated services and sufficient free memory. Profile failures need pool
wait, CPU and backend latency measurements to establish the limiting resource.
Repeated local runs exposed Redis failures and DB p95 breaches; the measured
results and unresolved limits are recorded in
[the audit](../../docs/outbound-resilience-audit.md#repeatable-saturation-and-load-gates).

Configure the profile to match the intended dependency demand:

| Variable | Default | Range |
| --- | --- | --- |
| `FAAS_RESILIENCE_CONCURRENCY` | 32 | 1–128 |
| `FAAS_RESILIENCE_ITERATIONS` | 20 per caller | 1–1000 |
| `FAAS_RESILIENCE_PG_POOL_SIZE` | 4 | 1–32 |
| `FAAS_RESILIENCE_REQUEST_BUDGET` | `1s` | positive, at most `10s` |
| `FAAS_RESILIENCE_P95_BUDGET` | `250ms` | positive, no greater than request budget |

Verbose test output reports operations, failures, throughput, p50/p95/max
latency, Postgres connection counts and accumulated acquisition time. Repeat
with the expected concurrency and peak/rollout overlap. This synthetic profile
measures the specified fixtures; production fleet headroom also depends on
query mix, network latency, replica counts and Postgres server capacity.

The Make targets serialize package builds and use the repository's compiler
memory limits. These gates require no daemon capabilities. The broader
end-to-end suite has additional daemon, capability and fixture requirements;
passing these gates does not establish a passing whole-repository suite.
The load gate remains separate so ordinary tests do not need Redis or a
performance-sized runner.
