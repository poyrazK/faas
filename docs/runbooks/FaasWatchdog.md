# FaasWatchdog (dead-man's switch) and alert delivery setup

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml` (group `faas_watchdog`)
and `deploy/ansible/roles/alertmanager`.
Severity: info. The alert always fires by design.

## Symptom

`FaasWatchdog` is permanently firing. That is correct. Alertmanager routes
it to the `faas-heartbeat` receiver, which posts to an external heartbeat
monitor every `am_heartbeat_interval` (default 5m). The external monitor is
what alerts you when the posts stop. In that case one of these is down:

- the control-plane host or its network path to the internet;
- Prometheus, or rule evaluation;
- Alertmanager, or its outbound delivery.

Every other alert is evaluated and delivered by those same components, so
the heartbeat is the only signal that survives their failure.

## Setup

A control-plane host refuses to bootstrap without a notification channel
and a heartbeat (`alertmanager — refuse a silent control plane`).

1. Create the heartbeat check in an external service that alerts when
   pings stop: healthchecks.io, Better Stack, Cronitor or similar. Set the
   period to `am_heartbeat_interval` and a grace of at least twice that.
   Point its own notifications somewhere that does not depend on Gregale.
2. Create the chat webhook:
   - Discord: channel settings → Integrations → Webhooks → New Webhook →
     Copy Webhook URL, then append `/slack`. Alertmanager 0.27 cannot read
     a native Discord receiver URL from a file, so the role uses Discord's
     Slack-compatible endpoint and refuses a URL without the suffix.
   - Slack: an incoming-webhook URL (`https://hooks.slack.com/services/…`).
3. On the control plane, write each URL to its own file. The URLs are
   credentials: paste them from your clipboard rather than typing them into
   shell history, and never commit them.

   ```bash
   sudo install -d -o alertmanager -g alertmanager -m 0750 /etc/alertmanager/secrets
   sudo install -o alertmanager -g alertmanager -m 0400 /dev/null /etc/alertmanager/secrets/discord-webhook-url
   sudo install -o alertmanager -g alertmanager -m 0400 /dev/null /etc/alertmanager/secrets/heartbeat-url
   sudoedit /etc/alertmanager/secrets/discord-webhook-url   # one line: the URL
   sudoedit /etc/alertmanager/secrets/heartbeat-url         # one line: the ping URL
   ```

4. Set the role variables for the control plane (inventory or `-e`), with
   `am_dev_mode` unset or false:

   ```yaml
   am_discord_webhook_url_file: /etc/alertmanager/secrets/discord-webhook-url
   am_heartbeat_url_file: /etc/alertmanager/secrets/heartbeat-url
   ```

5. Re-run the control-plane bootstrap, then confirm Alertmanager is enabled
   and the heartbeat monitor shows a ping within one interval.

## Check

```bash
systemctl is-enabled alertmanager; systemctl is-active alertmanager
amtool --alertmanager.url=http://127.0.0.1:9094 alert query alertname=FaasWatchdog
amtool config routes test --config.file=/etc/alertmanager/alertmanager.yml \
  alertname=FaasWatchdog severity=info component=alertmanager family=watchdog
journalctl -u alertmanager --since '-30m' --no-pager | grep -iE 'notify|error' | tail -20
```

The routes test must print `faas-heartbeat`. If it prints `faas-silent`,
`am_heartbeat_url_file` was empty when the config was rendered.

To prove chat delivery end to end, fire a short-lived test alert:

```bash
amtool --alertmanager.url=http://127.0.0.1:9094 alert add FaasDeliveryTest \
  severity=page component=alertmanager family=delivery_test \
  --annotation=summary='Delivery test, safe to ignore' --end="$(date -u -d '+5 minutes' +%Y-%m-%dT%H:%M:%SZ)"
```

## Recover

- Heartbeat stopped and the control plane is unreachable: treat it as a
  control-plane outage; check the provider console first.
- Control plane reachable: check `systemctl status prometheus alertmanager`,
  then `FaasPrometheusRuleEvaluationFailed` and
  [FaasAlertmanagerDeliveryDegraded](FaasAlertmanagerDeliveryDegraded.md).
- Chat posts failing with HTTP 400 from Discord: the stored URL is missing
  the `/slack` suffix or the webhook was deleted; recreate the file and
  re-run the bootstrap.
- Never silence `FaasWatchdog`: a silence stops the heartbeat and the
  external monitor will page.
