from typing import Literal

OutboundAIBudgetThresholdWebhookPayloadThresholdPercent = Literal[80, 100]

OUTBOUND_AI_BUDGET_THRESHOLD_WEBHOOK_PAYLOAD_THRESHOLD_PERCENT_VALUES: set[
    OutboundAIBudgetThresholdWebhookPayloadThresholdPercent
] = {
    80,
    100,
}


def check_outbound_ai_budget_threshold_webhook_payload_threshold_percent(
    value: int,
) -> OutboundAIBudgetThresholdWebhookPayloadThresholdPercent:
    if value in OUTBOUND_AI_BUDGET_THRESHOLD_WEBHOOK_PAYLOAD_THRESHOLD_PERCENT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_BUDGET_THRESHOLD_WEBHOOK_PAYLOAD_THRESHOLD_PERCENT_VALUES!r}"
    )
