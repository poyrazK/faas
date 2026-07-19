# How scaling to zero works

Apps on onebox FaaS scale to zero. When nobody is using your app
its instances are parked — a snapshot on disk, zero resident RAM.
The next request wakes an instance; the snapshot is restored and
your app is serving within ~350 ms.

This is the trick that makes the per-GB-RAM-hour price work. The
trade-off is that the **first request to a parked app pays the
wake cost**. Subsequent requests hit the warm instance at normal
latency.

You can detect a cold wake in two ways:

- The response header `x-faas-wake: cold` on the first request.
  Absent on subsequent requests. Useful for retries / client-side
  banners.
- The dashboard's per-app state badge: `◌ sleeping` before
  traffic, `⟳ waking` while the instance restores,
  `● running` once it's serving. The page refreshes every 10 s.

## Opting out: keep N instances warm (Pro, Scale)

On the Pro and Scale plans you can pin a number of instances
permanently resident via `faas app <slug> --min N`:

```bash
faas app my-api --min 1   # always keep 1 instance warm
faas app my-api --min 0   # back to scale-to-zero (default)
```

Each warm instance counts as always-resident for billing — the
cost is exactly `N × ram_mb × uptime`. The bill is honest about
this; there is no premium for keeping N warm, you simply pay for
N resident GB-hours like any other plan minute.

`--min` must be in `[0, plan max_concurrency]`:

| Plan   | max concurrency | min instances allowed |
|--------|-----------------|------------------------|
| Free   | 1               | 0 (scale to zero only) |
| Hobby  | 2               | 0 (scale to zero only) |
| Pro    | 5               | 0..5                    |
| Scale  | 20              | 0..20                   |

Hobby and Free reject `--min > 0` with `403 plan_min_instances_not_allowed`.
The reaper honors the floor; RAM-pressure eviction does not (the
ceiling is physics, the floor is budget).

## Why scale to zero matters

Every parked app costs nothing to keep. A box that runs 100 apps
and serves 5 of them looks the same as a box that serves 1 of
them — most apps are idle most of the time. Scale-to-zero is
what makes the hobby plan affordable at €9/month.