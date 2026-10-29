# FaasLokiPipelineDegraded

## Meaning

The journald → shipper → Loki path is unavailable, dropping entries, or
falling behind. Loki runs on the control plane in `faas-obs.slice` and keeps
its chunks in an object-storage bucket (ADR-20261023124955, `docs/ops/logs.md`). Customer requests do not depend synchronously on Loki, so
the FaaS service may remain healthy while incident evidence is at risk.

## Triage

1. Check the affected host:

   ```sh
   systemctl status alloy --no-pager
   journalctl -u alloy -n 100 --no-pager
   curl -fsS http://<private-address>:9080/-/ready
   ```

   The shipper is Alloy (ADR-20261023185953). Where `faas_obs_log_shipper:
   promtail` is still selected, use the `promtail` unit and `/ready` instead.
   On the control plane the shipper binds to loopback, so use
   `http://127.0.0.1:9080/-/ready` there. Compute hosts bind it to their
   private transport address so the control-plane Prometheus can scrape them.

2. Check the shipper's metrics in Prometheus (Promtail's names in brackets):

   - `up{job=~"promtail|promtail-compute"}` — process scrape health for the
     local control-plane and discovered compute-host shipper targets.
   - `rate(loki_write_sent_entries_total[10m])` (`promtail_sent_entries_total`) — successful sends.
   - `increase(loki_write_dropped_entries_total[10m])` (`promtail_dropped_entries_total`) — entries lost after
     the bounded retry budget.
   - `loki_source_journal_target_lines_total` (`promtail_journal_target_lines_total`) — source ingestion.

   When the Loki meta-monitoring target is configured, also check:

   - `up{job="loki"}` — the backend metrics endpoint and its mTLS path.
   - `time() - max(loki_compactor_apply_retention_last_successful_run_timestamp_seconds)` — retention sweep age in seconds; it should remain below 26h.
   - `time() - max(loki_boltdb_shipper_compact_tables_operation_last_successful_run_timestamp_seconds)` — index compaction age; it should remain below 6h.

3. Check Loki on the control plane: `systemctl status loki` and
   `journalctl -u loki -n 100`. Bucket errors (access denied, no such bucket)
   point at `/etc/faas/secrets/loki/s3.env` or the bucket's key. An OOM kill
   means it hit `loki_memory_max`, and `systemctl show faas-obs.slice -p
   MemoryCurrent` shows whether the whole slice is at its cap. Confirm the
   private route (3100 from compute is opened only while
   `faas_obs_logs_enabled` is set), the server certificate name, client
   certificate expiry, and that the Loki `/ready` endpoint is healthy. Do not put certificate or key contents in tickets,
   Ansible output, or shell history.

   If Grafana has the optional `Loki` datasource, use Explore with the
   `X-Scope-OrgID` tenant configured by the provider. The datasource uses the
   same private mTLS boundary as the shipper; do not create a public or
   verification-disabled fallback.

4. Check local journal pressure and disk usage. Alloy persists its cursor
   under `/var/lib/alloy` (Promtail: `/var/lib/promtail/positions.yaml`); do
   not delete it during routine
   recovery. The journal remains the replay source until its retention window
   expires.

## Recovery

Restart the shipper only after the endpoint and credentials are corrected:

```sh
systemctl restart alloy
```

After recovery, confirm `loki_send_total` increases and the dropped-entry
counter stops increasing. A restart resumes from the persisted positions file;
it must not be replaced with an empty file as a workaround for backlog.

## Rotation and retention

The certificates are fleet-PKI feature leaves (`docs/ops/logs.md`,
Certificates). The daily `pki-renew` workflow reissues them before expiry and
restarts the services whose leaf changed. To reissue by hand, converge the
`platform_logs_pki` role on the host. Check a leaf's expiry with:

```sh
openssl x509 -in /etc/faas/tls/loki/server.crt -noout -enddate
``` Loki keeps 14 days;
the bucket's lifecycle rule is the backstop. The compactor alerts cover stale
retention and index compaction. Local disk holds only the WAL and index
cache, so the host disk alerts cover it. Log shipping is not an archival or
billing source.
