# FaasRequestIDJournalWriteFailures

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metrics: `gateway_request_id_journal_write_total{result}` and
`gateway_request_id_journal_write_duration_seconds`.
Severity: warn after failed writes exceed 1/min for 2 minutes.

## Impact

The exact public `x-faas-request-id` mapping is a debugging index. Since
ADR-634 the gateway queues it and still serves the request when the write
fails; since ADR-1521 the queue writes records to apid in batches of up to
128. A failed record means its request ID may not resolve in the debugger.
The alert fires when failed records are above 1 per minute and above 5% of
all records for 2 minutes. It is fleet-wide by design; the metric has only
the closed `result` label (`recorded`, `rejected`, `failed`, `dropped`) and
never records app IDs, request IDs, paths, or payloads. `rejected` records
were refused by apid (malformed, app not owned by the account, or the plan
keeps no debugger data) and do not page.

## Verify

```promql
sum by (result) (rate(gateway_request_id_journal_write_total[5m]))
```

Compare failures with successful writes and inspect the write latency:

```promql
sum(increase(gateway_request_id_journal_write_total{result="failed"}[15m]))

histogram_quantile(
  0.95,
  sum by (le) (rate(gateway_request_id_journal_write_duration_seconds_bucket[5m]))
)
```

## Triage

1. Check `gatewayd-internal` logs for `request ID journal` and `persist
   request ID journal` errors. Connection failures point to the private
   apid gRPC target or TLS/socket configuration.
2. Check apid health and its Postgres connection pool. Repeated write
   failures usually mean the journal insert is being rejected or the
   apid listener is unavailable; correlate with Postgres capacity and
   apid database error metrics.
3. If gatewayd-internal logs `apid has no batch RPC`, the control plane's
   apid predates ADR-1521 and the gateway is writing one record per RPC. That
   is expected during a rollout; it is not the cause of failures.
4. Confirm the counters return to a low failure share and the batch write
   latency is below the gateway's 2-second RPC deadline before closing the
   alert.

Do not make the gateway wait for the journal to clear the alert: ADR-634
keeps the journal off the request's critical path on purpose.
