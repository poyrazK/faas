# Route status

`gregale routes status` shows every route of an app on one line with the
protections that apply to it:

```sh
gregale routes status my-api
gregale routes status my-api --since 24h --json
```

| Column | Source |
| --- | --- |
| `TENANTS`, `REQUESTS` | Observed traffic on the serving deployment over `--since` (default 7 days). |
| `CANARY` | [Route health](route-health.md) selection and mode; during a canary, the current verdict, marked `(pooled)` for low-traffic evidence. |
| `PRODUCTION` | [Production route budgets](route-production-monitoring.md): `report` or `rollback` and the current verdict. |
| `CONTRACT` | Whether the serving deployment's captured OpenAPI document declares the route. |
| `GAPS` | What is missing (below). |

Rows join on the gateway-normalized method and path. A contract operation such
as `GET /users/{userId}` joins the observed `GET /users/{id}` only when that
parameter shape is unique on both sides. Routes are ordered by tenants, then
requests.

Each route has one protection level: `rollback` (an error budget with
`--on-violation rollback`), `enforced` (an enforcing canary selector),
`monitored` (any canary selector or production budget), `policy_only` (saved
route requirements only) or `none`. The summary counts only canary and
production protection, because requirements check policy rather than release
health.

| Gap | Meaning |
| --- | --- |
| `traffic_without_protection` | The route has traffic but no canary selector or production budget. |
| `traffic_outside_contract` | The route has traffic but the captured contract does not declare it. |
| `contract_route_without_traffic` | The contract declares the route but no traffic was observed. |
| `rollback_needs_error_budget` | The monitor rolls back, but this route only has a latency budget. |

The latest production incident and its automatic rollback decision are listed
below the table. The command only reads. Each source is read separately: if
one is unavailable, for example request telemetry on a plan without it, that
section is listed under `Unavailable` and the rest is still shown. Saved
requirement groups are not expanded into routes.
