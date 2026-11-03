# Alerts: operator notes

Installation notes behind the customer [alerts page](../alerts.md). They moved
here so that page can be published (#5709).

- The workflow backlog preset (`workflow_due_age_seconds`) needs the
  backlog-alert migration and the evaluator upgrade before customers enable
  it; it never enables itself for existing apps.
- `login_target_signal_health` reads Prometheus. A Prometheus error marks the
  rule `degraded` without firing.
- Baseline (unusual-value) rules are a preview (ADR-744) enabled per
  installation.
