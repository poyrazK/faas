from typing import Literal

AlertRuleResponseMetric = Literal[
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

ALERT_RULE_RESPONSE_METRIC_VALUES: set[AlertRuleResponseMetric] = {
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


def check_alert_rule_response_metric(value: str) -> AlertRuleResponseMetric:
    if value in ALERT_RULE_RESPONSE_METRIC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_RULE_RESPONSE_METRIC_VALUES!r}")
