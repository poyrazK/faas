from typing import Literal

OutboundAIQualityAlertMetric = Literal[
    "bad_feedback", "empty", "evaluation_score", "invalid_json", "refused", "tool_error", "truncated"
]

OUTBOUND_AI_QUALITY_ALERT_METRIC_VALUES: set[OutboundAIQualityAlertMetric] = {
    "bad_feedback",
    "empty",
    "evaluation_score",
    "invalid_json",
    "refused",
    "tool_error",
    "truncated",
}


def check_outbound_ai_quality_alert_metric(value: str) -> OutboundAIQualityAlertMetric:
    if value in OUTBOUND_AI_QUALITY_ALERT_METRIC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_QUALITY_ALERT_METRIC_VALUES!r}")
