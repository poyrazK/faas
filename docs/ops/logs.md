# Platform logs

Platform daemon logs leave each host through Grafana Alloy (ADR-20261023185953;
Promtail, end of life, stays selectable for one release with
`faas_obs_log_shipper: promtail`) and land in Loki on the
control plane (ADR-20261023124955 §2, amending ADR-143).

- **Storage.** Loki keeps its chunks in an object-storage bucket for 14 days.
  Losing the control plane loses only its last few minutes of logs. The
  compute nodes' shipper replays up to 12 h of journal into a recovered
  control plane.
- **What ships.** Every `faas-*` unit, PostgreSQL and the observability stack.
- **What stays local.** Debug lines stay in the local journal.
- **Labels:**
  - `host`, `unit`, `level`, `daemon`;
  - `account_id` and `app_id` on customer app-log records.
- **Correlation fields.** `request_id`, `wake_id`, `trace_id` and `span_id` are
  fields inside the JSON line, never labels.
- **Resources.** Loki and the shipper run in `faas-obs.slice`, below PostgreSQL
  and the daemons for CPU.

## Turn it on

Do this once per environment, after the measurement gate in the ADR (§7): `scripts/ops/obs_gate.py` on a non-production control plane must print GO.

1. **Create a dedicated bucket** and an HMAC/S3 key that can read, write and
   delete in it only.
   - Add a lifecycle rule that deletes objects after 16 days, as a backstop for
     Loki's 14-day compactor.
   - On GCS, use HMAC keys and the S3-interop endpoint
     `storage.googleapis.com`.
2. **Write the bucket key on the control plane** as root-only:

   ```bash
   sudo install -d -m 0700 -o root -g root /etc/faas/secrets/loki
   sudo sh -c 'umask 077; cat > /etc/faas/secrets/loki/s3.env'   # AWS_ACCESS_KEY_ID=… and AWS_SECRET_ACCESS_KEY=…, one per line
   sudo chmod 0400 /etc/faas/secrets/loki/s3.env
   ```

3. **Set the operator variables** for the control plane and the compute nodes
   (`CP_ANSIBLE_VARS_B64`, `COMPUTE_ANSIBLE_VARS_B64`) and converge both:

   ```yaml
   faas_obs_logs_enabled: true
   faas_obs_loki_address: 10.0.0.2        # the control plane's private address
   loki_storage_backend: s3               # control plane
   loki_s3_endpoint: storage.googleapis.com
   loki_s3_bucket: gregale-prod-loki
   # loki_s3_ca_file: /etc/faas/tls/ca/ca.crt   # only for an endpoint on a private CA
   ```

   From these the roles derive:
   - the mTLS certificates (see below);
   - the shipper's push URL;
   - Prometheus's Loki and shipper scrape jobs (still named `promtail` and
     `promtail-compute`), which enable the
     `faas_loki_pipeline` alerts;
   - Grafana's Loki datasource;
   - the firewall openings: 3100 from compute to the control plane, and 9080
     from the control plane to compute.

4. **Verify:**
   - `systemctl status loki alloy` on the control plane;
   - `systemctl status alloy` on a compute node;
   - the "Loki pipeline" Grafana dashboard;
   - `FaasLokiShippingStalled` and `FaasLokiBackendDown` stay quiet.

## Cutover from Promtail

On a host where Promtail ran, the first converge with Alloy selected:

1. stops Promtail;
2. starts Alloy reading back only 10 minutes;
3. waits until Alloy has saved its own journal position;
4. returns the read-back window to 12 hours.

The 10 minutes cover the gap. Lines sent twice are identical, and Loki drops
them. The step runs once per host (`/var/lib/alloy/.cutover-from-promtail-done`).
To roll back, set `faas_obs_log_shipper: promtail` and converge:
`role_convergence` stops Alloy and the promtail role starts Promtail.

## Certificates

There is nothing to provision. `platform_logs_pki` issues the pipeline's mTLS
leaves from the fleet CA (`pkg/pki` feature `platform-logs`):

| Leaf | Hosts | Used by |
|---|---|---|
| `/etc/faas/tls/loki/server.{crt,key}` (`loki.faas`) | control plane | Loki's listener |
| `/etc/faas/tls/promtail/loki-client.*` | every host | the shipper's pushes (the path keeps Promtail's name) |
| `/etc/faas/tls/prometheus/loki-client.*` | control plane | Prometheus's Loki scrape, `gregalectl obs logs` |
| `/etc/faas/tls/grafana/loki-client.*` | control plane | Grafana's datasource (read with `$__file{}`) |

- **Where they are issued:** the control plane issues all of them. A compute
  node's shipper leaf is issued into
  `/var/lib/faas/pki-features/<node>/` on the control plane and copied to the
  node, the way realtimed's live-query leaf is.
- **Not required leaves:** they are feature leaves, which no host role
  requires, so they never block a rollout of hosts that lack them.
- **Renewal:** the daily `pki-renew` workflow reissues each leaf 30 days
  before expiry (`pki_renew.yml`). It restores each service's read access and
  restarts only the services whose leaf changed.
- **Trust:** Loki verifies clients against the fleet CA, so any leaf that CA
  signed is accepted. The private-network rule on port 3100, which admits only
  compute nodes, keeps everything else out.

## Search from the command line

On the control plane, as root. The command uses Prometheus's Loki client
leaf, and `/etc/faas/loki-client.env`, rendered by the loki role, holds the
URL and file paths:

```bash
sudo gregalectl obs logs --wake-id <wake_id> --since 6h            # one wake across every host, oldest first
sudo gregalectl obs logs --request-id <request_id>
sudo gregalectl obs logs --host fsn-3 --level error --since 30m
sudo gregalectl obs logs --unit schedd --grep "startup: layer attestations prepared" --since 24h
sudo gregalectl obs logs --query 'sum by (unit) (count_over_time({level="ERROR"}[1h]))'   # metric queries are rejected; use Grafana
```

- Each line prints as `time host unit LEVEL message key=value…`.
- `--json` emits the entries with their labels.
- By default the command returns the newest `--limit` lines (200, at most
  5,000) in the range and prints them oldest first. `--forward` returns the
  oldest instead.
- The range is `--since` (default 1h), or `--from` / `--to`, and can be at
  most 14 days.

## Query recipes (Grafana → Explore → Loki)

Lines are the daemons' own JSON (the shipper drops journald's envelope), so
`| json` exposes every field.

| Question | LogQL |
|---|---|
| Everything one wake did, across hosts | `{job="faas-journal"} \| json \| wake_id="<wake_id>"` |
| One request end to end | `{job="faas-journal"} \| json \| request_id="<request_id>"` |
| One trace (also from a `trace_id` exemplar) | `{job="faas-journal"} \| json \| trace_id="<trace_id>"` |
| Errors on one node in the last hour | `{host="fsn-3", level="ERROR"}` |
| One daemon on every node | `{unit="faas-schedd.service"}` |
| A customer app's log records | `{app_id="<app_id>"}` |
| Error rate by daemon | `sum by (unit) (rate({level="ERROR"}[5m]))` |

The `wake_id` exemplar on a slow `gateway_wake_latency_seconds` bucket gives
the id for the first row. The same id keys the events table's wake-phase
events. `sudo gregalectl obs wake <wake_id>` prints those events and this
row's log lines as one timeline, with the gap between rows.

## Turn it off

Set `faas_obs_logs_enabled: false` and converge. `role_convergence` stops and
disables `loki` and the shipper. It leaves their files and users in place, so
turning logs back on needs only the flag. The bucket keeps the last 14 days
until the lifecycle rule removes them.
