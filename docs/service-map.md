# Service map

The service map shows which of your apps call which, how often, how reliably,
and how fast. It is built from Gregale's own record of every call made through
[internal service routing](networking.md#internal-services), so it needs no SDK,
agent, or tracing setup in your code.

The service map is an internal preview (ADR-740). It is available on Hobby,
Pro, and Scale once enabled for your deployment.

## Read the map

```sh
gregale metrics --services             # last hour
gregale metrics --services --range 24h
gregale metrics --services --json      # for scripts and agents
```

```text
Range:      1h
Source:     prometheus

public-api → payments
  Calls:      12040 (errors 48, 0.40%)
  Latency:    p50=11.2ms p95=38.0ms
```

The same map is on the dashboard at **Service map** (`/dashboard/service-map`)
and in the API:

```sh
curl -H "Authorization: Bearer $GREGALE_TOKEN" \
  "https://api.gregale.dev/v1/service-map?range=1h"
```

`range` is one of `5m`, `15m`, `1h` (default), `6h`, `24h`, `7d`, or `15d`.

## What the numbers mean

| Field | Meaning |
| --- | --- |
| Calls | Every call from the caller to the target in the window. Calls are counted, not sampled. |
| Errors | Calls that ended in a 5xx after Gregale's retries. Calls refused because the caller was not allowed to reach the target are not counted. |
| Error rate | Errors divided by calls. The dashboard highlights edges at 5% or more. |
| p50 / p95 | Latency of successful calls, measured by Gregale from the caller's request to the target's response. It includes routing and, when the target was parked, the time to wake it. |

Only calls between apps in your account appear. Traffic from the internet and
calls your app makes to outside services are not edges on the map; use
`gregale metrics <app>` and request analytics for those.

## Limits

- The map shows at most 500 edges, busiest first. When more exist, the
  response sets `truncated: true` and the CLI and dashboard say so.
- Apps that made or received no internal calls in the window are not listed.
- If metrics are temporarily unavailable, the response keeps HTTP 200 with
  `source: "degraded: <reason>"` and no edges, rather than a partial map.
