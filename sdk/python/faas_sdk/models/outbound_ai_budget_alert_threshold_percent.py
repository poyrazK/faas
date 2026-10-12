from typing import Literal

OutboundAIBudgetAlertThresholdPercent = Literal[80, 100]

OUTBOUND_AI_BUDGET_ALERT_THRESHOLD_PERCENT_VALUES: set[OutboundAIBudgetAlertThresholdPercent] = {
    80,
    100,
}


def check_outbound_ai_budget_alert_threshold_percent(value: int) -> OutboundAIBudgetAlertThresholdPercent:
    if value in OUTBOUND_AI_BUDGET_ALERT_THRESHOLD_PERCENT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_BUDGET_ALERT_THRESHOLD_PERCENT_VALUES!r}"
    )
