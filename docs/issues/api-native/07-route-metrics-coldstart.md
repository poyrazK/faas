# Sub-issue #07 — Route-level cold-start + egress attribution

Parent: [README.md](README.md)

## Problem

ADR-093 (`docs/adr/093-per-route-app-metrics.md`) already ships
per-route traffic / latency / errors with a 50-route cap per app and a
`__route_other__` overflow bucket. The implementation is at
`pkg/gateway/handler.go:4782-4801` with the cardinality framework at
ADR-093:108-118.

What's still missing per the audit:

1. **Cold-start attribution per route.** ADR-093:61-73 explicitly
   excludes per-route cold-boot timing. The metric
   `gateway_wake_latency_seconds` is app-level only.
2. **Per-route egress bytes.** Currently `gateway_egress_bytes_total` is
   unlabeled beyond `app_id`.

Both are useful for the "1 endpoint with abnormal error rate" workflow
where the cause is cold-boot on a heavy route.

## Proposal

### Cold-start attribution

Wake events carry the request's `route` label (the label is already
written by the time the wake fires — the route is known at gateway
ingress). New metric:

```
gateway_wake_latency_seconds{
  app_id, route, trigger         # existing labels
}
```

The wake path is in `pkg/fcvm/wake.go` and the metric emission in
`pkg/wire/metrics.go`. Add `route` as a label using the same bounded
label-set machinery as ADR-093 (cap = 50, `__route_other__` overflow,
reserved labels admitted free). `consumer_id` from #05 is admitted free
as a reserved label.

### Egress attribution

`gateway_egress_bytes_total{app_id}` becomes
`gateway_egress_bytes_total{app_id, route}` for opted-in apps (the same
opt-in flag ADR-093 introduced for route metrics).

### Cardinality budget

Per-app series count after this change:

- existing route-metrics: 14 series × 50 routes = 700 histograms/counters
  (ADR-093:314-322 budget).
- + wake-latency histogram: 1 × 50 = 50.
- + egress counter: 1 × 50 = 50.
- + `consumer_id` reserved label: doesn't add a dimension (admitted free).

Total per opted-in app ≈ 800 series — still well under the cardinality
budget framework ADR-093 cites.

### Limits

None new — uses ADR-093's existing `route_metrics_per_app_cap = 50`.

## Acceptance

1. After the change, a wake triggered by a request to a known route
   emits `gateway_wake_latency_seconds{app_id, route, trigger}` with the
   correct route label.
2. The cold-start budget dashboard panel breaks down by route, sorted
   descending by p99.
3. Per-route egress matches the sum of `gateway_egress_bytes_total{app_id}`
   for the same time window (reconciliation property test).
4. Property test: 10k fuzzed routes through one app, assert `≤ 51`
   distinct route labels (mirror `pkg/wire/metrics_cardinality_test.go`).

## Dependencies

- ADR-093 (already shipped — this is a follow-on).
- #05 (consumer_id label, but only as a free reserved label, not a new
  dimension).

## Audit provenance

- `docs/adr/093-per-route-app-metrics.md:101-125` — 50-route cap.
- `docs/adr/093-per-route-app-metrics.md:61-73` — cold-start explicitly
  excluded.
- `pkg/gateway/handler.go:4782-4801` — current route metrics emission.
