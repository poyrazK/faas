# FaasAlertmanagerDeliveryDegraded

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.

This runbook covers `FaasAlertmanagerNotificationsFailed` and
`FaasAlertmanagerConfigReloadFailed`. Alertmanager may still be receiving
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
