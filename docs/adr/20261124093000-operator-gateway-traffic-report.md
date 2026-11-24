# ADR-20261124093000: Operator gateway traffic report

Status: Accepted

Extends ADR-20261115175927 (operator console routes) and
ADR-20261123163340 (operator build queue report, whose Prometheus read this
follows).

## Context

The console shows how wakes perform (Wake latency) but not what happened to
requests. When a customer reports a 502 or 503, an operator cannot see from
the console:

- whether the gateway produced the error before the guest answered, or the
  guest did;
- whether the request waited in a wake queue and the queue was full (the
  per-app queue, 512 waiters, or the gateway-wide admission queue) or the
  wait timed out;
- whether an app was out of capacity (429), or an edge gate or the compute
  edge turned the request away;
- which nodes and apps account for most of it, and what is held right now.

gatewayd already exports all of this to Prometheus:

| Series | Labels |
| --- | --- |
| `gateway_requests_total` | app, plan, full status code |
| `gateway_platform_errors_total` | app, plan (5xx before the guest's first byte) |
| `gateway_capacity_refusals_total` | app |
| `gateway_wake_admission_total` | plan, outcome |
| `gateway_wake_queue_depth`, `gateway_wake_admission_queue_depth` | app, plan / plan |
| `gateway_edge_rejections_total` | app, kind, status |
| `gatewayd_public_edge_refused_total` | reason |
| `gateway_retry_exhausted_total` | reason |
| `gatewayd_public_overload_level` | (node) |

Each compute node's gatewayd targets carry the metrics-discovery `node`
label.

## Decision

### One operator session read, Prometheus only

| Route | Returns |
| --- | --- |
| `GET /v1/admin/obs/gateway-traffic?window=1h` | `ObsGatewayTrafficReport` |

`window` is 5m to 168h and defaults to 1h. The route runs fourteen instant
PromQL queries concurrently, `increase()` over the window for counters and
the current value for the queue-depth and overload gauges. It then runs one
more query counting requests for exactly the apps the report lists, named
by ID (validated as UUIDs) once the list is known. Every query is evaluated
at the same instant, so totals, nodes and apps describe one moment. The
read gets `ObsGatewayTrafficPrometheusBudget` (3 s) for windows up to 24h
and `ObsGatewayTrafficLongWindowBudget` (10 s) beyond. No store query, no
migration; apid looks up only the slug and account of the apps it lists (at
most 25).

`pkg/apid/gatewaytraffic` (ADR-982) reports:

- **Totals:** requests by status class, 429/502/503/504, the 5xx rate,
  platform errors and their rate, capacity refusals, wake admission
  outcomes (admitted, the app's queue full, the gateway-wide queue full,
  timeouts), requests held for a wake now and wake leaders waiting for
  admission.
- **Per node:** requests, 5xx, platform errors, capacity refusals, held
  now and the compute edge's overload level.
- **Breakdowns:** every status code seen; wake admission outcomes by plan;
  edge rejections by gate kind and status; compute edge refusals and retry
  give-ups by reason.
- **Apps:** the 25 most affected, ordered by platform errors, then 5xx,
  capacity refusals and requests held now, with each app's requests and 5xx
  rate. gatewayd's row for hosts that name no app is marked
  `unknown_host`.

`source` says whether the read was `observed`, `partial` (the failed
queries are named and their sections are empty), `unavailable` or
`not_configured`. Counts are `increase()` rounded to whole numbers, so
they are estimates across counter resets and scrape gaps.

The route is operator-only (`requireOperatorSessionRead`), sends
`Cache-Control: no-store`, and is excluded from spec compliance. Limits
are in `pkg/api/limits.go` (`ObsGatewayTraffic*`).

## Consequences

- **Console:** gets a Gateway traffic page under On call.
- **Prometheus:** each page view runs fifteen queries over the window. Every
  per-app query keeps only apps with errors, refusals or waiters and is
  bounded by `topk`, so a long window does not return every app. A 7-day
  window is the heaviest read; if it misses its 10 s budget, the report
  comes back `partial` and names the queries that missed.
- **Retention:** a window longer than Prometheus retention reports only
  what is retained.
- **Not covered:** per-request detail (request telemetry and the support
  lookup have it) and gatewayd-public's own status codes, which the
  compute edge does not label by app.
