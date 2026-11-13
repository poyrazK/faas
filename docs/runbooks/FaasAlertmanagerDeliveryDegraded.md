# FaasAlertmanagerDeliveryDegraded

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.

This runbook covers `FaasAlertmanagerNotificationsFailed` and
`FaasAlertmanagerConfigReloadFailed`, `FaasNotificationReceiverCollectorUnavailable`,
`FaasNotificationReceiverTelemetryMissing` and
`FaasNotificationReceiverRequestsFailing`. Alertmanager may still be receiving
alerts while one or more email/Pushover integrations fail, or it may still be
running with its previous configuration after a failed reload.

## Symptoms

Prometheus continues evaluating alerts, but email or Pushover notifications
are missing, delayed, or routed using older receiver settings.

## Triage

1. Check service state, readiness, and recent delivery errors:

   ```bash
   systemctl status alertmanager --no-pager
   journalctl -u alertmanager --since "30 min ago" --no-pager
   curl -fsS http://127.0.0.1:9094/-/ready
   ```

2. Query the failure counters and identify the affected integration:

   ```promql
   increase(alertmanager_notifications_failed_total[10m])
   alertmanager_config_last_reload_successful
   ```

3. Check the rendered receiver configuration and operator-provisioned secret
   files. Verify SMTP reachability/TLS and Pushover credentials for the
   integration named by the metric or log entry. Do not copy secret contents
   into tickets or chat.

   For receiver warnings, inspect the fleet dashboard's notification panels
   and the collector timer:

   ```bash
   systemctl status faas-notification-receiver-metrics.timer --no-pager
   journalctl -u faas-notification-receiver-metrics --since "30 min ago" --no-pager
   systemctl cat alertmanager
   ```

   The Alertmanager unit must include
   `--enable-feature=receiver-name-in-metrics`. Compare
   `faas_notification_receiver:expected` with
   `faas_notification_receiver:telemetry_present`. A missing group can indicate
   a failed configuration reload; a failed or frozen collector makes evidence
   unavailable. Fix collection before interpreting transport history.

4. If a configuration reload failed, validate the generated Alertmanager
   configuration before retrying. The previous valid configuration may still
   be routing pages, so compare the intended receiver and route changes with
   the active configuration.

## Recovery

Repair the Ansible inputs or secret-file permissions, deploy the corrected
configuration, and reload Alertmanager. Confirm
`alertmanager_config_last_reload_successful == 1` and that the failure counter
stops increasing. Use a controlled test notification according to the local
on-call procedure; do not create an ad-hoc public receiver just to test the
path.

Run `make notification-delivery-check` for the isolated local receipt and
failure drill. See [notification delivery evidence](../ops/notification-delivery.md)
for its scope and the difference between transport observations, provider
receipts and human acknowledgement. Local success does not qualify production
provider delivery.
