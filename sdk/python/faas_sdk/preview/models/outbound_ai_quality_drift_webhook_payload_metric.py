from typing import Literal

OutboundAIQualityDriftWebhookPayloadMetric = Literal[
    "bad_feedback", "empty", "evaluation_score", "invalid_json", "refused", "tool_error", "truncated"
]

OUTBOUND_AI_QUALITY_DRIFT_WEBHOOK_PAYLOAD_METRIC_VALUES: set[OutboundAIQualityDriftWebhookPayloadMetric] = {
    "bad_feedback",
    "empty",
    "evaluation_score",
    "invalid_json",
    "refused",
    "tool_error",
    "truncated",
}


def check_outbound_ai_quality_drift_webhook_payload_metric(value: str) -> OutboundAIQualityDriftWebhookPayloadMetric:
    if value in OUTBOUND_AI_QUALITY_DRIFT_WEBHOOK_PAYLOAD_METRIC_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_QUALITY_DRIFT_WEBHOOK_PAYLOAD_METRIC_VALUES!r}"
    )
