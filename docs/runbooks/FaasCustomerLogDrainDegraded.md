# Customer log-drain degraded

Alerts:

- `FaasCustomerLogDrainBacklog`
- `FaasCustomerLogDrainDeliveryFailed`
- `FaasCustomerLogDrainDataLoss`

These are customer-runtime log export warnings. The application request path
is independent from the drain and should remain available while the external
destination is unhealthy.

## Symptom

The alert means a customer log drain is building backlog, failing delivery, or
losing records while the application request path remains available.

## Check

Inspect the affected app and destination in Prometheus:

```promql
gateway_log_drain_active{app="<app>", kind="<kind>"}
gateway_log_drain_queue_depth{app="<app>", kind="<kind>"}
gateway_log_drain_queue_capacity{app="<app>", kind="<kind>"}
rate(gateway_log_drain_retries_total{app="<app>", kind="<kind>"}[10m])
rate(gateway_log_drain_delivery_latency_seconds_sum{app="<app>", kind="<kind>"}[10m])
  / rate(gateway_log_drain_delivery_latency_seconds_count{app="<app>", kind="<kind>"}[10m])
```

Then inspect gatewayd-internal logs for the drain ID and endpoint response
status. Authentication headers are never logged. Use the app log-drain API to
confirm the destination URL and rotate the credential if necessary. For the
current per-drain snapshot, use
`GET /v1/apps/{slug}/log-drains/{id}/health` or the app dashboard's Log-drain
health page; the snapshot includes queue, delivery, retry, drop, gap, and
last-success/failure state without exposing credentials or raw endpoint
errors.

## Interpret the alert

- Backlog plus retries or rising latency means the endpoint is slow or
  unreachable. Once the bounded queue fills, new records are dropped so the
  application path is not blocked.
- Delivery failures with no recent success usually indicate an endpoint,
  credential, certificate, or egress problem. A 2xx response clears the
  failure condition after the alert window expires.
- `gateway_log_drain_gaps_total` means the per-instance runtime ring advanced
  past records before the drain consumed them. Those records cannot be
  recovered from the drain path.
- `gateway_log_drain_stream_reconnects_total` rising without endpoint
  failures indicates instability in the source stream or its schedd route.

## Recover

The delivery path is best effort. The per-drain health snapshot is durable,
but it does not provide a cursor or replay ledger. Treat drops and gaps as
real data loss and record the affected time window in the incident notes.
Resolve endpoint, credential, certificate, or egress issues, then confirm the
queue drains and the success timestamp advances. If records were dropped or
gaps were reported, notify the customer and record the affected time window;
those records cannot be replayed from the drain path.
