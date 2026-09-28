# How scaling to zero works

Apps on Gregale FaaS scale to zero. When nobody is using your app
its instances are parked — a snapshot on disk, zero resident RAM.
The next request wakes an instance. On the reference SSD node, Gregale targets
p95 below 350 ms for the platform interval from capacity admission/boot start
through the first upstream byte. This includes the scheduler, snapshot restore,
and Gregale's internal proxy. The full public request also includes Cloudflare,
Internet transit, client distance, and the rest of your app's response, so its
latency can be higher.

This is the trick that makes the per-GB-RAM-hour price work. The
trade-off is that the **first request to a parked app pays the
wake cost**. Subsequent requests hit the warm instance at normal
latency.

## Refresh application state after restore

If your app needs to reconnect a client or refresh an in-memory resource after
snapshot restore, configure an `after_restore` hook in `gregale.yaml`:

```yaml
lifecycle:
  after_restore:
    path: /internal/after-restore
    timeout_ms: 500
```

Gregale sends one `POST` to that path on the restored app's loopback listener
after repairing the guest clock and entropy. Return any 2xx status to allow
readiness. A timeout or other status rejects that restored instance and falls
back to a fresh cold boot. The hook runs only on restore, not on cold boot.
The path must start with `/`; timeout defaults to 500 ms and can be at most
2 seconds. This feature applies to request and service apps.

Make the handler idempotent and require both a loopback peer address and the
`X-Faas-After-Restore: 1` request header. Gregale strips inbound `x-faas-*`
headers at the public proxy, preventing a public caller from supplying that
marker. The hook's work adds to wake latency, so keep it short. Redeploy after
changing the lifecycle configuration; existing snapshots retain the manifest
from their deployment.

You can detect the wake tier on every routed response:

- `x-faas-wake: hot` means an already-running instance served the request.
- `x-faas-wake: restored` means the request admitted an instance from a
  usable snapshot.
- `x-faas-wake: cold` means the request admitted a fresh cold boot. The
  value is retained for CLI compatibility and is useful for retries /
  client-side banners.
- The dashboard's per-app state badge: `◌ sleeping` before
  traffic, `⟳ waking` while the instance restores,
  `● running` once it's serving. The page refreshes every 10 s.

## Opting out: keep N instances warm (Pro, Scale)

On the Pro and Scale plans you can pin a number of instances
permanently resident via `gregale app <slug> --min N`:

```bash
gregale app my-api --min 1   # always keep 1 instance warm
gregale app my-api --min 0   # back to scale-to-zero (default)
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
