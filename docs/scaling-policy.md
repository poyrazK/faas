# Scaling policy

Scaling is configured per app. Keep at least one instance for latency-sensitive services, or allow zero to park idle workloads.

```bash
gregale app APP_ID scale --min 1 --max-concurrency 5
gregale app APP_ID scale --min 0 --max-concurrency 20
gregale app APP_ID scale --max-concurrency 10 --concurrency-overflow drop
gregale app APP_ID scale --concurrency-overflow queue --max-queue-wait-ms 2500
gregale app APP_ID
```

The platform uses request concurrency and resource pressure to choose a replica count in the configured range. A zero-min app parks after the plan's idle timeout and cold-wakes on the next request; see [Cold wake](cold-wake.md) for client expectations.

The API validates min/max values against the selected plan and rejects impossible combinations before changing the app. Set a realistic maximum to protect downstream services, and make handlers idempotent because retries can overlap during a scale event.

## Manifest form

For deploys, the policy can be kept beside the source in `gregale.yaml`:

```yaml
scaling:
  min_instances: 1
  max_instances: 4
  target:
    metric: rps
    value: 10
  scale_out_cooldown_s: 5
  scale_in_cooldown_s: 60
  concurrency_overflow: queue # queue or drop
  max_queue_wait_ms: 2500 # 0 uses the plan default
  wake_max_queue_depth: 32 # 0 uses the plan default; max 8x plan default
  wake_max_queue_wait_seconds: 30 # 0 uses the plan default; max 60
```

The CLI validates the shape and shows the nested policy in `gregale deploy
--dry-run`. The server then applies the complete policy atomically and checks
plan quotas, cooldown bounds, and workload compatibility.

`concurrency_overflow: queue` keeps requests in the bounded admission queue;
`drop` returns HTTP 429 when the app's concurrency boundary is saturated.
`max_queue_wait_ms` overrides the plan wait budget and is bounded by the API.

`wake_max_queue_depth` and `wake_max_queue_wait_seconds` independently bound
the per-app cold-wake queue. Zero keeps the plan default; the depth override is
limited to eight times the plan default and the wait override to 60 seconds.
