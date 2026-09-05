# FaasLokiPipelineDegraded

## Meaning

The off-host journald → Promtail → Loki path is unavailable, dropping entries,
or falling behind. Customer requests do not depend synchronously on Loki, so
the FaaS service may remain healthy while incident evidence is at risk.

## Triage

1. Check the affected host:

   ```sh
   systemctl status promtail --no-pager
   journalctl -u promtail -n 100 --no-pager
   curl -fsS http://<promtail-private-address>:9080/ready
   ```

   On the control plane, the default Promtail bind is loopback, so use
   `http://127.0.0.1:9080/ready` there. Compute hosts bind Promtail to their
   private transport address so the control-plane Prometheus can scrape them.

2. Check Promtail metrics in Prometheus:

   - `up{job=~"promtail|promtail-compute"}` — process scrape health for the
     local control-plane and discovered compute-host Promtail targets.
   - `rate(promtail_sent_entries_total[10m])` — successful sends.
   - `increase(promtail_dropped_entries_total[10m])` — entries lost after
     the bounded retry budget.
   - `promtail_journal_target_lines_total` — source ingestion.

3. Check the Loki host and endpoint. Confirm the private route, the server
   certificate name, client certificate expiry, and that the Loki `/ready`
   endpoint is healthy. Do not put certificate or key contents in tickets,
   Ansible output, or shell history.

4. Check local journal pressure and disk usage. Promtail persists its cursor in
   `/var/lib/promtail/positions.yaml`; do not delete that file during routine
   recovery. The journal remains the replay source until its retention window
   expires.

## Recovery

Restart Promtail only after the endpoint and credentials are corrected:

```sh
systemctl restart promtail
```

After recovery, confirm `loki_send_total` increases and the dropped-entry
counter stops increasing. A restart resumes from the persisted positions file;
it must not be replaced with an empty file as a workaround for backlog.

## Rotation and retention

Rotate the Loki server/client certificates through the operator secret
provisioning process, then converge `site.yml` and restart the affected
services. Loki retention defaults to seven days in this role. The backend's
filesystem must be monitored independently; log shipping is not an archival
or billing source.
