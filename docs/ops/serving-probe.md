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
- Each attempt has one total deadline (`faas_serving_probe_timeout_seconds`,
  20 s by default), covering DNS, connection, TLS, headers and body reads.
  Receiving occasional bytes does not extend it. An expired attempt records
  failure code `4` (`timeout`); the probe stops and reaps its worker, applies
  the usual retry policy and continues to later URLs. Observed HTTP status
  and first-byte timing are retained when the body times out. Before any
  first byte, the timing field records elapsed time to failure.
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
  - `faas_serving_probe_failure_code{url}`
  - `faas_serving_probe_first_failure_code{url}`
  - `faas_serving_probe_attempts{url}`
  - `faas_serving_probe_last_run_timestamp_seconds`
  - `faas_serving_probe_content_check_enabled`
  - `faas_serving_probe_first_byte_budget_seconds`

  These are for dashboards while the control plane is up.

The diagnostic code table and retry fields are described in
[operator serving health](serving-health.md#inspect-failure-and-retry-diagnostics).
Logs and notification messages identify URLs by their exact-URL SHA-256 ID and
use fixed failure text. Raw URLs remain in private configuration/state and
source metric labels for inventory joins. Webhook and heartbeat errors do not
print credential URLs or provider exception text.

The Linux helper runs one worker process at a time under the existing systemd
service limits. The parent enforces deadlines independently of socket activity
and closes worker resources on completion, timeout or cancellation. Allow for
process cleanup and scheduling overhead around the configured deadline. The
service's outer timeout remains a final guard for the complete run.

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

## Direct notification transport evidence

The compute-node probe sends its webhook and heartbeat independently of
Alertmanager. Its transport metrics cover this separate path. Each submission
has a fixed **10-second total deadline**, including DNS, connection, TLS,
headers, body and response cleanup. The worker is stopped and reaped when the
deadline expires. Occasional response bytes do not extend the deadline.
Process cleanup and scheduling can add overhead.

Credentials must use HTTPS. Requests reject redirects and non-2xx status codes.
A successful submission requires a complete response body of at most **4096
bytes**. The helper reads at most 4097 bytes to detect an oversized response;
response content never appears in metrics, logs or worker results. A timeout,
incomplete response or oversized response leaves success unconfirmed even if
the provider already accepted the request. Retrying can therefore duplicate a
message. A completed 2xx request establishes transport submission only, without
proof of provider processing, external watchdog receipt or human acknowledgement.

`serving-probe.prom` includes these gauges with only
`channel="webhook"` or `channel="heartbeat"` as their local label:

| Metric | Meaning |
| --- | --- |
| `faas_serving_probe_notification_enabled` | 1 when a nonempty credential is configured; 0 when disabled. |
| `faas_serving_probe_notification_attempted` | 1 when a submission was attempted in this run; 0 when skipped. |
| `faas_serving_probe_notification_last_attempt_success` | 1 or 0 for the last completed submission; absent until known. |
| `faas_serving_probe_notification_last_attempt_failure_code` | Last completed submission's fixed failure code; absent until known. |
| `faas_serving_probe_notification_last_attempt_timestamp_seconds` | Completion time of the last attempt; absent until known. |
| `faas_serving_probe_notification_last_success_timestamp_seconds` | Last completed successful submission time; absent until known. |

Use the existing [failure code table](serving-health.md#inspect-failure-and-retry-diagnostics).
Notification requests use none, DNS, connection, TLS, timeout, HTTP status and
unknown codes. Unknown includes oversized/incomplete responses or an invalid
credential URL. A missing credential reports disabled; it does not manufacture
a failed request or a successful receipt. A configured channel that has never
completed a request has no last-attempt or last-success samples.

A quiet webhook run is intentionally skipped. A failing serving run suppresses
the heartbeat as before. Both cases report `attempted=0` and retain any known
completed-attempt history. An old success is historical evidence and cannot
prove the channel works now. A later failed attempt reports success 0 while
retaining the previous success timestamp. Never treat a skipped attempt or a
missing timestamp as a successful submission. Check the source run clock
(`faas_serving_probe_last_run_timestamp_seconds`) before using these gauges;
fresh node-exporter scrapes cannot make a frozen result file current.

History is saved independently in the service's `notifications.json`, mode
0600, including failed submissions. A private destination hash binds each
channel's history to its exact credential. Credential rotation, disablement,
invalid/future history or an unavailable state file leaves that history unknown.
No credential URL, provider error text or response body is saved. Failed state
writes emit fixed error text and still publish the current in-memory observation;
the next run can only retain history that reached disk.

Serving results are atomically published before provider calls. A final atomic
write adds the observed submission outcomes, including failures, while retaining
the original probe-completion clock so provider latency cannot refresh serving
evidence. A webhook
failure keeps its incident message due for the next run and does not prevent
an otherwise eligible heartbeat attempt. Heartbeat failures remain independent
of serving health and its existing exit policy: they log a fixed failure and
report success 0 in these metrics, while a healthy serving run can still exit 0.
The metrics add visibility while the control-plane monitoring stack is up;
retain the external missing-heartbeat check for control-plane outages.

See [ADR-20261113220001](../adr/20261113220001-serving-probe-notification-transport.md).

### Warnings from completed notification failures

The serving coverage rule group normalizes the direct notification metrics for
nodes in the shared fleet inventory. Each expected node has two telemetry
slots, `webhook` and `heartbeat`; a slot does not require the optional heartbeat
credential to be enabled. With valid inventory, missing notification metrics
from an older helper are unknown. Without usable inventory, these rules make
no channel assessment; inventory warnings remain separate.

`faas_serving_probe_notification:state{probe_node,channel}` reports:

| Value | Meaning |
| --- | --- |
| 0 | The channel is explicitly disabled, with no attempted request or retained history. |
| 1 | The completed outcome is unknown, including no history, invalid data or an unavailable source. |
| 2 | The last completed submission succeeded; this is historical transport evidence. |
| 3 | The last completed submission failed and no later success is known. |

Companion recordings expose validated configuration and completed-attempt
metadata: `:enabled`, `:last_attempt_success`, `:last_attempt_failure_code`,
`:last_attempt_timestamp_seconds` and `:last_success_timestamp_seconds`, all
under the `faas_serving_probe_notification` prefix. Read metadata with its state;
individually usable clocks do not establish a complete submission outcome.
The internal normalization helpers also retain reviewed source identity and
fixed field names; arbitrary source labels are removed.

Source validation requires one current discovered compute target, `up=1`, a
fresh probe run within its inventory age policy and exactly one raw run clock.
All fields used for an outcome must be unique, finite, correctly typed and
from the same scrape as `up` and the run clock. Scrapes must be within 120
seconds, with at most 30 seconds of future clock skew. Completion clocks must
fit the observation clock. A reported success requires its success clock to
match completion; a failure requires an allowed code and no success clock
newer than completion. A malformed, stale or foreign optional success clock
cannot be mistaken for absent history. Disabled channels with contradictory
history or attempted flags are unknown.

`FaasServingProbeNotificationFailed` warns after state 3 has remained present
for **five minutes**. Its value is the fixed failure code, and its labels retain
only the node/channel and normal alert routing metadata. Skips preserve an
existing failure and its original attempt time. A fresh run reporting an old
failure does not make that attempt newer or prove a current provider outage.
Unknown evidence or disablement also clears this warning, so resolution alone
does not prove successful delivery. The source run clock and channel state
must still be inspected. Keep external missing-heartbeat monitoring for
control-plane outages and shared-provider failures.

`faas_serving_probe_notification:evidence_status{probe_node,channel}` explains
unknown outcomes separately from the existing state codes:

| Value | Meaning |
| --- | --- |
| 0 | Complete evidence for the last completed submission or an explicitly disabled channel. |
| 1 | A valid enabled channel has no submission history and was not attempted in this run. |
| 2 | Required notification fields are missing from a fresh, unique probe source. |
| 3 | Notification telemetry is invalid or inconsistent, including duplicate fields or scrape-time mismatches. |
| 4 | The probe source cannot establish fresh, unique evidence under its node policy. |

The recording follows independent inventory and preserves only node/channel
labels. It checks required enabled/attempted fields, and requires complete
outcome fields after an attempted request or when any history is present.
Success requires its success clock; failure may legitimately have no prior
success. A quiet channel or credential rotation with cleanly cleared history
has code 1. An intentionally disabled channel with no contradictory history
has code 0. No usable inventory yields no channel assessment.

`FaasServingProbeNotificationTelemetryDegraded` warns after code 2 or 3 has
remained present for **five minutes**, with the coarse outcome still unknown.
Its value is the fixed evidence code. It identifies notification telemetry loss
on an otherwise fresh probe source; source outages and invalid inventory remain
covered by existing warnings. A failed submission can transition to lost
telemetry, clearing the submission-failure warning and raising this evidence
warning after its hold. Valid no-history channels and disabled channels are
excluded. Restored telemetry, rotation or disablement can clear the warning
without proving delivery. See the
[telemetry runbook](../runbooks/FaasServingProbeNotificationTelemetryDegraded.md)
and [ADR-20261114020000](../adr/20261114020000-serving-probe-notification-telemetry.md).

See the [runbook](../runbooks/FaasServingProbeNotificationFailed.md) and
[ADR-20261113230001](../adr/20261113230001-serving-probe-notification-alerts.md).

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
