# Customer log-drain degraded

Alerts:

- `FaasCustomerLogDrainBacklog`
- `FaasCustomerLogDrainDeliveryFailed`
- `FaasCustomerLogDrainDataLoss`

These are customer-runtime log export warnings. The application request path
is independent from the drain and should remain available while the external
destination is unhealthy.

## First checks

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
confirm the destination URL and rotate the credential if necessary.

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

The current delivery path is best effort and does not provide a durable
per-drain cursor or replay ledger. Treat drops and gaps as real data loss and
record the affected time window in the incident notes.
