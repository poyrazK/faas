# Plans and pricing

<!-- GENERATED — do not edit by hand; regenerate with `make pricing-md`. -->

Gregale pricing and quotas come from [`pkg/api/limits.go`](../pkg/api/limits.go). The same table is enforced by the API, so this page is generated rather than maintained separately. Prices are monthly and shown in US dollars. Usage beyond the included GB-RAM-hours is billed at $0.01 per GB-RAM-hour on paid plans.

## At a glance

| Plan | Monthly | Deployed apps | PR preview apps | Developer apps | Concurrent instances | RAM / app | Included GB-RAM-hours | App layer | Idle timeout | Max request timeout |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| **Free** | $0 | 1 | 1 | 1 | 1 | 256 MB | 20 | 256 MB | 1m | 30s |
| **Hobby** | $10 | 5 | 2 | 2 | 2 | 512 MB | 200 | 512 MB | 1m | 1m |
| **Pro** | $30 | 25 | 5 | 5 | 5 | 1024 MB | 1000 | 1024 MB | 5m | 5m |
| **Scale** | $99 | 100 | 20 | 10 | 20 | 2048 MB | 3000 | 2048 MB | 10m | 15m |

## What each limit means

- **Deployed apps** is the maximum number of production app records on the plan. `gregale dev` environments have a separate developer-app allowance.
- **PR preview apps** is the separate limit for temporary pull-request workloads. Each preview dependency uses a slot; the whole set is reserved together.
- **Concurrent instances** is the per-app wake/instance ceiling; request concurrency inside one VM is separately bounded by the plan.
- **RAM / app** and **app layer** are hard build/runtime ceilings. Smaller resource profiles remain available where the plan permits them.
- **Included GB-RAM-hours** is the monthly compute allowance. Free stops at its allowance; paid plans can accrue overage at the published rate.
- **Idle timeout** is when an inactive app is parked. A later request wakes it from its snapshot; see [scale-to-zero](cold-wake.md). Functions default to 30s on every plan; any app or function can set its own value from 10s up to twice the plan default.
- **Max request timeout** is the longest `request_timeout_s` an app or function may set (`gregale app APP --request-timeout SECONDS`). Without one, a function request must start its response within 3s and an app request within 30s; a function's whole invocation, a streamed response included, may then run for the longer of its timeout and 30s.

## Developer environments

Each [`gregale dev`](gregale-dev.md) environment keeps a lease that every sync renews. The default lease is 24h; `--ttl` or `dev.ttl` in `gregale.yaml` chooses another value from 1h up to the plan maximum.

| Plan | Developer apps | Maximum lease |
|---|---:|---:|
| **Free** | 1 | 24h |
| **Hobby** | 2 | 72h |
| **Pro** | 5 | 168h |
| **Scale** | 10 | 336h |

## Managed realtime

Realtime is included on every paid plan at no extra charge: Gregale bills compute, and channels, consumer groups and delivery are software. Limits apply only where realtime holds real capacity. Retained history is bounded per endpoint, and functions triggered by channel messages bill as normal compute.

| Plan | Endpoints per app | Endpoints per account | Concurrent connections per account |
|---|---:|---:|---:|
| **Free** | — | — | — |
| **Hobby** | 2 | 10 | 200 |
| **Pro** | 10 | 50 | 1,000 |
| **Scale** | 25 | 250 | 5,000 |

The connection limit counts open WebSocket and SSE connections across all of an account's endpoints. One account may also hold at most 50% of a single realtime node's connections; see [realtime fair use](realtime.md#fair-use-limits).

## Object storage

Object storage bills the bytes it holds. Each plan includes an average amount of stored data per month; storage beyond it costs $0.03 per GiB-month. Requests and egress are not charged. Free stops at its included storage.

| Plan | Included storage | Beyond the included storage |
|---|---:|---|
| **Free** | 1 GiB | stops at 1 GiB |
| **Hobby** | 10 GiB | $0.03 per GiB-month |
| **Pro** | 50 GiB | $0.03 per GiB-month |
| **Scale** | 200 GiB | $0.03 per GiB-month |

Included storage is the average over the month, so 20 GiB held for half a month uses 10 GiB of it. See [object storage](object-storage.md).

## Choose a plan

Start on **Free** for a small public API or a trial. **Hobby** unlocks the paid observability, async, and data surfaces. **Pro** is the normal production tier for teams, while **Scale** raises the app, concurrency, RAM, and usage ceilings. Feature maturity and entitlement are listed in the [capability matrix](capabilities.md).

Plan changes are safe to preview with `gregale plan`; quota errors include the exact observed value, limit, and next action.
