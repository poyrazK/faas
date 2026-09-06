from typing import Literal

CreateAlertRuleRequestMetric = Literal[
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

CREATE_ALERT_RULE_REQUEST_METRIC_VALUES: set[CreateAlertRuleRequestMetric] = {
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


def check_create_alert_rule_request_metric(value: str) -> CreateAlertRuleRequestMetric:
    if value in CREATE_ALERT_RULE_REQUEST_METRIC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_ALERT_RULE_REQUEST_METRIC_VALUES!r}")
