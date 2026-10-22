# Dashboards

Each app has its own dashboard. A custom dashboard is yours to compose: put
checkout latency next to the payment worker's queue depth and the number of
5xx responses on `/checkout`, across any of your apps.

Custom dashboards are an internal preview (ADR-20261022173833).

## Create one

Describe the widgets in a JSON file:

```json
[
  {"title": "Checkout p95", "kind": "app_metric", "app": "shop", "metric": "latency_p95_ms", "window": "1h"},
  {"title": "Checkout 5xx", "kind": "log_count", "app": "shop", "query": "route=/checkout&status=5xx", "window": "6h"},
  {"title": "Orders waiting", "kind": "custom_metric", "app": "payments", "metric": "orders_pending", "window": "15m"},
  {"title": "Checkout SLO", "kind": "slo", "app": "shop", "slo_id": "6f0c…"}
]
```

```sh
gregale dashboards create checkout --file checkout.json
gregale dashboards list
gregale dashboards get DASHBOARD_ID
gregale dashboards replace DASHBOARD_ID --name checkout --file checkout.json
gregale dashboards rm DASHBOARD_ID
```

## Widgets

| Kind | Shows | Fields |
|---|---|---|
| `app_metric` | an app's request metric over the window | `metric`: `request_count`, `error_rate_pct`, `latency_p50_ms`, `latency_p95_ms`, `latency_p99_ms` or `cold_start_pct` |
| `log_count` | how many log events match a [log search](log-search.md) | `query`: the same filters as `gregale logs search` (`source`, `level`, `status`, `route`, `deployment_id`, `request_id`, `trace_id`, `q`) |
| `custom_metric` | a metric you push | `metric`: its name |
| `slo` | an SLO's error budget | `slo_id` |

- `window` is `5m`, `1h` (the default), `6h`, `24h` or `7d`; SLO widgets use
  the SLO's own window. A `log_count` with a text filter (`q`) covers at most
  24 hours.
- Widgets name apps you own by slug. If an app is deleted later, its widgets
  show as unavailable and the rest of the dashboard still loads.
- An account keeps up to 20 dashboards of up to 24 widgets each.
