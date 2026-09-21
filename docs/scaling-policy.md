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
  targets:
    - metric: concurrent_requests
      value: 80
    - metric: cpu
      value: 70
  scale_out_cooldown_s: 5
  scale_in_cooldown_s: 60
  concurrency_overflow: queue # queue or drop
  max_queue_wait_ms: 2500 # 0 uses the plan default
  wake_max_queue_depth: 32 # 0 uses the plan default; max 8x plan default
  wake_max_queue_wait_seconds: 30 # 0 uses the plan default; max 60
```

## Scaling signals

Each entry under `targets` states how much load **one instance** should carry.
Declaring the signals is the whole configuration surface: you do not write a
scaling rule, choose a stabilization window, or decide how signals combine.

| metric | meaning | good for |
|---|---|---|
| `concurrent_requests` | in-flight requests per instance | request apps; the signal a latency target is really reaching for |
| `rps` | requests per second per instance | steady traffic with predictable per-request cost |
| `cpu` | max CPU percent across instances | CPU-bound work whose request count understates its cost |
| `queue_depth` | backlog each worker should drain | job and worker apps |

When more than one target is declared, Gregale evaluates each independently
and provisions for whichever asks for the most instances. So:

```yaml
scaling:
  targets:
    - metric: concurrent_requests
      value: 100
    - metric: cpu
      value: 70
```

means "100 in-flight requests per instance is the target, **and** never let an
instance sit above 70% CPU" — if requests stay flat but CPU climbs, the app
still scales out. Each metric may appear at most once, and every value must be
greater than zero.

The single `target:` form is still accepted and behaves as a one-element
`targets` list, so existing manifests keep working unchanged.

`p99_latency_ms` was accepted by older releases and never did anything — no
component ever measured a per-app p99 for scaling. It is now rejected with a
422 pointing at `concurrent_requests`, which is what rises first when an
instance starts queueing.

The CLI validates the shape and shows the nested policy in `gregale deploy
--dry-run`. The server then applies the complete policy atomically and checks
plan quotas, cooldown bounds, and workload compatibility.

`concurrency_overflow: queue` keeps requests in the bounded admission queue;
`drop` returns HTTP 429 when the app's concurrency boundary is saturated.
`max_queue_wait_ms` overrides the plan wait budget and is bounded by the API.

`wake_max_queue_depth` and `wake_max_queue_wait_seconds` independently bound
the per-app cold-wake queue. Zero keeps the plan default; the depth override is
limited to eight times the plan default and the wait override to 60 seconds.
