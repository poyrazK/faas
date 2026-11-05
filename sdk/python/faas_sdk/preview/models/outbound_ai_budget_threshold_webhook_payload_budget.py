from typing import Literal

OutboundAIBudgetThresholdWebhookPayloadBudget = Literal[
    "app_daily_tokens",
    "daily_cost",
    "daily_tokens",
    "monthly_cost",
    "tag_daily_cost",
    "user_daily_cost",
    "user_daily_tokens",
]

OUTBOUND_AI_BUDGET_THRESHOLD_WEBHOOK_PAYLOAD_BUDGET_VALUES: set[OutboundAIBudgetThresholdWebhookPayloadBudget] = {
    "app_daily_tokens",
    "daily_cost",
    "daily_tokens",
    "monthly_cost",
    "tag_daily_cost",
    "user_daily_cost",
    "user_daily_tokens",
}


def check_outbound_ai_budget_threshold_webhook_payload_budget(
    value: str,
) -> OutboundAIBudgetThresholdWebhookPayloadBudget:
    if value in OUTBOUND_AI_BUDGET_THRESHOLD_WEBHOOK_PAYLOAD_BUDGET_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_BUDGET_THRESHOLD_WEBHOOK_PAYLOAD_BUDGET_VALUES!r}"
    )
