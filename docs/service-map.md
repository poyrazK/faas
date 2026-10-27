# Service map

The service map shows which of your apps call which, how often, how reliably,
and how fast. It is built from Gregale's own record of every call made through
[internal service routing](networking.md#internal-services), so it needs no SDK,
agent, or tracing setup in your code.

The service map is an internal preview (ADR-1131). It is available on Hobby,
Pro, and Scale once enabled for your deployment.

## Read the map

```sh
gregale metric --services             # last hour
gregale metric --services --range 24h
gregale metric --services --json      # for scripts and agents
```

```text
Range:      1h
Source:     prometheus

public-api → payments
  Calls:      12040 (errors 48, 0.40%)
  Latency:    p50=11.2ms p95=38.0ms
  Woke:       9030 calls (75%) found the target parked
  Tip:        run `gregale app wake-ahead public-api on` to restore payments while public-api wakes

payments → postgres
  TCP:        3120 sessions (errors 4, 210 woke the target)
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
| Woke target | Calls that found the target parked (scaled to zero) and waited for it to restore, and their share of all calls on the edge. |
| Wake-ahead used | For a caller with wake-ahead on: how many of the restores it started for this target were then called while fresh (`wake_ahead_used` of `wake_ahead_started`). A low share means wake-ahead is waking the target for nothing. |
| TCP sessions | Connections the caller opened to the target's [private TCP ports](networking.md#private-tcp-between-services) — databases, queues, and other non-HTTP services (`tcp_sessions`). Of those, `tcp_errors` failed after the caller was allowed in (no replica, the target could not be woken or reached, or the connection broke), and `tcp_cold_sessions` found the target parked and waited for it to restore. Sessions refused because the caller was not allowed to reach the target, or because the account was at its plan's session limit, are not counted. |

## Cold calls and wake-ahead

A call to a parked service waits while Gregale restores it. When most calls on
an edge do, the caller pays that restore again and again. Wake-ahead
(`gregale app wake-ahead <app> on`) lets Gregale start restoring the services an
app calls while the app itself wakes, along the edges it has measured.

The map marks an edge with a wake-ahead tip when the caller has not turned
wake-ahead on yet, at least 20 calls or TCP sessions in the window woke the
target, and those are at least half of the edge's traffic — the same evidence
wake-ahead itself needs before it acts. The API sets `suggest_wake_ahead: true` on such
edges and `wake_ahead_enabled` on each app. Wake-ahead restores bill like any
wake (plan RAM per running second until the idle timeout parks the service).

An edge with only TCP sessions has no HTTP calls, so it shows no latency.
Wake-ahead learns from TCP sessions as it does from calls, so such an edge can
still carry a wake-ahead tip.

Only calls between apps in your account appear. Traffic from the internet and
calls your app makes to outside services are not edges on the map; use
`gregale metrics <app>` and request analytics for those.

## Limits

- The map shows at most 500 edges, busiest first (HTTP calls plus TCP
  sessions). When more exist, the
  response sets `truncated: true` and the CLI and dashboard say so.
- Apps that made or received no internal calls in the window are not listed.
- If metrics are temporarily unavailable, the response keeps HTTP 200 with
  `source: "degraded: <reason>"` and no edges, rather than a partial map.
