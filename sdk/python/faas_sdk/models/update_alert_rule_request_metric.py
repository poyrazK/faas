from typing import Literal

UpdateAlertRuleRequestMetric = Literal[
    "account_spend_eur",
    "api_up",
    "cert_expiry_seconds",
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
    "queue_depth",
    "request_count",
]

UPDATE_ALERT_RULE_REQUEST_METRIC_VALUES: set[UpdateAlertRuleRequestMetric] = {
    "account_spend_eur",
    "api_up",
    "cert_expiry_seconds",
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
    "queue_depth",
    "request_count",
}


def check_update_alert_rule_request_metric(value: str) -> UpdateAlertRuleRequestMetric:
    if value in UPDATE_ALERT_RULE_REQUEST_METRIC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_ALERT_RULE_REQUEST_METRIC_VALUES!r}")
