# Serving probe

Every alert Prometheus evaluates is delivered by Alertmanager on the control
plane. When the control plane is down, nothing pages. The serving probe covers
that gap (ADR-20261023124955 §5).

## What it does

A systemd timer (`faas-serving-probe.timer`) on each compute node requests one
or more probe-app URLs through the public edge every 5 minutes.

- A URL fails when two attempts, 30 s apart, both fail its response checks:
  a 2xx status, the configured first-byte latency budget, and an exact response
  body when configured. A redirect counts as a failure. HTTP 200 alone does
  not pass a configured content or latency check.
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
  - `faas_serving_probe_content_check_enabled`
  - `faas_serving_probe_first_byte_budget_seconds`

  These are for dashboards while the control plane is up.

GitHub-hosted runners cannot do this job, because Cloudflare blocks their
egress to the public host. The self-hosted runners cannot either, because they
run on the control plane.

## Set up

1. **Deploy a probe app.** Use a small HTTP app that answers `200` on `/`
   with the exact text `gregale-serving-probe-ok` (no trailing newline),
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
   faas_serving_probe_expected_body: gregale-serving-probe-ok
   faas_serving_probe_max_first_byte_seconds: 2
   ```

   The same expected body and budget apply to every configured URL. Choose a
   budget for the full public path, including network time and waking the app;
   this is separate from the platform-only snapshot-restore target. It must
   be positive and no greater than `faas_serving_probe_timeout_seconds` (20 s
   by default). The expected body is limited to 4096 UTF-8 bytes without
   control characters. Responses are read only far enough to reject an
   unexpected suffix, and response content is never put into notifications.

   Existing configurations remain compatible: an empty expected body keeps
   status-only content validation, and the first-byte budget defaults to the
   attempt timeout. For an upgrade, deploy the marker endpoint first, then
   set these variables and converge. Check
   `faas_serving_probe_content_check_enabled == 1` and the reported budget to
   verify the stronger checks are active. Requests send `Cache-Control:
   no-cache, no-store`; configure the probe route to bypass edge caches too.

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

The scheduled canary also needs a dedicated external missing-run check using
the `CANARY_HEARTBEAT_URL` production environment secret. Only successful first
scheduled attempts refresh it; see the
[setup and qualification procedure](synthetic-canary-heartbeat.md). This check
detects a workflow that stops running, including when no failure step executes.

## Coverage from deployment intent

The operator API and `gregalectl obs serving-health` expose this coverage
without opening probe URLs. See [operator serving health](serving-health.md)
for states, URL fingerprints and the distinction between unknown and empty
inventory.

Supply one shared fleet policy to **both control-plane and compute
convergence**, through inventory group variables shared by both groups or the
same extra-vars file. Compute-only variables cannot establish what the control
plane should expect.

```yaml
faas_serving_probe_fleet:
  fsn-2:
    urls: [https://probe.example.gregale.app/]
    expected_body: gregale-serving-probe-ok
    max_first_byte_seconds: 2
    interval: 5min
    max_age_seconds: 900
  fsn-3:
    urls: [https://probe.example.gregale.app/]
    expected_body: gregale-serving-probe-ok
    max_first_byte_seconds: 2
```

Keys must match stable compute registry node names (`faas_node_name`), not IP
addresses or Ansible connection aliases. Entries need unique HTTPS URLs and a
nonempty exact body. Optional `timeout_seconds` and `retry_delay_seconds`
default to 20 and 30. The first-byte budget defaults to the timeout; maximum
result age defaults to 900 s and must cover at least three probe intervals.
The inventory is bounded to 1000 node/URL pairs. Avoid overriding legacy node
settings while using the fleet policy: the mapping supplies those settings too.

The `prometheus` role publishes independent control-plane expectations in
`serving-probe-inventory.prom`. Missing a whole node, a result file, or one URL
cannot erase its own expectation. A healthy sibling node or URL cannot make
up for it. The file contains node names, URLs and timing budgets; it contains
neither response bodies nor notification credentials.

Converge the control plane, then compute nodes with the same policy. Allow the
next probe run and scrape before judging results. Remove a node's entry and
converge both sides to retire its probes. Unlisted nodes and URLs are outside
this assessment. Omitting the inventory (`null`) reports **Unconfigured**;
an explicit `{}` means zero probes are expected.

The existing **FaaS fleet** Grafana dashboard adds inventory/source status,
expected probe count, coverage per node, and a table of each expected node
and URL:

| Value | State | Meaning |
| --- | --- | --- |
| 0 | Healthy | Fresh, unique evidence, content checks on, matching budget, and passing response. |
| 1 | Missing result | The expected node/URL has no result, including after its series disappears. |
| 2 | Stale result | The scrape or probe run cannot establish freshness. |
| 3 | Invalid telemetry | Required metadata is missing, duplicated or invalid. |
| 4 | Content validation disabled | The running probe reports its content check is off. |
| 5 | Latency budget mismatch | The running budget differs from deployment intent. |
| 6 | Probe failing | The probe ran with the expected checks and its response failed. |

`FaasServingProbeCoverageDegraded` warns after a fault lasts ten minutes.
`FaasServingProbeInventoryUnavailable` warns when the independent inventory
source is missing, stale, ambiguous or inconsistent. Unavailable inventory
leaves coverage unknown. These checks depend on the control-plane monitoring
stack; keep the direct webhook and external heartbeat for control-plane outages.

## Turn it off

Set `faas_serving_probe_urls: []` and converge. The role stops the timer and
removes the units, the helper, the configuration and the metrics file. It
leaves the credential files in place.
