# Serving probe

Every alert Prometheus evaluates is delivered by Alertmanager on the control
plane. When the control plane is down, nothing pages. The serving probe covers
that gap (ADR-20261023124955 §5).

## What it does

A systemd timer (`faas-serving-probe.timer`) on each compute node requests one
or more probe-app URLs through the public edge every 5 minutes.

- A URL fails when two attempts, 30 s apart, both miss a 2xx. A redirect counts
  as a failure.
- A failure posts to a chat webhook directly from the node, without going
  through the control plane. Recovery posts as well.
- While a URL keeps failing, a reminder posts at most once an hour.
- A run where every URL passes pings an optional external heartbeat check, so
  the probe itself is watched.
- Each node reports independently. A fleet-wide outage therefore arrives once
  per compute node, and the node name is in every message.
- `serving-probe.prom` in the node_exporter textfile directory carries:
  - `faas_serving_probe_success{url}`
  - `faas_serving_probe_first_byte_seconds{url}`
  - `faas_serving_probe_http_status{url}`
  - `faas_serving_probe_last_run_timestamp_seconds`

  These are for dashboards while the control plane is up.

GitHub-hosted runners cannot do this job, because Cloudflare blocks their
egress to the public host. The self-hosted runners cannot either, because they
run on the control plane.

## Set up

1. **Deploy a probe app.** Use a small HTTP app that answers `200` on `/`,
   deployed in the platform's internal account (the same one the release
   acceptance script uses).
   - Keep its idle timeout shorter than the probe interval: the 60 s plan
     default, or a function's 30 s. Every probe is then a real wake, and the
     app parks between probes.
   - Never attach a cron or a synthetic check to it. An app held warm blocks
     node drains, which caused the rc.290 fsn-3 outage.

2. **Create the chat webhook.** Use a Slack incoming webhook, or a Discord
   webhook URL with `/slack` appended. It can be the channel Alertmanager
   already pages.

3. **Optional: create a heartbeat check per compute node** in the same external
   service the `FaasWatchdog` heartbeat uses. Set the period to 5 minutes and
   the grace to at least 10. It alerts when that node's probe stops passing or
   stops running.

4. **On each compute node, write the credentials.** Paste the URLs from the
   clipboard rather than typing them; they are secrets.

   ```bash
   sudo install -d -m 0700 -o root -g root /etc/faas/secrets/serving-probe
   sudo sh -c 'umask 077; cat > /etc/faas/secrets/serving-probe/webhook_url'
   sudo sh -c 'umask 077; cat > /etc/faas/secrets/serving-probe/heartbeat_url'
   sudo chmod 0400 /etc/faas/secrets/serving-probe/*
   ```

5. **List the probe URLs** in the compute operator variables
   (`COMPUTE_ANSIBLE_VARS_B64`) and converge the compute nodes. The role refuses
   to install the probe without a root-only `webhook_url`.

   ```yaml
   faas_serving_probe_urls:
     - https://probe.example.gregale.app/
   ```

6. **Verify on a node:**

   ```bash
   sudo systemctl start faas-serving-probe.service
   journalctl -u faas-serving-probe.service -n 20 --no-pager
   cat /var/lib/node_exporter/textfile_collector/serving-probe.prom
   ```

## Synthetic canary fallback

`synthetic-canary.yml` pages through the control plane's Alertmanager. When
that push fails, it posts to the `CANARY_FALLBACK_WEBHOOK_URL` repository
secret instead. Set that secret to the same chat webhook.

## Turn it off

Set `faas_serving_probe_urls: []` and converge. The role stops the timer and
removes the units, the helper, the configuration and the metrics file. It
leaves the credential files in place.
