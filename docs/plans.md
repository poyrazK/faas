# Plans and pricing

<!-- GENERATED — do not edit by hand; regenerate with `make pricing-md`. -->

Gregale pricing and quotas come from [`pkg/api/limits.go`](../pkg/api/limits.go). The same table is enforced by the API, so this page is generated rather than maintained separately. Prices are monthly and shown in euros. Usage beyond the included GB-RAM-hours is billed at €0.01 per GB-RAM-hour on paid plans.

## At a glance

| Plan | Monthly | Deployed apps | PR preview apps | Developer apps | Concurrent instances | RAM / app | Included GB-RAM-hours | App layer | Idle timeout | Rollback history |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| **Free** | €0 | 1 | 1 | 1 | 1 | 128 MB | 5 | 256 MB | 1m | 2 |
| **Hobby** | €9 | 5 | 2 | 2 | 2 | 256 MB | 50 | 512 MB | 1m | 3 |
| **Pro** | €29 | 25 | 5 | 5 | 5 | 512 MB | 250 | 1024 MB | 5m | 3 |
| **Scale** | €99 | 100 | 20 | 10 | 20 | 1024 MB | 1500 | 2048 MB | 10m | 5 |

## What each limit means

- **Deployed apps** is the maximum number of production app records on the plan. `gregale dev` environments have a separate developer-app allowance.
- **PR preview apps** is the separate limit for temporary pull-request workloads. Each preview dependency uses a slot; the whole set is reserved together.
- **Concurrent instances** is the per-app wake/instance ceiling; request concurrency inside one VM is separately bounded by the plan.
- **RAM / app** and **app layer** are hard build/runtime ceilings. Smaller resource profiles remain available where the plan permits them.
- **Included GB-RAM-hours** is the monthly compute allowance. Free stops at its allowance; paid plans can accrue overage at the published rate.
- **Idle timeout** is when an inactive app is parked. A later request wakes it from its snapshot; see [scale-to-zero](cold-wake.md).
- **Rollback history** is how many of an app's newest deployments, including the live one, can be rolled back to. Older deployments are reclaimed nightly and `gregale rollback` to them returns `rollback_target_unavailable`; see [safe changes](deploys.md#safe-changes).

## Developer environments

Each [`gregale dev`](gregale-dev.md) environment keeps a lease that every sync renews. The default lease is 24h; `--ttl` or `dev.ttl` in `gregale.yaml` chooses another value from 1h up to the plan maximum.

| Plan | Developer apps | Maximum lease |
|---|---:|---:|
| **Free** | 1 | 24h |
| **Hobby** | 2 | 72h |
| **Pro** | 5 | 168h |
| **Scale** | 10 | 336h |

## Choose a plan

Start on **Free** for a small public API or a trial. **Hobby** unlocks the paid observability, async, and data surfaces. **Pro** is the normal production tier for teams, while **Scale** raises the app, concurrency, RAM, and usage ceilings. Feature maturity and entitlement are listed in the [capability matrix](capabilities.md).

Plan changes are safe to preview with `gregale plan`; quota errors include the exact observed value, limit, and next action.
