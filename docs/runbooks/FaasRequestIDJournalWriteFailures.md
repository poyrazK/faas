# FaasRequestIDJournalWriteFailures

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metrics: `gateway_request_id_journal_write_total{result}` and
`gateway_request_id_journal_write_duration_seconds`.
Severity: warn after failed writes exceed 1/min for 2 minutes.

## Impact

The exact public `x-faas-request-id` mapping is a mandatory write before
the gateway sends work to the guest. A failed write returns 503 and the
guest is not invoked for that request. The alert is fleet-wide by design;
the metric has only the closed `result` label and never records app IDs,
request IDs, paths, or payloads.

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
3. Confirm the counters return to zero failure rate and journal-write
   latency is below the gateway's 2-second RPC deadline before closing
   the alert.

Do not bypass or make this write best-effort to clear the alert: that
would allow a request to execute without the exact lookup record the
public debugger promises. Requests rejected at this gate have not been
forwarded to the guest, so callers can retry after the write path recovers.
