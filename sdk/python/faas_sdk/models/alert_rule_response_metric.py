from typing import Literal

AlertRuleResponseMetric = Literal[
    "account_spend_eur",
    "api_up",
    "cert_expiry_seconds",
    "cert_issuance_failed",
    "cold_start_pct",
    "cold_wake_rate_pct",
    "daily_cost_cents",
    "deployment_failed",
    "error_rate_pct",
    "failed_invocations",
    "latency_p50_ms",
    "latency_p95_ms",
    "latency_p99_ms",
    "new_error_fingerprint",
    "pre_auth_target_signal_gap_pct",
    "pre_auth_target_threshold",
    "queue_depth",
    "request_count",
    "slo_burn_rate",
    "workflow_due_age_seconds",
    "workflow_failures",
    "workflow_pending_age_seconds",
    "workflow_schedule_quota_skips",
    "workflow_waiting_age_seconds",
]

ALERT_RULE_RESPONSE_METRIC_VALUES: set[AlertRuleResponseMetric] = {
    "account_spend_eur",
    "api_up",
    "cert_expiry_seconds",
    "cert_issuance_failed",
    "cold_start_pct",
    "cold_wake_rate_pct",
    "daily_cost_cents",
    "deployment_failed",
    "error_rate_pct",
    "failed_invocations",
    "latency_p50_ms",
    "latency_p95_ms",
    "latency_p99_ms",
    "new_error_fingerprint",
    "pre_auth_target_signal_gap_pct",
    "pre_auth_target_threshold",
    "queue_depth",
    "request_count",
    "slo_burn_rate",
    "workflow_due_age_seconds",
    "workflow_failures",
    "workflow_pending_age_seconds",
    "workflow_schedule_quota_skips",
    "workflow_waiting_age_seconds",
}


def check_alert_rule_response_metric(value: str) -> AlertRuleResponseMetric:
    if value in ALERT_RULE_RESPONSE_METRIC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_RULE_RESPONSE_METRIC_VALUES!r}")
