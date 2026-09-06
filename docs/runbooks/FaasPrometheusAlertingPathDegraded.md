# FaasPrometheusAlertingPathDegraded

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.

This runbook covers `FaasPrometheusRuleEvaluationFailed`,
`FaasPrometheusNotificationsDropped`, and
`FaasPrometheusNotificationQueueBacklog`. These alerts mean the local
Prometheus evaluator or its hand-off to Alertmanager is impaired. The
platform may still serve traffic, but new pages or recording rules can be
late or missing.

## Symptoms

Pages may be delayed or absent while the Prometheus and Alertmanager
processes still appear ready. Dashboards can also show stale recording rules
when only one rule group is failing.

## Triage

1. Check both services and their recent logs:

   ```bash
   systemctl status prometheus alertmanager --no-pager
   journalctl -u prometheus -u alertmanager --since "30 min ago" --no-pager
   ```

2. Confirm the local endpoints are ready:

   ```bash
   curl -fsS http://127.0.0.1:9095/-/ready
   curl -fsS http://127.0.0.1:9094/-/ready
   ```

3. Inspect the affected Prometheus counters and queue:

   ```promql
   increase(prometheus_rule_evaluation_failures_total[10m])
   increase(prometheus_notifications_dropped_total[10m])
   prometheus_notifications_queue_length
   prometheus_notifications_queue_capacity
   ```

4. Open `http://127.0.0.1:9095/rules` and check for rule groups with an
   evaluation error. A malformed rule, an invalid query, or resource pressure
   can leave a recording rule stale even when the process is ready.

5. If the queue is growing, inspect Alertmanager readiness and its delivery
   failures using `FaasAlertmanagerDeliveryDegraded`. Restore Alertmanager
   reachability before restarting Prometheus; a restart does not recover an
   unavailable receiver.

## Recovery

Validate the rendered configuration and rules before restarting either
service. Use the Ansible role to repair configuration drift, then restart the
affected service only after the local readiness endpoint and logs are clean.

These alerts are intentionally local self-observations. If the Prometheus
process stops completely, this evaluator cannot page about its own outage;
the multi-host/federated monitoring follow-up must provide that external
failure domain.
